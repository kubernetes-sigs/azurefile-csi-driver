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
	"time"

	resources "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	storage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/accountclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/fileshareclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/resourcegroupclient"
)

type Client struct {
	groupsClient     resourcegroupclient.Interface
	accountsClient   accountclient.Interface
	filesharesClient fileshareclient.Interface
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
	return &Client{
		groupsClient:     factory.GetResourceGroupClient(),
		accountsClient:   factory.GetAccountClient(),
		filesharesClient: factory.GetFileShareClient(),
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
