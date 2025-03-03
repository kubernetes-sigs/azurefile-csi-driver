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

package azurefile

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"k8s.io/klog/v2"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/fileshareclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/storage"
)

type azureFileMgmtClient struct {
	fileShareClient fileshareclient.Interface
	accountOptions  *storage.AccountOptions
}

func newAzureFileMgmtClient(cloud *storage.AccountRepo, accountOptions *storage.AccountOptions) (azureFileClient, error) {
	if cloud == nil || cloud.ComputeClientFactory == nil {
		return nil, fmt.Errorf("cloud provider is not initialized")
	}
	if accountOptions == nil {
		return nil, fmt.Errorf("accountOptions is nil")
	}
	fileShareClient, err := cloud.ComputeClientFactory.GetFileShareClientForSub(accountOptions.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get file share client for subscription %s: %w", accountOptions.SubscriptionID, err)
	}

	return &azureFileMgmtClient{
		accountOptions:  accountOptions,
		fileShareClient: fileShareClient,
	}, nil

}

// CreateFileShare creates a file share, using a matching storage account type, account kind, etc.
// storage account will be created if specified account is not found
func (az *azureFileMgmtClient) CreateFileShare(ctx context.Context, shareOptions *ShareOptions) error {
	if shareOptions == nil {
		return fmt.Errorf("shareOptions of account(%s) is nil", az.accountOptions.Name)
	}
	az.accountOptions.EnableHTTPSTrafficOnly = true
	if shareOptions.Protocol == armstorage.EnabledProtocolsNFS {
		az.accountOptions.EnableHTTPSTrafficOnly = false
	}
	shareOps := armstorage.FileShare{
		Name:                to.Ptr(shareOptions.Name),
		FileShareProperties: &armstorage.FileShareProperties{},
	}
	if shareOptions.RequestGiB > 0 {
		quota := int32(shareOptions.RequestGiB)
		shareOps.FileShareProperties.ShareQuota = &quota
	}
	if shareOptions.Protocol == armstorage.EnabledProtocolsNFS {
		shareOps.FileShareProperties.EnabledProtocols = to.Ptr(shareOptions.Protocol)
	}
	if shareOptions.AccessTier != "" {
		shareOps.FileShareProperties.AccessTier = to.Ptr(armstorage.ShareAccessTier(shareOptions.AccessTier))
	}
	if shareOptions.RootSquash != "" {
		shareOps.FileShareProperties.RootSquash = to.Ptr(armstorage.RootSquashType(shareOptions.RootSquash))
	}
	if shareOptions.Metadata != nil {
		shareOps.FileShareProperties.Metadata = shareOptions.Metadata
	}
	if _, err := az.fileShareClient.Create(ctx, az.accountOptions.ResourceGroup, az.accountOptions.Name, shareOptions.Name, shareOps, nil); err != nil {
		return fmt.Errorf("failed to create share %s in account %s: %w", shareOptions.Name, az.accountOptions.Name, err)
	}
	klog.V(4).Infof("created share %s in account %s", shareOptions.Name, az.accountOptions.Name)
	return nil
}

// DeleteFileShare deletes a file share using storage account name and key
func (az *azureFileMgmtClient) DeleteFileShare(ctx context.Context, shareName string) error {
	if err := az.fileShareClient.Delete(ctx, az.accountOptions.ResourceGroup, az.accountOptions.Name, shareName, nil); err != nil {
		return err
	}
	klog.V(4).Infof("share %s deleted", shareName)
	return nil
}

// ResizeFileShare resizes a file share
func (az *azureFileMgmtClient) ResizeFileShare(ctx context.Context, name string, sizeGiB int) error {
	fileShare, err := az.fileShareClient.Get(ctx, az.accountOptions.ResourceGroup, az.accountOptions.Name, name, nil)
	if err != nil {
		return err
	}
	if int(*fileShare.FileShareProperties.ShareQuota) >= sizeGiB {
		klog.Warningf("file share size(%dGi) is already greater or equal than requested size(%dGi), accountName: %s, shareName: %s",
			*fileShare.FileShareProperties.ShareQuota, sizeGiB, az.accountOptions.Name, name)
		return nil
	}

	fileShare.FileShareProperties.ShareQuota = to.Ptr(int32(sizeGiB))
	_, err = az.fileShareClient.Update(ctx, az.accountOptions.ResourceGroup, az.accountOptions.Name, name, *fileShare)
	return err
}

// GetFileShare gets a file share
func (az *azureFileMgmtClient) GetFileShareQuota(ctx context.Context, name string) (int, error) {
	share, err := az.fileShareClient.Get(ctx, az.accountOptions.ResourceGroup, az.accountOptions.Name, name, nil)
	if err != nil {
		return -1, err
	}
	if share.FileShareProperties == nil || share.FileShareProperties.ShareQuota == nil {
		return -1, fmt.Errorf("FileShareProperties or FileShareProperties.ShareQuota is nil")
	}
	return int(*share.FileShareProperties.ShareQuota), nil
}
