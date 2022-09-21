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
	"fmt"
	"net/http"

	azs "github.com/Azure/azure-sdk-for-go/storage"
	"github.com/Azure/go-autorest/autorest/azure"
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

type azureFileClient struct {
	env     *azure.Environment
	backoff *retry.Backoff
	// if storageEndpointSuffix is empty, will default to cloud.env.StorageEndpointSuffix
	StorageEndpointSuffix string
}

func newAzureFileClient(env *azure.Environment, backoff *retry.Backoff) *azureFileClient {
	return &azureFileClient{
		env:     env,
		backoff: backoff,
	}
}

func (f *azureFileClient) CreateFileShare(accountName, accountKey string, shareOptions *fileclient.ShareOptions) error {
	if shareOptions == nil {
		return fmt.Errorf("shareOptions of account(%s) is nil", accountName)
	}
	return f.createFileShare(accountName, accountKey, shareOptions.Name, shareOptions.RequestGiB)
}

func (f *azureFileClient) createFileShare(accountName, accountKey, name string, sizeGiB int) error {
	fileClient, err := f.getFileSvcClient(accountName, accountKey)
	if err != nil {
		return err
	}
	share := fileClient.GetShareReference(name)
	share.Properties.Quota = sizeGiB
	newlyCreated, err := share.CreateIfNotExists(nil)
	if err != nil {
		return fmt.Errorf("failed to create file share, err: %v", err)
	}
	if !newlyCreated {
		klog.V(2).Infof("file share(%s) under account(%s) already exists", name, accountName)
	}
	return nil
}

// delete a file share
func (f *azureFileClient) deleteFileShare(accountName, accountKey, name string) error {
	fileClient, err := f.getFileSvcClient(accountName, accountKey)
	if err != nil {
		return err
	}
	return fileClient.GetShareReference(name).Delete(nil)
}

func (f *azureFileClient) resizeFileShare(accountName, accountKey, name string, sizeGiB int) error {
	fileClient, err := f.getFileSvcClient(accountName, accountKey)
	if err != nil {
		return err
	}
	share := fileClient.GetShareReference(name)
	if share.Properties.Quota >= sizeGiB {
		klog.Warningf("file share size(%dGi) is already greater or equal than requested size(%dGi), accountName: %s, shareName: %s",
			share.Properties.Quota, sizeGiB, accountName, name)
		return nil
	}
	share.Properties.Quota = sizeGiB
	if err = share.SetProperties(nil); err != nil {
		return fmt.Errorf("failed to set quota on file share %s, err: %v", name, err)
	}
	klog.V(4).Infof("resize file share completed, accountName: %s, shareName: %s, sizeGiB: %d", accountName, name, sizeGiB)
	return nil
}

func (f *azureFileClient) getFileSvcClient(accountName, accountKey string) (*azs.FileServiceClient, error) {
	storageEndpointSuffix := f.env.StorageEndpointSuffix
	if f.StorageEndpointSuffix != "" {
		storageEndpointSuffix = f.StorageEndpointSuffix
	}
	if storageEndpointSuffix == "" {
		storageEndpointSuffix = defaultStorageEndPointSuffix
	}

	fileClient, err := azs.NewClient(accountName, accountKey, storageEndpointSuffix, azs.DefaultAPIVersion, useHTTPS)
	if err != nil {
		return nil, fmt.Errorf("error creating azure client: %v", err)
	}

	if f.backoff != nil {
		fileClient.Sender = &azs.DefaultSender{
			RetryAttempts:    f.backoff.Steps,
			ValidStatusCodes: defaultValidStatusCodes,
			RetryDuration:    f.backoff.Duration,
		}
	}

	fc := fileClient.GetFileService()
	return &fc, nil
}
