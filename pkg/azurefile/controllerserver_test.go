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
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2019-12-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2019-06-01/storage"
	azure2 "github.com/Azure/go-autorest/autorest/azure"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/cloud-provider"
	"k8s.io/legacy-cloud-providers/azure"
	"k8s.io/legacy-cloud-providers/azure/clients/fileclient/mockfileclient"
	"k8s.io/legacy-cloud-providers/azure/clients/storageaccountclient/mockstorageaccountclient"
	"k8s.io/legacy-cloud-providers/azure/clients/vmclient/mockvmclient"
	"k8s.io/legacy-cloud-providers/azure/retry"
)

func TestCreateVolume(t *testing.T) {
	stdVolCap := []*csi.VolumeCapability{
		{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}
	stdVolSize := int64(5 * 1024 * 1024 * 1024)
	stdCapRange := &csi.CapacityRange{RequiredBytes: stdVolSize}
	zeroCapRange := &csi.CapacityRange{RequiredBytes: int64(0)}
	lessThanPremCapRange := &csi.CapacityRange{RequiredBytes: int64(1 * 1024 * 1024 * 1024)}

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "Controller Capability missing",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         nil,
				}

				ctx := context.Background()
				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, "CREATE_DELETE_VOLUME")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Volume name missing",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         nil,
				}

				ctx := context.Background()
				d := NewFakeDriver()
				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					})

				expectedErr := status.Error(codes.InvalidArgument, "CreateVolume Name must be provided")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Volume capabilities missing",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:          "random-vol-name",
					CapacityRange: stdCapRange,
					Parameters:    nil,
				}

				ctx := context.Background()
				d := NewFakeDriver()
				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					})

				expectedErr := status.Error(codes.InvalidArgument, "CreateVolume Volume capabilities must be provided")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "No valid key with zero request gib",
			testFunc: func(t *testing.T) {
				name := "baz"
				sku := "sku"
				kind := "StorageV2"
				location := "centralus"
				value := ""
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}, Kind: storage.Kind(kind), Location: &location},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}

				allParam := map[string]string{
					"skuname":            "premium",
					"location":           "loc",
					"storageaccount":     "stoacc",
					"resourcegroup":      "rg",
					shareNameField:       "",
					diskNameField:        "diskname",
					fsTypeField:          "fstype",
					storeAccountKeyField: "storeaccountkey",
					secretNamespaceField: "secretnamespace",
					"defaultparam":       "defaultvalue",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      zeroCapRange,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient

				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					})

				ctx := context.Background()
				expectedErr := fmt.Errorf("no valid keys")

				_, err := d.CreateVolume(ctx, req)
				if !strings.Contains(err.Error(), expectedErr.Error()) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "No valid key, check all params, with less than min premium volume",
			testFunc: func(t *testing.T) {
				name := "baz"
				sku := "sku"
				kind := "StorageV2"
				location := "centralus"
				value := ""
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}, Kind: storage.Kind(kind), Location: &location},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}

				allParam := map[string]string{
					"skuname":            "premium",
					"storageaccounttype": "stoacctype",
					"location":           "loc",
					"storageaccount":     "stoacc",
					"resourcegroup":      "rg",
					shareNameField:       "",
					diskNameField:        "diskname",
					fsTypeField:          "fstype",
					storeAccountKeyField: "storeaccountkey",
					secretNamespaceField: "secretnamespace",
					"defaultparam":       "defaultvalue",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      lessThanPremCapRange,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient

				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					})

				ctx := context.Background()
				expectedErr := fmt.Errorf("no valid keys")

				_, err := d.CreateVolume(ctx, req)
				if !strings.Contains(err.Error(), expectedErr.Error()) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Get file share returns error",
			testFunc: func(t *testing.T) {
				name := "baz"
				sku := "sku"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}, Kind: storage.Kind(kind), Location: &location},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      stdCapRange,
					Parameters:         nil,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient

				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, fmt.Errorf("test error")).AnyTimes()

				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					})

				ctx := context.Background()
				expectedErr := status.Errorf(codes.Internal, "failed to check file share(random-vol-name) if exists: failed to get file share(random-vol-name) under rg() account(baz): test error")

				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Create file share error tests",
			testFunc: func(t *testing.T) {
				name := "baz"
				sku := "sku"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}, Kind: storage.Kind(kind), Location: &location},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}

				allParam := map[string]string{
					"skuname":            "premium",
					"storageaccounttype": "stoacctype",
					"location":           "loc",
					"storageaccount":     "stoacc",
					"resourcegroup":      "rg",
					shareNameField:       "",
					diskNameField:        "diskname",
					fsTypeField:          "fstype",
					storeAccountKeyField: "storeaccountkey",
					secretNamespaceField: "secretnamespace",
					"defaultparam":       "defaultvalue",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      lessThanPremCapRange,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient

				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf(accountNotProvisioned)).Times(1)
				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf(tooManyRequests)).Times(1)
				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf(shareNotFound)).Times(1)
				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf(shareBeingDeleted)).Times(1)
				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("test error")).Times(1)
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()

				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					})

				ctx := context.Background()

				expectedErr := fmt.Errorf("error: failed to create share random-vol-name in account stoacc: test error")
				_, err := d.CreateVolume(ctx, req)
				if !strings.Contains(err.Error(), expectedErr.Error()) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Request namespace does not match",
			testFunc: func(t *testing.T) {
				name := "baz"
				sku := "sku"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}, Kind: storage.Kind(kind), Location: &location},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}

				allParam := map[string]string{
					"skuname":            "premium",
					"storageaccounttype": "stoacctype",
					"location":           "loc",
					"storageaccount":     "stoacc",
					"resourcegroup":      "rg",
					shareNameField:       "",
					diskNameField:        "diskname",
					fsTypeField:          "fstype",
					storeAccountKeyField: "storeaccountkey",
					secretNamespaceField: "secretnamespace",
					"defaultparam":       "defaultvalue",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      lessThanPremCapRange,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{}
				d.cloud.KubeClient = fake.NewSimpleClientset()

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient

				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()

				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					})

				ctx := context.Background()

				expectedErr := status.Error(codes.Internal, "failed to store storage account key: couldn't create secret request namespace does not match object namespace, request: \"secretnamespace\" object: \"default\"")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Create disk returns error",
			testFunc: func(t *testing.T) {
				name := "baz"
				sku := "sku"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}, Kind: storage.Kind(kind), Location: &location},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}

				allParam := map[string]string{
					"skuname":            "premium",
					"storageaccounttype": "stoacctype",
					"location":           "loc",
					"storageaccount":     "stoacc",
					"resourcegroup":      "rg",
					fsTypeField:          "fstype",
					storeAccountKeyField: "storeaccountkey",
					secretNamespaceField: "default",
					"defaultparam":       "defaultvalue",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      lessThanPremCapRange,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{}
				d.cloud.KubeClient = fake.NewSimpleClientset()

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				tests := []struct {
					desc          string
					fileSharename string
					expectedErr   error
				}{
					{
						desc:          "File share name empty",
						fileSharename: "",
						expectedErr:   status.Error(codes.Internal, "failed to create VHD disk: NewSharedKeyCredential(stoacc) failed with error: illegal base64 data at input byte 0"),
					},
					{
						desc:          "File share name provided",
						fileSharename: "filesharename",
						expectedErr:   status.Error(codes.Internal, "failed to create VHD disk: NewSharedKeyCredential(stoacc) failed with error: illegal base64 data at input byte 0"),
					},
				}
				for _, test := range tests {
					allParam[shareNameField] = test.fileSharename
					mockFileClient := mockfileclient.NewMockInterface(ctrl)
					d.cloud.FileClient = mockFileClient

					mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
					d.cloud.StorageAccountClient = mockStorageAccountsClient

					mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
					mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
					mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
					mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
					mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()

					d.AddControllerServiceCapabilities(
						[]csi.ControllerServiceCapability_RPC_Type{
							csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
						})

					ctx := context.Background()

					_, err := d.CreateVolume(ctx, req)
					if !reflect.DeepEqual(err, test.expectedErr) {
						t.Errorf("Unexpected error: %v", err)
					}
				}
			},
		},
		{
			name: "Valid request",
			testFunc: func(t *testing.T) {
				name := "baz"
				sku := "sku"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}, Kind: storage.Kind(kind), Location: &location},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}

				allParam := map[string]string{
					"skuname":            "premium",
					"storageaccounttype": "stoacctype",
					"location":           "loc",
					"storageaccount":     "stoacc",
					"resourcegroup":      "rg",
					shareNameField:       "",
					diskNameField:        "diskname",
					fsTypeField:          "fstype",
					storeAccountKeyField: "storeaccountkey",
					secretNamespaceField: "default",
					"defaultparam":       "defaultvalue",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      lessThanPremCapRange,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{}
				d.cloud.KubeClient = fake.NewSimpleClientset()

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient

				mockFileClient.EXPECT().CreateFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()

				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					})

				ctx := context.Background()

				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestDeleteVolume(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "Volume ID missing",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					Secrets: map[string]string{},
				}

				ctx := context.Background()
				d := NewFakeDriver()

				expectedErr := status.Error(codes.InvalidArgument, "Volume ID missing in request")
				_, err := d.DeleteVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Controller capability missing",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "vol_1",
					Secrets:  map[string]string{},
				}

				ctx := context.Background()
				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, "invalid delete volume request: %v", req)
				_, err := d.DeleteVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid volume ID",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "vol_1",
					Secrets:  map[string]string{},
				}

				ctx := context.Background()
				d := NewFakeDriver()
				d.Cap = []*csi.ControllerServiceCapability{
					{
						Type: &csi.ControllerServiceCapability_Rpc{
							Rpc: &csi.ControllerServiceCapability_RPC{Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME},
						},
					},
				}

				_, err := d.DeleteVolume(ctx, req)
				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Delete file share returns error",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#diskname#",
					Secrets:  map[string]string{},
				}

				ctx := context.Background()
				d := NewFakeDriver()
				d.Cap = []*csi.ControllerServiceCapability{
					{
						Type: &csi.ControllerServiceCapability_Rpc{
							Rpc: &csi.ControllerServiceCapability_RPC{Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME},
						},
					},
				}
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud = &azure.Cloud{}
				d.cloud.FileClient = mockFileClient
				mockFileClient.EXPECT().DeleteFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("test error")).Times(1)

				expectedErr := status.Errorf(codes.Internal, "DeleteFileShare fileshare under account(f5713de20cde511e8ba4900) rg(vol_1) failed with error: test error")
				_, err := d.DeleteVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Valid request",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#diskname#",
					Secrets:  map[string]string{},
				}

				ctx := context.Background()
				d := NewFakeDriver()
				d.Cap = []*csi.ControllerServiceCapability{
					{
						Type: &csi.ControllerServiceCapability_Rpc{
							Rpc: &csi.ControllerServiceCapability_RPC{Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME},
						},
					},
				}
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud = &azure.Cloud{}
				d.cloud.FileClient = mockFileClient
				mockFileClient.EXPECT().DeleteFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

				expectedResp := &csi.DeleteSnapshotResponse{}
				resp, err := d.DeleteVolume(ctx, req)
				if !(reflect.DeepEqual(err, nil) || reflect.DeepEqual(resp, expectedResp)) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestControllerGetCapabilities(t *testing.T) {
	d := NewFakeDriver()
	controlCap := []*csi.ControllerServiceCapability{
		{
			Type: &csi.ControllerServiceCapability_Rpc{},
		},
	}
	d.Cap = controlCap
	req := csi.ControllerGetCapabilitiesRequest{}
	resp, err := d.ControllerGetCapabilities(context.Background(), &req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, resp.Capabilities, controlCap)
}

