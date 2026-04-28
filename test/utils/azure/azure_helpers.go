/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package azure

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	resources "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	storage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"github.com/google/uuid"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/accountclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/fileshareclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/resourcegroupclient"
)

type Client struct {
	subscriptionID   string
	groupsClient     resourcegroupclient.Interface
	accountsClient   accountclient.Interface
	filesharesClient fileshareclient.Interface
	vmssClient       *armcompute.VirtualMachineScaleSetsClient
	vmClient         *armcompute.VirtualMachinesClient
	roleClient       *armauthorization.RoleAssignmentsClient
}

func GetAzureClient(cloud, subscriptionID, clientID, tenantID, clientSecret, aadFederatedTokenFile string) (*Client, error) {
	armConfig := &azclient.ARMClientConfig{
		Cloud:     cloud,
		TenantID:  tenantID,
		UserAgent: azurefile.GetUserAgent(azurefile.DefaultDriverName, "", "e2e-test"),
	}
	clientOps, _, err := azclient.GetAzureCloudConfigAndEnvConfig(armConfig)
	if err != nil {
		return nil, err
	}
	useFederatedWorkloadIdentityExtension := false
	if aadFederatedTokenFile != "" {
		useFederatedWorkloadIdentityExtension = true
	}
	credProvider, err := azclient.NewAuthProvider(armConfig, &azclient.AzureAuthConfig{
		AADClientID:                           clientID,
		AADClientSecret:                       clientSecret,
		AADFederatedTokenFile:                 aadFederatedTokenFile,
		UseFederatedWorkloadIdentityExtension: useFederatedWorkloadIdentityExtension,
	})
	if err != nil {
		return nil, err
	}
	cred := credProvider.GetAzIdentity()
	factory, err := azclient.NewClientFactory(&azclient.ClientFactoryConfig{
		SubscriptionID: subscriptionID,
	}, armConfig, clientOps, cred)
	if err != nil {
		return nil, err
	}

	armClientOpts, err := azclient.GetDefaultResourceClientOption(armConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get default ARM client options: %v", err)
	}
	armClientOpts.Cloud = clientOps

	vmssClient, err := armcompute.NewVirtualMachineScaleSetsClient(subscriptionID, cred, armClientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create VMSS client: %v", err)
	}
	vmClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, cred, armClientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create VM client: %v", err)
	}
	roleClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, armClientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create role assignments client: %v", err)
	}

	return &Client{
		subscriptionID:   subscriptionID,
		groupsClient:     factory.GetResourceGroupClient(),
		accountsClient:   factory.GetAccountClient(),
		filesharesClient: factory.GetFileShareClient(),
		vmssClient:       vmssClient,
		vmClient:         vmClient,
		roleClient:       roleClient,
	}, nil
}

func (az *Client) GetAzureFilesClient() (fileshareclient.Interface, error) {
	return az.filesharesClient, nil
}

func (az *Client) EnsureResourceGroup(ctx context.Context, name, location string, managedBy *string) (resourceGroup *resources.ResourceGroup, err error) {
	var tags map[string]*string
	group, err := az.groupsClient.Get(ctx, name)
	if err == nil && group.Tags != nil {
		tags = group.Tags
	} else {
		tags = make(map[string]*string)
	}
	// Tags for correlating resource groups with prow jobs on testgrid
	tags["buildID"] = stringPointer(os.Getenv("BUILD_ID"))
	tags["jobName"] = stringPointer(os.Getenv("JOB_NAME"))
	tags["creationTimestamp"] = stringPointer(time.Now().UTC().Format(time.RFC3339))

	response, err := az.groupsClient.CreateOrUpdate(ctx, name, resources.ResourceGroup{
		Name:      &name,
		Location:  &location,
		ManagedBy: managedBy,
		Tags:      tags,
	})
	if err != nil {
		return response, err
	}

	return response, nil
}

func (az *Client) DeleteResourceGroup(ctx context.Context, groupName string) error {
	_, err := az.groupsClient.Get(ctx, groupName)
	if err != nil {
		return err
	}

	timeout := 20 * time.Minute
	ch := make(chan bool, 1)

	go func() {
		err = az.groupsClient.Delete(ctx, groupName)
		ch <- true
	}()

	select {
	case <-ch:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("timeout waiting for resource group %s to be deleted", groupName)
	}
}

