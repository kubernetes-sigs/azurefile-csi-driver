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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	azs "github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
	"github.com/Azure/go-autorest/autorest/azure"
	"k8s.io/klog/v2"

	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/retry"
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

func (f *azureFileClient) CreateFileShare(ctx context.Context, accountName, accountKey string, shareOptions *fileclient.ShareOptions) error {
	if shareOptions == nil {
		return fmt.Errorf("shareOptions of account(%s) is nil", accountName)
	}
	return f.createFileShare(ctx, accountName, accountKey, shareOptions.Name, int32(shareOptions.RequestGiB))
}

func (f *azureFileClient) createFileShare(ctx context.Context, accountName, accountKey, name string, sizeGiB int32) error {
	fileClient, err := f.getFileSvcClient(accountName, accountKey, name)
	if err != nil {
		return err
	}
	if _, err := fileClient.Create(ctx, &azs.CreateOptions{
		Quota: to.Ptr(sizeGiB),
	}); err != nil {
		return fmt.Errorf("failed to create file share, err: %v", err)
	}
	return nil
}

// delete a file share
func (f *azureFileClient) deleteFileShare(ctx context.Context, accountName, accountKey, name string) error {
	fileClient, err := f.getFileSvcClient(accountName, accountKey, name)
	if err != nil {
		return err
	}
	_, err = fileClient.Delete(ctx, nil)
	return err
}

func (f *azureFileClient) resizeFileShare(ctx context.Context, accountName, accountKey, name string, sizeGiB int32) error {
	fileClient, err := f.getFileSvcClient(accountName, accountKey, name)
	if err != nil {
		return err
	}
	share, err := fileClient.GetProperties(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get file share %s, err: %v", name, err)
	}
	if share.Quota != nil && *share.Quota >= sizeGiB {
		klog.Warningf("file share size(%dGi) is already greater or equal than requested size(%dGi), accountName: %s, shareName: %s",
			share.Quota, sizeGiB, accountName, name)
		return nil
	}
	if _, err = fileClient.SetProperties(ctx, &azs.SetPropertiesOptions{
		Quota: to.Ptr(sizeGiB),
	}); err != nil {
		return fmt.Errorf("failed to set quota on file share %s, err: %v", name, err)
	}
	klog.V(4).Infof("resize file share completed, accountName: %s, shareName: %s, sizeGiB: %d", accountName, name, sizeGiB)
	return nil
}

func (f *azureFileClient) getFileSvcClient(accountName, accountKey, shareName string) (*azs.Client, error) {
	serviceURL := fmt.Sprintf("https://%s.file.core.windows.net/%s", accountName, shareName)
	cred, err := azs.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, fmt.Errorf("error creating azure shared key credential: %v", err)
	}

	var clientOption *azs.ClientOptions
	if f.backoff != nil {
		clientOption = &azs.ClientOptions{
			ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{
					MaxRetries:  int32(f.backoff.Steps),
					StatusCodes: defaultValidStatusCodes,
					RetryDelay:  f.backoff.Duration,
				},
			},
		}
	}
	fileShareClient, err := azs.NewClientWithSharedKeyCredential(serviceURL, cred, clientOption)
	if err != nil {
		return nil, fmt.Errorf("error creating azure client: %v", err)
	}

	return fileShareClient, nil
}
