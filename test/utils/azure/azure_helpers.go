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
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	armcontainerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
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
	aksClient        *armcontainerservice.ManagedClustersClient
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

	aksClient, err := armcontainerservice.NewManagedClustersClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AKS client: %v", err)
	}
	roleClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create role assignments client: %v", err)
	}

	return &Client{
		subscriptionID:   subscriptionID,
		groupsClient:     factory.GetResourceGroupClient(),
		accountsClient:   factory.GetAccountClient(),
		filesharesClient: factory.GetFileShareClient(),
		aksClient:        aksClient,
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

// GetKubeletIdentityObjectID retrieves the kubelet managed identity object ID from an AKS cluster.
// It lists all AKS clusters in the given resource group and returns the kubelet identity of the first one found.
func (az *Client) GetKubeletIdentityObjectID(ctx context.Context, resourceGroup string) (string, error) {
	pager := az.aksClient.NewListByResourceGroupPager(resourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list AKS clusters in resource group %s: %v", resourceGroup, err)
		}
		for _, cluster := range page.Value {
			if cluster.Properties != nil && cluster.Properties.IdentityProfile != nil {
				if kubeletIdentity, ok := cluster.Properties.IdentityProfile["kubeletidentity"]; ok && kubeletIdentity.ObjectID != nil {
					return *kubeletIdentity.ObjectID, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no AKS cluster with kubelet identity found in resource group %s", resourceGroup)
}

// AssignRoleToIdentity assigns an Azure role to a principal on a resource group scope.
// roleDefinitionID should be just the GUID of the role definition.
// Common roles:
//   - Storage File Data Privileged Contributor: 69566ab7-960f-475b-8e7c-b3118f30c6bd
//   - Storage File Data SMB Share Elevated Contributor: a7264617-510b-434b-a828-9731dc254ea7
func (az *Client) AssignRoleToIdentity(ctx context.Context, resourceGroup, principalID, roleDefinitionID string) error {
	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", az.subscriptionID, resourceGroup)
	fullRoleDefID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", az.subscriptionID, roleDefinitionID)
	assignmentID := uuid.New().String()

	_, err := az.roleClient.Create(ctx, scope, assignmentID, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      &principalID,
			PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
			RoleDefinitionID: &fullRoleDefID,
		},
	}, nil)
	if err != nil {
		// Ignore "RoleAssignmentExists" conflict errors
		if strings.Contains(err.Error(), "RoleAssignmentExists") {
			return nil
		}
		return fmt.Errorf("failed to create role assignment for principal %s with role %s on scope %s: %v", principalID, roleDefinitionID, scope, err)
	}
	return nil
}

// EnsureKubeletStorageFileDataRole gets the kubelet identity from the AKS cluster in the given
// resource group and assigns it the "Storage File Data Privileged Contributor" role on that resource group.
func (az *Client) EnsureKubeletStorageFileDataRole(ctx context.Context, resourceGroup string) error {
	principalID, err := az.GetKubeletIdentityObjectID(ctx, resourceGroup)
	if err != nil {
		return err
	}
	// Storage File Data Privileged Contributor
	const storageFileDataPrivilegedContributorRoleID = "69566ab7-960f-475b-8e7c-b3118f30c6bd"
	return az.AssignRoleToIdentity(ctx, resourceGroup, principalID, storageFileDataPrivilegedContributorRoleID)
}
