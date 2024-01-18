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

	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient"
)

type azureFileClient interface {
	CreateFileShare(ctx context.Context, shareOptions *fileclient.ShareOptions) error
	DeleteFileShare(ctx context.Context, name string) error
	GetFileShareQuota(ctx context.Context, name string) (int, error)
	ResizeFileShare(ctx context.Context, name string, sizeGiB int) error
}