func TestValidateVolumeCapabilities(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &azure.Cloud{}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &value},
		},
	}
	clientSet := fake.NewSimpleClientset()
	stdVolCap := []*csi.VolumeCapability{
		{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}

	multiNodeVolCap := []*csi.VolumeCapability{
		{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
			},
		},
	}

	tests := []struct {
		desc               string
		req                csi.ValidateVolumeCapabilitiesRequest
		expectedErr        error
		mockedFileShareErr error
	}{
		{
			desc:               "Volume ID missing",
			req:                csi.ValidateVolumeCapabilitiesRequest{},
			expectedErr:        status.Error(codes.InvalidArgument, "Volume ID not provided"),
			mockedFileShareErr: nil,
		},
		{
			desc:               "Volume capabilities missing",
			req:                csi.ValidateVolumeCapabilitiesRequest{VolumeId: "vol_1"},
			expectedErr:        status.Error(codes.InvalidArgument, "Volume capabilities not provided"),
			mockedFileShareErr: nil,
		},
		{
			desc: "Volume ID not valid",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1",
				VolumeCapabilities: stdVolCap,
			},
			expectedErr:        status.Errorf(codes.NotFound, "error getting volume(vol_1) info: error parsing volume id: \"vol_1\", should at least contain two #"),
			mockedFileShareErr: nil,
		},
		{
			desc: "Check file share exists errors",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#",
				VolumeCapabilities: stdVolCap,
			},
			expectedErr:        status.Errorf(codes.NotFound, "error checking if volume(vol_1#f5713de20cde511e8ba4900#fileshare#) exists: failed to get file share(fileshare) under rg(vol_1) account(f5713de20cde511e8ba4900): test error"),
			mockedFileShareErr: fmt.Errorf("test error"),
		},
		{
			desc: "Share not found",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#",
				VolumeCapabilities: stdVolCap,
			},
			expectedErr:        status.Errorf(codes.NotFound, "the requested volume(vol_1#f5713de20cde511e8ba4900#fileshare#) does not exist."),
			mockedFileShareErr: fmt.Errorf("ShareNotFound"),
		},
		{
			desc: "Valid request disk name is empty",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#",
				VolumeCapabilities: stdVolCap,
			},
			expectedErr:        nil,
			mockedFileShareErr: nil,
		},
		{
			desc: "Valid request volume capability is multi node single writer",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#diskname#",
				VolumeCapabilities: multiNodeVolCap,
			},
			expectedErr:        nil,
			mockedFileShareErr: nil,
		},
		{
			desc: "Valid request",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#diskname#",
				VolumeCapabilities: stdVolCap,
			},
			expectedErr:        nil,
			mockedFileShareErr: nil,
		},
		{
			desc: "Resource group empty",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#diskname#",
				VolumeCapabilities: stdVolCap,
				Secrets: map[string]string{
					"accountname":            "accountname",
					"azurestorageaccountkey": "key",
				},
				VolumeContext: map[string]string{
					shareNameField: "sharename",
					diskNameField:  "diskname",
				},
			},
			expectedErr:        nil,
			mockedFileShareErr: nil,
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(key, nil).AnyTimes()
		mockFileClient := mockfileclient.NewMockInterface(ctrl)
		d.cloud.FileClient = mockFileClient
		mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{}, test.mockedFileShareErr).AnyTimes()

		_, err := d.ValidateVolumeCapabilities(context.Background(), &test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("Unexpected error: %v", err)
		}
	}
}

