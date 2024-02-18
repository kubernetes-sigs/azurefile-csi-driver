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
	azs "github.com/Azure/azure-sdk-for-go/storage"
	"k8s.io/klog/v2"

	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/retry"
)

const (
	useHTTPS = true
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
	*azs.FileServiceClient
}

func newAzureFileClient(accountName, accountKey, storageEndpointSuffix string, backoff *retry.Backoff) (azureFileClient, error) {
	if storageEndpointSuffix == "" {
		storageEndpointSuffix = defaultStorageEndPointSuffix
	}

	fileClient, err := azs.NewClient(accountName, accountKey, storageEndpointSuffix, azs.DefaultAPIVersion, useHTTPS)
	if err != nil {
		return nil, fmt.Errorf("error creating azure client: %v", err)
	}

	if backoff != nil {
		fileClient.Sender = &azs.DefaultSender{
			RetryAttempts:    backoff.Steps,
			ValidStatusCodes: defaultValidStatusCodes,
			RetryDuration:    backoff.Duration,
		}
	}

	return &azureFileDataplaneClient{
		accountName:       accountName,
		accountKey:        accountKey,
		FileServiceClient: to.Ptr(fileClient.GetFileService()),
	}, nil
}

func (f *azureFileDataplaneClient) CreateFileShare(_ context.Context, shareOptions *fileclient.ShareOptions) error {
	if shareOptions == nil {
		return fmt.Errorf("shareOptions of account(%s) is nil", f.accountName)
	}
	share := f.FileServiceClient.GetShareReference(shareOptions.Name)
	share.Properties.Quota = shareOptions.RequestGiB
	newlyCreated, err := share.CreateIfNotExists(nil)
	if err != nil {
		return fmt.Errorf("failed to create file share, err: %v", err)
	}
	if !newlyCreated {
		klog.V(2).Infof("file share(%s) under account(%s) already exists", shareOptions.Name, f.accountName)
	}
	return nil
}

// delete a file share
func (f *azureFileDataplaneClient) DeleteFileShare(_ context.Context, shareName string) error {
	return f.FileServiceClient.GetShareReference(shareName).Delete(nil)
}

func (f *azureFileDataplaneClient) ResizeFileShare(_ context.Context, shareName string, sizeGiB int) error {
	share := f.FileServiceClient.GetShareReference(shareName)
	if share.Properties.Quota >= sizeGiB {
		klog.Warningf("file share size(%dGi) is already greater or equal than requested size(%dGi), accountName: %s, shareName: %s",
			share.Properties.Quota, sizeGiB, f.accountName, shareName)
		return nil
	}
	share.Properties.Quota = sizeGiB
	if err := share.SetProperties(nil); err != nil {
		return fmt.Errorf("failed to set quota on file share %s, err: %v", shareName, err)
	}
	klog.V(4).Infof("resize file share completed, accountName: %s, shareName: %s, sizeGiB: %d", f.accountName, shareName, sizeGiB)
	return nil
}

func (f *azureFileDataplaneClient) GetFileShareQuota(_ context.Context, name string) (int, error) {
	share := f.FileServiceClient.GetShareReference(name)
	exists, err := share.Exists()
	if err != nil {
		return -1, err
	}
	if !exists {
		return -1, nil
	}
	return share.Properties.Quota, nil
}
