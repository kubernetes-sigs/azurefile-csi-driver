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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
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

	armClientOpts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Cloud: clientOps,
		},
	}

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

func stringPointer(s string) *string {
	return &s
}

// GetNodeIdentityPrincipalIDs retrieves the principal IDs of managed identities from VMSS and VMs in the resource group.
// Supports both system-assigned and user-assigned managed identities.
// This works for both CAPZ and AKS clusters regardless of whether they use VMSS or availability set VMs.
func (az *Client) GetNodeIdentityPrincipalIDs(ctx context.Context, resourceGroup string) ([]string, error) {
	var principalIDs []string

	// Check VMSS
	log.Printf("Listing VMSS in resource group %s ...", resourceGroup)
	vmssPager := az.vmssClient.NewListPager(resourceGroup, nil)
	for vmssPager.More() {
		page, err := vmssPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list VMSS in resource group %s: %v", resourceGroup, err)
		}
		for _, vmss := range page.Value {
			name := ""
			if vmss.Name != nil {
				name = *vmss.Name
			}
			if vmss.Identity == nil {
				log.Printf("VMSS %s has no managed identity, skipping", name)
				continue
			}
			// System-assigned identity
			if vmss.Identity.PrincipalID != nil && *vmss.Identity.PrincipalID != "" {
				log.Printf("VMSS %s: found system-assigned identity, principalID=%s", name, *vmss.Identity.PrincipalID)
				principalIDs = append(principalIDs, *vmss.Identity.PrincipalID)
			}
			// User-assigned identities
			for uaID, uaIdentity := range vmss.Identity.UserAssignedIdentities {
				if uaIdentity != nil && uaIdentity.PrincipalID != nil && *uaIdentity.PrincipalID != "" {
					log.Printf("VMSS %s: found user-assigned identity %s, principalID=%s", name, uaID, *uaIdentity.PrincipalID)
					principalIDs = append(principalIDs, *uaIdentity.PrincipalID)
				}
			}
		}
	}

	// Check individual VMs (availability set scenario)
	log.Printf("Listing VMs in resource group %s ...", resourceGroup)
	vmPager := az.vmClient.NewListPager(resourceGroup, nil)
	for vmPager.More() {
		page, err := vmPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list VMs in resource group %s: %v", resourceGroup, err)
		}
		for _, vm := range page.Value {
			name := ""
			if vm.Name != nil {
				name = *vm.Name
			}
			if vm.Identity == nil {
				log.Printf("VM %s has no managed identity, skipping", name)
				continue
			}
			// System-assigned identity
			if vm.Identity.PrincipalID != nil && *vm.Identity.PrincipalID != "" {
				log.Printf("VM %s: found system-assigned identity, principalID=%s", name, *vm.Identity.PrincipalID)
				principalIDs = append(principalIDs, *vm.Identity.PrincipalID)
			}
			// User-assigned identities
			for uaID, uaIdentity := range vm.Identity.UserAssignedIdentities {
				if uaIdentity != nil && uaIdentity.PrincipalID != nil && *uaIdentity.PrincipalID != "" {
					log.Printf("VM %s: found user-assigned identity %s, principalID=%s", name, uaID, *uaIdentity.PrincipalID)
					principalIDs = append(principalIDs, *uaIdentity.PrincipalID)
				}
			}
		}
	}

	if len(principalIDs) == 0 {
		return nil, fmt.Errorf("no VMSS or VM with managed identity found in resource group %s", resourceGroup)
	}

	// Deduplicate
	seen := make(map[string]bool)
	unique := principalIDs[:0]
	for _, id := range principalIDs {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	log.Printf("Found %d unique principal IDs in resource group %s", len(unique), resourceGroup)
	return unique, nil
}

// AssignRoleToIdentity assigns an Azure role to a principal on a resource group scope.
// roleDefinitionID should be just the GUID of the role definition.
// Common roles:
//   - Storage File Data Privileged Contributor: 69566ab7-960f-475b-8e7c-b3118f30c6bd
//   - Storage File Data SMB Share Elevated Contributor: a7264617-510b-434b-a828-9731dc254ea7
func (az *Client) AssignRoleToIdentity(ctx context.Context, resourceGroup, principalID, roleDefinitionID string) error {
	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", az.subscriptionID, resourceGroup)
	fullRoleDefID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", az.subscriptionID, roleDefinitionID)

	// Deterministic assignment ID based on scope+principal+role to avoid duplicate assignments on retry
	assignmentID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(scope+principalID+roleDefinitionID)).String()

	_, err := az.roleClient.Create(ctx, scope, assignmentID, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      &principalID,
			PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
			RoleDefinitionID: &fullRoleDefID,
		},
	}, nil)
	if err != nil {
		// Ignore conflict (HTTP 409) — role assignment already exists
		var respErr *azcore.ResponseError
		if ok := errors.As(err, &respErr); ok && respErr.StatusCode == http.StatusConflict {
			return nil
		}
		return fmt.Errorf("failed to create role assignment for principal %s with role %s on scope %s: %v", principalID, roleDefinitionID, scope, err)
	}
	return nil
}

// EnsureNodeStorageFileDataRole gets the node identities from VMSS and VMs in the given
// resource group and assigns them the "Storage File Data SMB Share Elevated Contributor" role.
func (az *Client) EnsureNodeStorageFileDataRole(ctx context.Context, resourceGroup string) error {
	log.Printf("Begin to assign Storage File Data SMB Share Elevated Contributor role to node identities in resource group %s", resourceGroup)
	principalIDs, err := az.GetNodeIdentityPrincipalIDs(ctx, resourceGroup)
	if err != nil {
		log.Printf("Failed to get node identity principal IDs: %v", err)
		return err
	}
	// Storage File Data SMB Share Elevated Contributor
	const storageFileDataSMBShareElevatedContributorRoleID = "a7264617-510b-434b-a828-9731dc254ea7"
	for _, principalID := range principalIDs {
		log.Printf("Assigning Storage File Data SMB Share Elevated Contributor role to principal %s ...", principalID)
		if err := az.AssignRoleToIdentity(ctx, resourceGroup, principalID, storageFileDataSMBShareElevatedContributorRoleID); err != nil {
			log.Printf("Failed to assign role to principal %s: %v", principalID, err)
			return err
		}
		log.Printf("Successfully assigned Storage File Data SMB Share Elevated Contributor role to principal %s", principalID)
	}
	log.Printf("Successfully assigned Storage File Data SMB Share Elevated Contributor role to all %d node identities", len(principalIDs))
	return nil
}