func TestControllerPublishVolume(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	d := NewFakeDriver()
	d.cloud = azure.GetTestCloud(ctrl)
	d.cloud.Location = "centralus"
	d.cloud.ResourceGroup = "rg"
	nodeName := "vm1"
	instanceID := fmt.Sprintf("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/%s", nodeName)
	vm := compute.VirtualMachine{
		Name:     &nodeName,
		ID:       &instanceID,
		Location: &d.cloud.Location,
	}
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &value},
		},
	}
	clientSet := fake.NewSimpleClientset()

	stdVolCap := csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
	multiWriterVolCap := csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		},
	}
	readOnlyVolCap := csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		},
	}

	tests := []struct {
		desc        string
		req         *csi.ControllerPublishVolumeRequest
		expectedErr error
	}{
		{
			desc:        "Volume ID missing",
			req:         &csi.ControllerPublishVolumeRequest{},
			expectedErr: status.Error(codes.InvalidArgument, "Volume ID not provided"),
		},
		{
			desc: "Volume capability missing",
			req: &csi.ControllerPublishVolumeRequest{
				VolumeId: "vol_1",
			},
			expectedErr: status.Error(codes.InvalidArgument, "Volume capability not provided"),
		},
		{
			desc: "Node ID missing",
			req: &csi.ControllerPublishVolumeRequest{
				VolumeId:         "vol_1",
				VolumeCapability: &stdVolCap,
			},
			expectedErr: status.Error(codes.InvalidArgument, "Node ID not provided"),
		},
		{
			desc: "Instance not found",
			req: &csi.ControllerPublishVolumeRequest{
				VolumeId:         "vol_1",
				VolumeCapability: &stdVolCap,
				NodeId:           "vm1",
			},
			expectedErr: status.Error(codes.NotFound, "failed to get azure instance id for node \"vm1\" (instance not found)"),
		},
		{
			desc: "Get azure instance returns error",
			req: &csi.ControllerPublishVolumeRequest{
				VolumeId:         "vol_1",
				VolumeCapability: &stdVolCap,
				NodeId:           "vm2",
			},
			expectedErr: status.Error(codes.Internal, "get azure instance id for node \"vm2\" failed with Retriable: false, RetryAfter: 0s, HTTPStatusCode: 502, RawError: instance not found"),
		},
		{
			desc: "Valid request disk name empty",
			req: &csi.ControllerPublishVolumeRequest{
				VolumeId:         "vol_1",
				VolumeCapability: &stdVolCap,
				NodeId:           "vm3",
			},
			expectedErr: nil,
		},
		{
			desc: "Get account info returns error",
			req: &csi.ControllerPublishVolumeRequest{
				VolumeId:         "vol_2#f5713de20cde511e8ba4900#fileshare#diskname",
				VolumeCapability: &stdVolCap,
				NodeId:           "vm3",
			},
			expectedErr: status.Error(codes.InvalidArgument, "GetAccountInfo(vol_2#f5713de20cde511e8ba4900#fileshare#diskname) failed with error: Retriable: false, RetryAfter: 0s, HTTPStatusCode: 502, RawError: instance not found"),
		},
		{
			desc: "Unsupported access mode",
			req: &csi.ControllerPublishVolumeRequest{
				VolumeId:         "vol_1#f5713de20cde511e8ba4900#fileshare#diskname",
				VolumeCapability: &multiWriterVolCap,
				NodeId:           "vm3",
			},
			expectedErr: status.Error(codes.InvalidArgument, "unsupported AccessMode(mode:MULTI_NODE_MULTI_WRITER ) for volume(vol_1#f5713de20cde511e8ba4900#fileshare#diskname)"),
		},
		{
			desc: "Read only access mode",
			req: &csi.ControllerPublishVolumeRequest{
				VolumeId:         "vol_1#f5713de20cde511e8ba4900#fileshare#diskname",
				VolumeCapability: &readOnlyVolCap,
				NodeId:           "vm3",
			},
			expectedErr: nil,
		},
	}

	for _, test := range tests {
		d.cloud.VirtualMachinesClient = mockvmclient.NewMockInterface(ctrl)
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		mockVMsClient := d.cloud.VirtualMachinesClient.(*mockvmclient.MockInterface)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_2", gomock.Any()).Return(key, &retry.Error{HTTPStatusCode: http.StatusBadGateway, RawError: cloudprovider.InstanceNotFound}).AnyTimes()
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()
		mockVMsClient.EXPECT().Get(gomock.Any(), d.cloud.ResourceGroup, "vm1", gomock.Any()).Return(compute.VirtualMachine{}, &retry.Error{HTTPStatusCode: http.StatusNotFound, RawError: cloudprovider.InstanceNotFound}).AnyTimes()
		mockVMsClient.EXPECT().Get(gomock.Any(), d.cloud.ResourceGroup, "vm2", gomock.Any()).Return(compute.VirtualMachine{}, &retry.Error{HTTPStatusCode: http.StatusBadGateway, RawError: cloudprovider.InstanceNotFound}).AnyTimes()
		mockVMsClient.EXPECT().Get(gomock.Any(), d.cloud.ResourceGroup, "vm3", gomock.Any()).Return(vm, nil).AnyTimes()
		mockVMsClient.EXPECT().Update(gomock.Any(), d.cloud.ResourceGroup, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		_, err := d.ControllerPublishVolume(context.Background(), test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("Unexpected error: %v", err)
		}
	}
}

