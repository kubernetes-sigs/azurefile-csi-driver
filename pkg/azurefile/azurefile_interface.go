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

	armstorage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
)

const (
	// FileShareAccountNamePrefix is the file share account name prefix
	FileShareAccountNamePrefix = "f"
)

// ShareOptions contains the fields which are used to create file share.
type ShareOptions struct {
	Name       string
	Protocol   armstorage.EnabledProtocols
	RequestGiB int
	// supported values: ""(by default), "TransactionOptimized", "Cool", "Hot", "Premium"
	AccessTier string
	// supported values: ""(by default), "AllSquash", "NoRootSquash", "RootSquash"
	RootSquash string
	// Metadata - A name-value pair to associate with the share as metadata.
	Metadata map[string]*string
	// The provisioned bandwidth of the share, in mebibytes per second. This property is only for file shares created under Files
	// Provisioned v2 account type
	ProvisionedBandwidthMibps *int32
	// The provisioned IOPS of the share. This property is only for file shares created under Files Provisioned v2 account type.
	ProvisionedIops *int32
}

type azureFileClient interface {
	CreateFileShare(ctx context.Context, shareOptions *ShareOptions) error
	DeleteFileShare(ctx context.Context, name string) error
	GetFileShareQuota(ctx context.Context, name string) (int, error)
	ResizeFileShare(ctx context.Context, name string, sizeGiB int) error
}