func (az *Client) GetStorageAccount(ctx context.Context, groupName, accountName string) (*storage.Account, error) {
	return az.accountsClient.GetProperties(ctx, groupName, accountName, nil)
}

func (az *Client) GetAccountNumByResourceGroup(ctx context.Context, groupName string) (count int, err error) {
	result, err := az.accountsClient.List(ctx, groupName)
	if err != nil {
		return -1, err
	}
	return len(result), nil
}

// NodeIdentityInfo holds the principal ID, client ID, and resource ID of a user-assigned identity.
type NodeIdentityInfo struct {
	PrincipalID string
	ClientID    string
	ResourceID  string
}

// GetNodeIdentityInfo returns the principalID, clientID, and resource ID of the first
// user-assigned identity found on VMSS or VMs in the given resource group.
func (az *Client) GetNodeIdentityInfo(ctx context.Context, resourceGroup string) (*NodeIdentityInfo, error) {
	// Check VMSS first
	vmssPager := az.vmssClient.NewListPager(resourceGroup, nil)
	for vmssPager.More() {
		page, pageErr := vmssPager.NextPage(ctx)
		if pageErr != nil {
			return nil, fmt.Errorf("failed to list VMSS: %v", pageErr)
		}
		for _, vmss := range page.Value {
			if vmss.Identity == nil {
				continue
			}
			if info := pickIdentity(vmss.Identity.UserAssignedIdentities); info != nil {
				return info, nil
			}
		}
	}

	// Check VMs
	vmPager := az.vmClient.NewListPager(resourceGroup, nil)
	for vmPager.More() {
		page, pageErr := vmPager.NextPage(ctx)
		if pageErr != nil {
			return nil, fmt.Errorf("failed to list VMs: %v", pageErr)
		}
		for _, vm := range page.Value {
			if vm.Identity == nil {
				continue
			}
			if info := pickIdentity(vm.Identity.UserAssignedIdentities); info != nil {
				return info, nil
			}
		}
	}

	return nil, fmt.Errorf("no user-assigned identity with both principalID and clientID found in resource group %s", resourceGroup)
}

// pickIdentity selects the first valid identity from a map, sorting keys for determinism.
func pickIdentity(identities map[string]*armcompute.UserAssignedIdentitiesValue) *NodeIdentityInfo {
	keys := make([]string, 0, len(identities))
	for k := range identities {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, resourceID := range keys {
		uaIdentity := identities[resourceID]
		if uaIdentity != nil && uaIdentity.ClientID != nil && *uaIdentity.ClientID != "" &&
			uaIdentity.PrincipalID != nil && *uaIdentity.PrincipalID != "" {
			return &NodeIdentityInfo{
				PrincipalID: *uaIdentity.PrincipalID,
				ClientID:    *uaIdentity.ClientID,
				ResourceID:  resourceID,
			}
		}
	}
	return nil
}

// AssignRoleToIdentity assigns an Azure RBAC role to an identity by principalID.
// roleDefinitionID is the GUID of the role definition (e.g., "a235d3ee-5935-4cfb-8cc5-a3303ad5995e"
// for Storage File Data SMB MI Admin).
func (az *Client) AssignRoleToIdentity(ctx context.Context, resourceGroup, principalID, roleDefinitionID string) error {
	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", az.subscriptionID, resourceGroup)
	fullRoleDefID := fmt.Sprintf("%s/providers/Microsoft.Authorization/roleDefinitions/%s", scope, roleDefinitionID)
	assignmentName := uuid.New().String()

	_, err := az.roleClient.Create(ctx, scope, assignmentName, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      to.Ptr(principalID),
			RoleDefinitionID: to.Ptr(fullRoleDefID),
			PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
		},
	}, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
			log.Printf("Role assignment already exists for principalID %s with role %s, skipping", principalID, roleDefinitionID)
			return nil
		}
		return fmt.Errorf("failed to create role assignment: %v", err)
	}
	return nil
}

// GetStorageOAuthToken obtains an Azure Storage OAuth token using a managed identity.
// The token can be used for mounting Azure File shares with mountWithOAuthToken.
func GetStorageOAuthToken(ctx context.Context, clientID string) (string, error) {
	cred, err := azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
		ID: azidentity.ClientID(clientID),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create managed identity credential: %v", err)
	}
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://storage.azure.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get storage OAuth token: %v", err)
	}
	return token.Token, nil
}

func stringPointer(s string) *string {
	return &s
}