func TestControllerUnpublishVolume(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &azure.Cloud{}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &value},
		},
	}
	clientSet := fake.NewSimpleClientset()

	tests := []struct {
		desc        string
		req         *csi.ControllerUnpublishVolumeRequest
		expectedErr error
	}{
		{
			desc:        "Volume ID missing",
			req:         &csi.ControllerUnpublishVolumeRequest{},
			expectedErr: status.Error(codes.InvalidArgument, "Volume ID not provided"),
		},
		{
			desc: "Node ID missing",
			req: &csi.ControllerUnpublishVolumeRequest{
				VolumeId: "vol_1",
			},
			expectedErr: status.Error(codes.InvalidArgument, "Node ID not provided"),
		},
		{
			desc: "Disk name empty",
			req: &csi.ControllerUnpublishVolumeRequest{
				VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#",
				NodeId:   fakeNodeID,
				Secrets:  map[string]string{},
			},
			expectedErr: nil,
		},
		{
			desc: "Get account info returns error",
			req: &csi.ControllerUnpublishVolumeRequest{
				VolumeId: "vol_2#f5713de20cde511e8ba4900#fileshare#diskname#",
				NodeId:   fakeNodeID,
				Secrets:  map[string]string{},
			},
			expectedErr: status.Error(codes.InvalidArgument, "GetAccountInfo(vol_2#f5713de20cde511e8ba4900#fileshare#diskname#) failed with error: Retriable: false, RetryAfter: 0s, HTTPStatusCode: 502, RawError: instance not found"),
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_2", gomock.Any()).Return(key, &retry.Error{HTTPStatusCode: http.StatusBadGateway, RawError: cloudprovider.InstanceNotFound}).AnyTimes()
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()

		_, err := d.ControllerUnpublishVolume(context.Background(), test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("Unexpected error: %v", err)
		}
	}
}

