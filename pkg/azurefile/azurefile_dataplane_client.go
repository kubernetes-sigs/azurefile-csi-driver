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
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/service"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
	"k8s.io/klog/v2"

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/utils"
)

var (
	// refer https://github.com/Azure/azure-sdk-for-go/blob/master/storage/client.go#L88.
	defaultValidStatusCodes = []int{
		http.StatusRequestTimeout,      // 408
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
	}
)

type azureFileDataplaneClient struct {
	accountName string
	accountKey  string
	*service.Client
}

func newAzureFileClient(accountName, accountKey, storageEndpointSuffix string) (azureFileClient, error) {
	if storageEndpointSuffix == "" {
		storageEndpointSuffix = defaultStorageEndPointSuffix
	}
	keyCred, err := service.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, fmt.Errorf("error creating azure client: %v", err)
	}
	storageEndpoint := fmt.Sprintf("https://%s.file."+storageEndpointSuffix, accountName)
	clientOps := utils.GetDefaultAzCoreClientOption()
	clientOps.Retry.StatusCodes = defaultValidStatusCodes

	fileClient, err := service.NewClientWithSharedKeyCredential(storageEndpoint, keyCred, &service.ClientOptions{
		ClientOptions: clientOps,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating azure client: %v", err)
	}

	return &azureFileDataplaneClient{
		accountName: accountName,
		accountKey:  accountKey,
		Client:      fileClient,
	}, nil
}

func (f *azureFileDataplaneClient) CreateFileShare(ctx context.Context, shareOptions *ShareOptions) error {
	if shareOptions == nil {
		return fmt.Errorf("shareOptions of account(%s) is nil", f.accountName)
	}
	shareClient := f.Client.NewShareClient(shareOptions.Name)
	_, err := shareClient.Create(ctx, &share.CreateOptions{
		Quota: to.Ptr(int32(shareOptions.RequestGiB)),
	})

	if err != nil {
		return fmt.Errorf("failed to create file share, err: %v", err)
	}
	return nil
}

// delete a file share
func (f *azureFileDataplaneClient) DeleteFileShare(ctx context.Context, shareName string) error {
	_, err := f.Client.NewShareClient(shareName).Delete(ctx, nil)
	return err
}

func (f *azureFileDataplaneClient) ResizeFileShare(ctx context.Context, shareName string, sizeGiB int) error {
	shareClient := f.Client.NewShareClient(shareName)
	shareProps, err := shareClient.GetProperties(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to set quota on file share %s, err: %v", shareName, err)
	}
	if *shareProps.Quota >= int32(sizeGiB) {
		klog.Warningf("file share size(%dGi) is already greater or equal than requested size(%dGi), accountName: %s, shareName: %s",
			*shareProps.Quota, sizeGiB, f.accountName, shareName)
		return nil
	}
	if _, err := shareClient.SetProperties(ctx, &share.SetPropertiesOptions{
		Quota: to.Ptr(int32(sizeGiB)),
	}); err != nil {
		return fmt.Errorf("failed to set quota on file share %s, err: %v", shareName, err)
	}
	klog.V(4).Infof("resize file share completed, accountName: %s, shareName: %s, sizeGiB: %d", f.accountName, shareName, sizeGiB)
	return nil
}

func (f *azureFileDataplaneClient) GetFileShareQuota(ctx context.Context, name string) (int, error) {
	shareClient := f.Client.NewShareClient(name)
	shareProps, err := shareClient.GetProperties(ctx, nil)
	if err != nil {
		return -1, err
	}
	return int(*shareProps.Quota), nil
}