func TestCreateSnapshot(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &azure.Cloud{}

	tests := []struct {
		desc        string
		req         *csi.CreateSnapshotRequest
		expectedErr error
	}{
		{
			desc:        "Snapshot name missing",
			req:         &csi.CreateSnapshotRequest{},
			expectedErr: status.Error(codes.InvalidArgument, "Snapshot name must be provided"),
		},
		{
			desc: "Source volume ID",
			req: &csi.CreateSnapshotRequest{
				Name: "snapname",
			},
			expectedErr: status.Error(codes.InvalidArgument, "CreateSnapshot Source Volume ID must be provided"),
		},
		{
			desc: "Invalid volume ID",
			req: &csi.CreateSnapshotRequest{
				SourceVolumeId: "vol_1",
				Name:           "snapname",
			},
			expectedErr: status.Errorf(codes.Internal, "failed to check if snapshot(snapname) exists: error parsing volume id: \"vol_1\", should at least contain two #"),
		},
	}

	for _, test := range tests {
		_, err := d.CreateSnapshot(context.Background(), test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("Unexpected error: %v", err)
		}
	}
}

func TestDeleteSnapshot(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &azure.Cloud{}

	validSecret := map[string]string{}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &value},
		},
	}
	clientSet := fake.NewSimpleClientset()

	tests := []struct {
		desc        string
		req         *csi.DeleteSnapshotRequest
		expectedErr error
	}{
		{
			desc:        "Snapshot name missing",
			req:         &csi.DeleteSnapshotRequest{},
			expectedErr: status.Error(codes.InvalidArgument, "Snapshot ID must be provided"),
		},
		{
			desc: "Invalid volume ID",
			req: &csi.DeleteSnapshotRequest{
				SnapshotId: "vol_1#",
			},
			expectedErr: nil,
		},
		{
			desc: "Invalid volume ID for snapshot name",
			req: &csi.DeleteSnapshotRequest{
				SnapshotId: "vol_1##",
				Secrets:    validSecret,
			},
			expectedErr: status.Errorf(codes.Internal, "failed to get snapshot name with (vol_1##): error parsing volume id: \"vol_1##\", should at least contain four #"),
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()

		_, err := d.DeleteSnapshot(context.Background(), test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("Unexpected error: %v", err)
		}
	}
}

func TestControllerExpandVolume(t *testing.T) {
	stdVolSize := int64(5 * 1024 * 1024 * 1024)
	stdCapRange := &csi.CapacityRange{RequiredBytes: stdVolSize}

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "Volume ID missing",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerExpandVolumeRequest{}

				ctx := context.Background()
				d := NewFakeDriver()

				expectedErr := status.Error(codes.InvalidArgument, "Volume ID missing in request")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Volume Capacity range missing",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerExpandVolumeRequest{
					VolumeId: "vol_1",
				}

				ctx := context.Background()
				d := NewFakeDriver()

				expectedErr := status.Error(codes.InvalidArgument, "volume capacity range missing in request")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Volume capabilities missing",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerExpandVolumeRequest{
					VolumeId:      "vol_1",
					CapacityRange: stdCapRange,
				}

				ctx := context.Background()
				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, "invalid expand volume request: volume_id:\"vol_1\" capacity_range:<required_bytes:5368709120 > ")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid Volume ID",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerExpandVolumeRequest{
					VolumeId:      "vol_1",
					CapacityRange: stdCapRange,
				}

				ctx := context.Background()
				d := NewFakeDriver()
				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
					})

				expectedErr := status.Errorf(codes.InvalidArgument, "GetAccountInfo(vol_1) failed with error: error parsing volume id: \"vol_1\", should at least contain two #")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Disk name not empty",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
					})
				d.cloud = &azure.Cloud{}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
				key := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}
				clientSet := fake.NewSimpleClientset()
				req := &csi.ControllerExpandVolumeRequest{
					VolumeId:      "vol_1#f5713de20cde511e8ba4900#filename#diskname#",
					CapacityRange: stdCapRange,
				}

				ctx := context.Background()
				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				d.cloud.KubeClient = clientSet
				d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()

				expectErr := status.Error(codes.Unimplemented, "vhd disk volume(vol_1#f5713de20cde511e8ba4900#filename#diskname#) is not supported on ControllerExpandVolume")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Resize file share returns error",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
					})
				d.cloud = &azure.Cloud{}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
				key := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}
				clientSet := fake.NewSimpleClientset()
				req := &csi.ControllerExpandVolumeRequest{
					VolumeId:      "vol_1#f5713de20cde511e8ba4900#filename#",
					CapacityRange: stdCapRange,
				}

				ctx := context.Background()
				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				d.cloud.KubeClient = clientSet
				d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				mockFileClient.EXPECT().ResizeFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("test error")).AnyTimes()
				d.cloud.FileClient = mockFileClient

				expectErr := status.Errorf(codes.Internal, "expand volume error: test error")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Get file share returns error",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
					})
				d.cloud = &azure.Cloud{}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
				key := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}
				clientSet := fake.NewSimpleClientset()
				req := &csi.ControllerExpandVolumeRequest{
					VolumeId:      "vol_1#f5713de20cde511e8ba4900#filename#",
					CapacityRange: stdCapRange,
				}

				ctx := context.Background()
				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				d.cloud.KubeClient = clientSet
				d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				mockFileClient.EXPECT().ResizeFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{}, fmt.Errorf("test error")).AnyTimes()
				d.cloud.FileClient = mockFileClient

				expectErr := status.Errorf(codes.Internal, "could not get file share(filename): test error")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "File share share quota is nil",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
					})
				d.cloud = &azure.Cloud{}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
				key := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}
				clientSet := fake.NewSimpleClientset()
				req := &csi.ControllerExpandVolumeRequest{
					VolumeId:      "vol_1#f5713de20cde511e8ba4900#filename#",
					CapacityRange: stdCapRange,
				}

				ctx := context.Background()
				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				d.cloud.KubeClient = clientSet
				d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				mockFileClient.EXPECT().ResizeFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				d.cloud.FileClient = mockFileClient

				expectErr := status.Errorf(codes.Internal, "the pointer of file share(filename) quota is nil")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Valid request",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.AddControllerServiceCapabilities(
					[]csi.ControllerServiceCapability_RPC_Type{
						csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
					})
				d.cloud = &azure.Cloud{}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
				key := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}
				clientSet := fake.NewSimpleClientset()
				req := &csi.ControllerExpandVolumeRequest{
					VolumeId:      "vol_1#f5713de20cde511e8ba4900#filename#",
					CapacityRange: stdCapRange,
				}

				ctx := context.Background()
				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				d.cloud.KubeClient = clientSet
				d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				mockFileClient.EXPECT().ResizeFileShare(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				shareQuota := int32(0)
				mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &shareQuota}}, nil).AnyTimes()
				d.cloud.FileClient = mockFileClient

				expectedResp := &csi.ControllerExpandVolumeResponse{CapacityBytes: int64(0)}
				resp, err := d.ControllerExpandVolume(ctx, req)
				if !(reflect.DeepEqual(err, nil) || reflect.DeepEqual(resp, expectedResp)) {
					t.Errorf("Expected response: %v received response: %v expected error: %v received error: %v", expectedResp, resp, nil, err)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestGetShareURL(t *testing.T) {
	d := NewFakeDriver()
	validSecret := map[string]string{}
	d.cloud = &azure.Cloud{}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &value},
		},
	}
	clientSet := fake.NewSimpleClientset()
	tests := []struct {
		desc           string
		sourceVolumeID string
		expectedErr    error
	}{
		{
			desc:           "Volume ID error",
			sourceVolumeID: "vol_1",
			expectedErr:    fmt.Errorf("error parsing volume id: \"vol_1\", should at least contain two #"),
		},
		{
			desc:           "Valid request",
			sourceVolumeID: "vol_1##",
			expectedErr:    nil,
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()

		_, err := d.getShareURL(test.sourceVolumeID, validSecret)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("Unexpected error: %v", err)
		}
	}
}

func TestGetServiceURL(t *testing.T) {
	d := NewFakeDriver()
	validSecret := map[string]string{}
	d.cloud = &azure.Cloud{}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	errValue := "acc_key"
	validKey := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &value},
		},
	}
	errKey := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &errValue},
		},
	}
	clientSet := fake.NewSimpleClientset()
	tests := []struct {
		desc           string
		sourceVolumeID string
		key            storage.AccountListKeysResult
		expectedErr    error
	}{
		{
			desc:           "Invalid volume ID",
			sourceVolumeID: "vol_1",
			key:            validKey,
			expectedErr:    fmt.Errorf("error parsing volume id: \"vol_1\", should at least contain two #"),
		},
		{
			desc:           "Invalid Key",
			sourceVolumeID: "vol_1##",
			key:            errKey,
			expectedErr:    base64.CorruptInputError(3),
		},
		{
			desc:           "Invalid URL",
			sourceVolumeID: "vol_1#^f5713de20cde511e8ba4900#",
			key:            validKey,
			expectedErr:    &url.Error{Op: "parse", URL: "https://^f5713de20cde511e8ba4900.file.abc", Err: url.InvalidHostError("^")},
		},
		{
			desc:           "Valid call",
			sourceVolumeID: "vol_1##",
			key:            validKey,
			expectedErr:    nil,
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(test.key, nil).AnyTimes()

		_, _, err := d.getServiceURL(test.sourceVolumeID, validSecret)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("Unexpected error: %v", err)
		}
	}
}

func TestSnapshotExists(t *testing.T) {
	d := NewFakeDriver()
	validSecret := map[string]string{}
	d.cloud = &azure.Cloud{}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))

	validKey := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &value},
		},
	}

	clientSet := fake.NewSimpleClientset()
	tests := []struct {
		desc           string
		sourceVolumeID string
		key            storage.AccountListKeysResult
		expectedErr    error
	}{
		{
			desc:           "Invalid volume ID",
			sourceVolumeID: "vol_1",
			key:            validKey,
			expectedErr:    fmt.Errorf("error parsing volume id: \"vol_1\", should at least contain two #"),
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "vol_1", gomock.Any()).Return(test.key, nil).AnyTimes()

		_, _, err := d.snapshotExists(context.Background(), test.sourceVolumeID, "sname", validSecret)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("Unexpected error: %v", err)
		}
	}
}
