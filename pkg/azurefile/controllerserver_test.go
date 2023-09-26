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
	"sync"
	"testing"
	"time"

	"sigs.k8s.io/azurefile-csi-driver/pkg/util"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/config"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2022-07-01/network"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/subnetclient/mocksubnetclient"
	azcache "sigs.k8s.io/cloud-provider-azure/pkg/cache"

	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2021-09-01/storage"
	azure2 "github.com/Azure/go-autorest/autorest/azure"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/utils/pointer"

	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient/mockfileclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/storageaccountclient/mockstorageaccountclient"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
	"sigs.k8s.io/cloud-provider-azure/pkg/retry"
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
	fakeShareQuota := int32(100)
	stdVolSize := int64(5 * 1024 * 1024 * 1024)
	stdCapRange := &csi.CapacityRange{RequiredBytes: stdVolSize}
	zeroCapRange := &csi.CapacityRange{RequiredBytes: int64(0)}
	lessThanPremCapRange := &csi.CapacityRange{RequiredBytes: int64(fakeShareQuota * 1024 * 1024 * 1024)}
	ctx := context.TODO()

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "Controller Capability missing",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-cap-missing",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         nil,
				}

				d := NewFakeDriver()
				d.Cap = []*csi.ControllerServiceCapability{}

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
				d := NewFakeDriver()

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
					Name:          "random-vol-name-vol-cap-missing",
					CapacityRange: stdCapRange,
					Parameters:    nil,
				}

				d := NewFakeDriver()

				expectedErr := status.Error(codes.InvalidArgument, "CreateVolume Volume capabilities not valid: CreateVolume Volume capabilities must be provided")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid volume capabilities",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:          "random-vol-name-vol-cap-invalid",
					CapacityRange: stdCapRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Block{
								Block: &csi.VolumeCapability_BlockVolume{},
							},
							AccessMode: &csi.VolumeCapability_AccessMode{
								Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
							},
						},
					},
					Parameters: nil,
				}

				d := NewFakeDriver()

				expectedErr := status.Error(codes.InvalidArgument, "CreateVolume Volume capabilities not valid: driver does not support block volumes")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Volume lock already present",
			testFunc: func(t *testing.T) {
				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         nil,
				}

				d := NewFakeDriver()
				locks := newVolumeLocks()
				locks.locks.Insert(req.GetName())
				d.volumeLocks = locks

				expectedErr := status.Error(codes.Aborted, "An operation with the given Volume ID random-vol-name-vol-cap-invalid already exists")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Disabled fsType",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					fsTypeField:     "test_fs",
					secretNameField: "secretname",
					pvcNamespaceKey: "pvcname",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				driverOptions := DriverOptions{
					NodeID:               fakeNodeID,
					DriverName:           DefaultDriverName,
					EnableVHDDiskFeature: false,
				}
				d := NewFakeDriverCustomOptions(driverOptions)

				expectedErr := status.Errorf(codes.InvalidArgument, "fsType storage class parameter enables experimental VDH disk feature which is currently disabled, use --enable-vhd driver option to enable it")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid fsType",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					fsTypeField: "test_fs",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				driverOptions := DriverOptions{
					NodeID:               fakeNodeID,
					DriverName:           DefaultDriverName,
					EnableVHDDiskFeature: true,
				}
				d := NewFakeDriverCustomOptions(driverOptions)

				expectedErr := status.Errorf(codes.InvalidArgument, "fsType(test_fs) is not supported, supported fsType list: [cifs smb nfs ext4 ext3 ext2 xfs]")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid protocol",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					protocolField: "test_protocol",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, "protocol(test_protocol) is not supported, supported protocol list: [smb nfs]")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "nfs protocol only supports premium storage",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					protocolField: "nfs",
					skuNameField:  "Standard_LRS",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-nfs-protocol-standard-sku",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				expectedErr := status.Errorf(codes.InvalidArgument, "nfs protocol only supports premium storage, current account type: Standard_LRS")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid accessTier",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					protocolField:   "smb",
					accessTierField: "test_accessTier",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, "shareAccessTier(test_accessTier) is not supported, supported ShareAccessTier list: [Cool Hot Premium TransactionOptimized]")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid rootSquashType",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					rootSquashTypeField: "test_rootSquashType",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, "rootSquashType(test_rootSquashType) is not supported, supported RootSquashType list: [AllSquash NoRootSquash RootSquash]")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid fsGroupChangePolicy",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					fsGroupChangePolicyField: "test_fsGroupChangePolicy",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, "fsGroupChangePolicy(test_fsGroupChangePolicy) is not supported, supported fsGroupChangePolicy list: [None Always OnRootMismatch]")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid shareNamePrefix",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					shareNamePrefixField: "-invalid",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}
				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, "shareNamePrefix(-invalid) can only contain lowercase letters, numbers, hyphens, and length should be less than 21")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid accountQuota",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					accountQuotaField: "10",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, fmt.Sprintf("invalid accountQuota %d in storage class, minimum quota: %d", 10, minimumAccountQuota))
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "invalid tags format to convert to map",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					skuNameField:               "premium",
					resourceGroupField:         "rg",
					tagsField:                  "tags",
					createAccountField:         "true",
					useSecretCacheField:        "true",
					enableLargeFileSharesField: "true",
					pvcNameKey:                 "pvc",
					pvNameKey:                  "pv",
					shareNamePrefixField:       "pre",
					storageEndpointSuffixField: ".core",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, fmt.Errorf("Tags 'tags' are invalid, the format should like: 'key1=value1,key2=value2'").Error())
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "failed to GetStorageAccesskey",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					protocolField:            "nfs",
					networkEndpointTypeField: "privateendpoint",
					useDataPlaneAPIField:     "true",
					vnetResourceGroupField:   "",
					vnetNameField:            "",
					subnetNameField:          "",
				}
				fakeCloud := &azure.Cloud{
					Config: azure.Config{},
					Environment: azure2.Environment{
						StorageEndpointSuffix: "core.windows.net",
					},
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = fakeCloud
				d.volMap = sync.Map{}
				d.volMap.Store("random-vol-name-vol-cap-invalid", "account")

				expectedErr := status.Errorf(codes.Internal, "failed to GetStorageAccesskey on account(account) rg(), error: StorageAccountClient is nil")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Invalid protocol & fsType combination",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					protocolField: "nfs",
					fsTypeField:   "ext4",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				driverOptions := DriverOptions{
					NodeID:               fakeNodeID,
					DriverName:           DefaultDriverName,
					EnableVHDDiskFeature: true,
				}
				d := NewFakeDriverCustomOptions(driverOptions)

				expectedErr := status.Errorf(codes.InvalidArgument, "fsType(ext4) is not supported with protocol(nfs)")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "storeAccountKey must set as true in cross subscription",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					subscriptionIDField:              "abc",
					storeAccountKeyField:             "false",
					selectRandomMatchingAccountField: "true",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{},
				}

				expectedErr := status.Errorf(codes.InvalidArgument, "resourceGroup must be provided in cross subscription(abc)")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "invalid selectRandomMatchingAccount value",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					selectRandomMatchingAccountField: "invalid",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-selectRandomMatchingAccount-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{},
				}

				expectedErr := status.Errorf(codes.InvalidArgument, "invalid selectrandommatchingaccount: invalid in storage class")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "invalid getLatestAccountKey value",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					getLatestAccountKeyField: "invalid",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-getLatestAccountKey-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{},
				}

				expectedErr := status.Errorf(codes.InvalidArgument, "invalid getlatestaccountkey: invalid in storage class")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v, expected error: %v", err, expectedErr)
				}
			},
		},
		{
			name: "storageAccount and matchTags conflict",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					storageAccountField: "abc",
					matchTagsField:      "true",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{},
				}

				expectedErr := status.Errorf(codes.InvalidArgument, "matchTags must set as false when storageAccount(abc) is provided")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "invalid privateEndpoint and subnetName combination",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					networkEndpointTypeField: "privateendpoint",
					subnetNameField:          "subnet1,subnet2",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "invalid-privateEndpoint-and-subnetName-combination",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{},
				}

				expectedErr := status.Errorf(codes.InvalidArgument, "subnetName(subnet1,subnet2) can only contain one subnet for private endpoint")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v, expected error: %v", err, expectedErr)
				}
			},
		},
		{
			name: "Failed to update subnet service endpoints",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{
					protocolField: "nfs",
				}

				fakeCloud := &azure.Cloud{
					Config: azure.Config{
						ResourceGroup: "rg",
						Location:      "loc",
						VnetName:      "fake-vnet",
						SubnetName:    "fake-subnet",
					},
				}
				retErr := retry.NewError(false, fmt.Errorf("the subnet does not exist"))

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-vol-cap-invalid",
					CapacityRange:      stdCapRange,
					VolumeCapabilities: stdVolCap,
					Parameters:         allParam,
				}
				d := NewFakeDriver()

				d.cloud = fakeCloud
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockSubnetClient := mocksubnetclient.NewMockInterface(ctrl)
				fakeCloud.SubnetsClient = mockSubnetClient

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(network.Subnet{}, retErr).Times(1)

				expectedErr := status.Errorf(codes.Internal, "update service endpoints failed with error: failed to get the subnet fake-subnet under vnet fake-vnet: &{false 0 0001-01-01 00:00:00 +0000 UTC the subnet does not exist}")
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
				account := storage.Account{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}, Kind: storage.Kind(kind), Location: &location}
				accounts := []storage.Account{account}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}

				allParam := map[string]string{
					skuNameField:                      "premium",
					locationField:                     "loc",
					storageAccountField:               "",
					resourceGroupField:                "rg",
					shareNameField:                    "",
					diskNameField:                     "diskname.vhd",
					fsTypeField:                       "",
					storeAccountKeyField:              "storeaccountkey",
					secretNamespaceField:              "secretnamespace",
					disableDeleteRetentionPolicyField: "true",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-no-valid-key",
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

				fileServiceProperties := storage.FileServiceProperties{
					FileServicePropertiesProperties: &storage.FileServicePropertiesProperties{},
				}

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetServiceProperties(context.TODO(), gomock.Any(), gomock.Any()).Return(fileServiceProperties, nil).AnyTimes()
				mockFileClient.EXPECT().SetServiceProperties(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fileServiceProperties, nil).AnyTimes()

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
					skuNameField:         "premium",
					locationField:        "loc",
					storageAccountField:  "",
					resourceGroupField:   "rg",
					shareNameField:       "",
					diskNameField:        "diskname.vhd",
					fsTypeField:          "",
					storeAccountKeyField: "storeaccountkey",
					secretNamespaceField: "secretnamespace",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-no-valid-key-check-all-params",
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

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

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
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}, Kind: storage.Kind(kind), Location: &location, AccountProperties: &storage.AccountProperties{}},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-get-file-error",
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

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, fmt.Errorf("test error")).AnyTimes()
				mockFileClient.EXPECT().ListFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]storage.FileShareItem{}, nil).AnyTimes()

				expectedErr := status.Errorf(codes.Internal, "test error")

				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("test name: %s, Unexpected error: %v, expected error: %v", name, err, expectedErr)
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
					storageAccountTypeField:           "premium",
					locationField:                     "loc",
					storageAccountField:               "stoacc",
					resourceGroupField:                "rg",
					shareNameField:                    "",
					diskNameField:                     "diskname.vhd",
					fsTypeField:                       "",
					storeAccountKeyField:              "storeaccountkey",
					secretNamespaceField:              "secretnamespace",
					disableDeleteRetentionPolicyField: "true",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-crete-file-error",
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
				d.cloud.CloudProviderBackoff = true
				d.cloud.ResourceRequestBackoff = wait.Backoff{
					Steps: 6,
				}

				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()

				expectedErr := status.Errorf(codes.Internal, "FileShareProperties or FileShareProperties.ShareQuota is nil")

				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("test name: %s, Unexpected error: %v, expected error: %v", name, err, expectedErr)
				}
			},
		},
		{
			name: "existing file share quota is smaller than request quota",
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
					storageAccountTypeField:           "premium",
					locationField:                     "loc",
					storageAccountField:               "stoacc",
					resourceGroupField:                "rg",
					shareNameField:                    "",
					diskNameField:                     "diskname.vhd",
					fsTypeField:                       "",
					storeAccountKeyField:              "storeaccountkey",
					secretNamespaceField:              "secretnamespace",
					disableDeleteRetentionPolicyField: "true",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-crete-file-error",
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
				d.cloud.CloudProviderBackoff = true
				d.cloud.ResourceRequestBackoff = wait.Backoff{
					Steps: 6,
				}

				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: pointer.Int32(1)}}, nil).AnyTimes()

				expectedErr := status.Errorf(codes.AlreadyExists, "request file share(random-vol-name-crete-file-error) already exists, but its capacity 1 is smaller than 100")
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("test name: %s, Unexpected error: %v, expected error: %v", name, err, expectedErr)
				}
			},
		},
		{
			name: "Create disk returns error",
			testFunc: func(t *testing.T) {
				skipIfTestingOnWindows(t)
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
					skuNameField:            "premium",
					storageAccountTypeField: "stoacctype",
					locationField:           "loc",
					storageAccountField:     "stoacc",
					resourceGroupField:      "rg",
					fsTypeField:             "ext4",
					storeAccountKeyField:    "storeaccountkey",
					secretNamespaceField:    "default",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-create-disk-error",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      lessThanPremCapRange,
					Parameters:         allParam,
				}

				driverOptions := DriverOptions{
					NodeID:               fakeNodeID,
					DriverName:           DefaultDriverName,
					EnableVHDDiskFeature: true,
				}
				d := NewFakeDriverCustomOptions(driverOptions)

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

					mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
					mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
					mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
					mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
					mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
					mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()

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
					skuNameField:            "premium",
					storageAccountTypeField: "stoacctype",
					locationField:           "loc",
					storageAccountField:     "stoacc",
					resourceGroupField:      "rg",
					shareNameField:          "",
					diskNameField:           "diskname.vhd",
					fsTypeField:             "",
					storeAccountKeyField:    "storeaccountkey",
					secretNamespaceField:    "default",
					mountPermissionsField:   "0755",
					accountQuotaField:       "1000",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-valid-request",
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

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()

				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "invalid mountPermissions",
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
					mountPermissionsField: "0abc",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-valid-request",
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

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()

				expectedErr := status.Errorf(codes.InvalidArgument, fmt.Sprintf("invalid %s %s in storage class", "mountPermissions", "0abc"))
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "invalid parameter",
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
					"invalidparameter": "invalidparameter",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-valid-request",
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

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()

				expectedErr := status.Errorf(codes.InvalidArgument, fmt.Sprintf("invalid parameter %q in storage class", "invalidparameter"))
				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Account limit exceeded",
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
					skuNameField:            "premium",
					storageAccountTypeField: "stoacctype",
					locationField:           "loc",
					storageAccountField:     "stoacc",
					resourceGroupField:      "rg",
					shareNameField:          "",
					diskNameField:           "diskname.vhd",
					fsTypeField:             "",
					storeAccountKeyField:    "storeaccountkey",
					secretNamespaceField:    "default",
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-valid-request",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      lessThanPremCapRange,
					Parameters:         allParam,
				}

				d := NewFakeDriver()
				d.cloud.KubeClient = fake.NewSimpleClientset()
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				d.cloud = azure.GetTestCloud(ctrl)

				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				tagValue := "TestTagValue"

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				first := mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, fmt.Errorf(accountLimitExceedManagementAPI))
				second := mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil)
				gomock.InOrder(first, second)
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().GetProperties(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.Account{Tags: map[string]*string{"TestKey": &tagValue}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()

				_, err := d.CreateVolume(ctx, req)
				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Premium storage account type (sku) loads from storage account when not given as parameter and share request size is increased to min. size required by premium",
			testFunc: func(t *testing.T) {
				name := "stoacc"
				sku := "premium"
				value := "foo bar"
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}
				capRange := &csi.CapacityRange{RequiredBytes: 1024 * 1024 * 1024, LimitBytes: 1024 * 1024 * 1024}

				allParam := map[string]string{
					locationField:         "loc",
					storageAccountField:   "stoacc",
					resourceGroupField:    "rg",
					shareNameField:        "",
					diskNameField:         "diskname.vhd",
					fsTypeField:           "",
					storeAccountKeyField:  "storeaccountkey",
					secretNamespaceField:  "default",
					mountPermissionsField: "0755",
					accountQuotaField:     "1000",
					protocolField:         smb,
				}
				req := &csi.CreateVolumeRequest{
					Name:               "vol-1",
					Parameters:         allParam,
					VolumeCapabilities: stdVolCap,
					CapacityRange:      capRange,
				}

				expectedShareOptions := &fileclient.ShareOptions{Name: "vol-1", Protocol: "SMB", RequestGiB: 100, AccessTier: "", RootSquash: "", Metadata: nil}

				d := NewFakeDriver()

				ctrl := gomock.NewController(t)
				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient
				d.cloud.StorageAccountClient = mockStorageAccountsClient

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), expectedShareOptions, gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().GetProperties(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts[0], nil).AnyTimes()

				_, err := d.CreateVolume(ctx, req)

				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Premium storage account type (sku) does not load from storage account for size request above min. premium size",
			testFunc: func(t *testing.T) {
				name := "stoacc"
				sku := "premium"
				value := "foo bar"
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}
				capRange := &csi.CapacityRange{RequiredBytes: 1024 * 1024 * 1024 * 100, LimitBytes: 1024 * 1024 * 1024 * 100}

				allParam := map[string]string{
					locationField:         "loc",
					storageAccountField:   "stoacc",
					resourceGroupField:    "rg",
					shareNameField:        "",
					diskNameField:         "diskname.vhd",
					fsTypeField:           "",
					storeAccountKeyField:  "storeaccountkey",
					secretNamespaceField:  "default",
					mountPermissionsField: "0755",
					accountQuotaField:     "1000",
					protocolField:         smb,
				}
				req := &csi.CreateVolumeRequest{
					Name:               "vol-1",
					Parameters:         allParam,
					VolumeCapabilities: stdVolCap,
					CapacityRange:      capRange,
				}

				expectedShareOptions := &fileclient.ShareOptions{Name: "vol-1", Protocol: "SMB", RequestGiB: 100, AccessTier: "", RootSquash: "", Metadata: nil}

				d := NewFakeDriver()

				ctrl := gomock.NewController(t)
				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient
				d.cloud.StorageAccountClient = mockStorageAccountsClient

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), expectedShareOptions, gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

				_, err := d.CreateVolume(ctx, req)

				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Storage account type (sku) defaults to standard type and share request size is unchanged",
			testFunc: func(t *testing.T) {
				name := "stoacc"
				sku := ""
				value := "foo bar"
				accounts := []storage.Account{
					{Name: &name, Sku: &storage.Sku{Name: storage.SkuName(sku)}},
				}
				keys := storage.AccountListKeysResult{
					Keys: &[]storage.AccountKey{
						{Value: &value},
					},
				}
				capRange := &csi.CapacityRange{RequiredBytes: 1024 * 1024 * 1024, LimitBytes: 1024 * 1024 * 1024}

				allParam := map[string]string{
					locationField:         "loc",
					storageAccountField:   "stoacc",
					resourceGroupField:    "rg",
					shareNameField:        "",
					diskNameField:         "diskname.vhd",
					fsTypeField:           "",
					storeAccountKeyField:  "storeaccountkey",
					secretNamespaceField:  "default",
					mountPermissionsField: "0755",
					accountQuotaField:     "1000",
				}
				req := &csi.CreateVolumeRequest{
					Name:               "vol-1",
					Parameters:         allParam,
					VolumeCapabilities: stdVolCap,
					CapacityRange:      capRange,
				}

				expectedShareOptions := &fileclient.ShareOptions{Name: "vol-1", Protocol: "SMB", RequestGiB: 1, AccessTier: "", RootSquash: "", Metadata: nil}

				d := NewFakeDriver()

				ctrl := gomock.NewController(t)
				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud.FileClient = mockFileClient
				d.cloud.StorageAccountClient = mockStorageAccountsClient

				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().CreateFileShare(context.TODO(), gomock.Any(), gomock.Any(), expectedShareOptions, gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().GetProperties(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts[0], nil).AnyTimes()

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
	ctx := context.TODO()

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
					VolumeId: "vol_1-cap-missing",
					Secrets:  map[string]string{},
				}

				d := NewFakeDriver()
				d.Cap = []*csi.ControllerServiceCapability{}

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
			name: "failed to get account info",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd##secret",
					Secrets:  map[string]string{},
				}

				d := NewFakeDriver()
				d.Cap = []*csi.ControllerServiceCapability{
					{
						Type: &csi.ControllerServiceCapability_Rpc{
							Rpc: &csi.ControllerServiceCapability_RPC{Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME},
						},
					},
				}
				d.dataPlaneAPIAccountCache, _ = azcache.NewTimedCache(10*time.Minute, func(key string) (interface{}, error) { return nil, nil }, false)
				d.dataPlaneAPIAccountCache.Set("f5713de20cde511e8ba4900", "1")
				d.cloud = &azure.Cloud{}

				expectedErr := status.Errorf(codes.NotFound, "get account info from(vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd##secret) failed with error: could not get account key from secret(azure-storage-account-f5713de20cde511e8ba4900-secret): KubeClient is nil")
				_, err := d.DeleteVolume(ctx, req)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Delete file share returns error",
			testFunc: func(t *testing.T) {
				req := &csi.DeleteVolumeRequest{
					VolumeId: "#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
					Secrets:  map[string]string{},
				}

				d := NewFakeDriver()
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud = &azure.Cloud{}
				d.cloud.FileClient = mockFileClient
				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().DeleteFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("test error")).Times(1)

				expectedErr := status.Errorf(codes.Internal, "DeleteFileShare fileshare under account(f5713de20cde511e8ba4900) rg() failed with error: test error")
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
					VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
					Secrets:  map[string]string{},
				}

				d := NewFakeDriver()
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				d.cloud = azure.GetTestCloud(ctrl)
				d.cloud.FileClient = mockFileClient
				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().DeleteFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

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

func TestCopyVolume(t *testing.T) {
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
	fakeShareQuota := int32(100)
	lessThanPremCapRange := &csi.CapacityRange{RequiredBytes: int64(fakeShareQuota * 1024 * 1024 * 1024)}

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "restore volume from volumeSnapshot nfs is not supported",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{}

				volumeSnapshotSource := &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: "unit-test",
				}
				volumeContentSourceSnapshotSource := &csi.VolumeContentSource_Snapshot{
					Snapshot: volumeSnapshotSource,
				}
				volumecontensource := csi.VolumeContentSource{
					Type: volumeContentSourceSnapshotSource,
				}

				req := &csi.CreateVolumeRequest{
					Name:                "random-vol-name-valid-request",
					VolumeCapabilities:  stdVolCap,
					CapacityRange:       lessThanPremCapRange,
					Parameters:          allParam,
					VolumeContentSource: &volumecontensource,
				}

				secret := map[string]string{}

				d := NewFakeDriver()
				ctx := context.Background()

				expectedErr := fmt.Errorf("protocol nfs is not supported for snapshot restore")
				err := d.copyVolume(ctx, req, "", "", []string{}, "", "", secret, &fileclient.ShareOptions{Protocol: storage.EnabledProtocolsNFS}, nil, "core.windows.net")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "restore volume from volumeSnapshot not found",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{}

				volumeSnapshotSource := &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: "unit-test",
				}
				volumeContentSourceSnapshotSource := &csi.VolumeContentSource_Snapshot{
					Snapshot: volumeSnapshotSource,
				}
				volumecontensource := csi.VolumeContentSource{
					Type: volumeContentSourceSnapshotSource,
				}

				req := &csi.CreateVolumeRequest{
					Name:                "random-vol-name-valid-request",
					VolumeCapabilities:  stdVolCap,
					CapacityRange:       lessThanPremCapRange,
					Parameters:          allParam,
					VolumeContentSource: &volumecontensource,
				}

				secret := map[string]string{}

				d := NewFakeDriver()
				ctx := context.Background()

				expectedErr := status.Errorf(codes.NotFound, "error parsing volume id: \"unit-test\", should at least contain two #")
				err := d.copyVolume(ctx, req, "", "", []string{}, "", "", secret, &fileclient.ShareOptions{Name: "dstFileshare"}, nil, "core.windows.net")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "restore volume from volumeSnapshot src fileshare is empty",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{}

				volumeSnapshotSource := &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: "rg#unit-test###",
				}
				volumeContentSourceSnapshotSource := &csi.VolumeContentSource_Snapshot{
					Snapshot: volumeSnapshotSource,
				}
				volumecontensource := csi.VolumeContentSource{
					Type: volumeContentSourceSnapshotSource,
				}

				req := &csi.CreateVolumeRequest{
					Name:                "random-vol-name-valid-request",
					VolumeCapabilities:  stdVolCap,
					CapacityRange:       lessThanPremCapRange,
					Parameters:          allParam,
					VolumeContentSource: &volumecontensource,
				}

				secret := map[string]string{}

				d := NewFakeDriver()
				ctx := context.Background()

				expectedErr := fmt.Errorf("srcFileShareName() or dstFileShareName(dstFileshare) is empty")
				err := d.copyVolume(ctx, req, "", "", []string{}, "", "", secret, &fileclient.ShareOptions{Name: "dstFileshare"}, nil, "core.windows.net")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "copy volume nfs is not supported",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{}

				volumeSource := &csi.VolumeContentSource_VolumeSource{
					VolumeId: "unit-test",
				}
				volumeContentSourceVolumeSource := &csi.VolumeContentSource_Volume{
					Volume: volumeSource,
				}
				volumecontensource := csi.VolumeContentSource{
					Type: volumeContentSourceVolumeSource,
				}

				req := &csi.CreateVolumeRequest{
					Name:                "random-vol-name-valid-request",
					VolumeCapabilities:  stdVolCap,
					CapacityRange:       lessThanPremCapRange,
					Parameters:          allParam,
					VolumeContentSource: &volumecontensource,
				}

				secret := map[string]string{}

				d := NewFakeDriver()
				ctx := context.Background()

				expectedErr := fmt.Errorf("protocol nfs is not supported for volume cloning")
				err := d.copyVolume(ctx, req, "", "", []string{}, "", "", secret, &fileclient.ShareOptions{Protocol: storage.EnabledProtocolsNFS}, nil, "core.windows.net")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "copy volume from volume not found",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{}

				volumeSource := &csi.VolumeContentSource_VolumeSource{
					VolumeId: "unit-test",
				}
				volumeContentSourceVolumeSource := &csi.VolumeContentSource_Volume{
					Volume: volumeSource,
				}
				volumecontensource := csi.VolumeContentSource{
					Type: volumeContentSourceVolumeSource,
				}

				req := &csi.CreateVolumeRequest{
					Name:                "random-vol-name-valid-request",
					VolumeCapabilities:  stdVolCap,
					CapacityRange:       lessThanPremCapRange,
					Parameters:          allParam,
					VolumeContentSource: &volumecontensource,
				}

				secret := map[string]string{}

				d := NewFakeDriver()
				ctx := context.Background()

				expectedErr := status.Errorf(codes.NotFound, "error parsing volume id: \"unit-test\", should at least contain two #")
				err := d.copyVolume(ctx, req, "", "", []string{}, "", "", secret, &fileclient.ShareOptions{Name: "dstFileshare"}, nil, "core.windows.net")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "src fileshare is empty",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{}

				volumeSource := &csi.VolumeContentSource_VolumeSource{
					VolumeId: "rg#unit-test##",
				}
				volumeContentSourceVolumeSource := &csi.VolumeContentSource_Volume{
					Volume: volumeSource,
				}
				volumecontensource := csi.VolumeContentSource{
					Type: volumeContentSourceVolumeSource,
				}

				req := &csi.CreateVolumeRequest{
					Name:                "random-vol-name-valid-request",
					VolumeCapabilities:  stdVolCap,
					CapacityRange:       lessThanPremCapRange,
					Parameters:          allParam,
					VolumeContentSource: &volumecontensource,
				}

				secret := map[string]string{}

				d := NewFakeDriver()
				ctx := context.Background()

				expectedErr := fmt.Errorf("srcFileShareName() or dstFileShareName(dstFileshare) is empty")
				err := d.copyVolume(ctx, req, "", "", []string{}, "", "", secret, &fileclient.ShareOptions{Name: "dstFileshare"}, nil, "core.windows.net")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "dst fileshare is empty",
			testFunc: func(t *testing.T) {
				allParam := map[string]string{}

				volumeSource := &csi.VolumeContentSource_VolumeSource{
					VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#",
				}
				volumeContentSourceVolumeSource := &csi.VolumeContentSource_Volume{
					Volume: volumeSource,
				}
				volumecontensource := csi.VolumeContentSource{
					Type: volumeContentSourceVolumeSource,
				}

				req := &csi.CreateVolumeRequest{
					Name:                "random-vol-name-valid-request",
					VolumeCapabilities:  stdVolCap,
					CapacityRange:       lessThanPremCapRange,
					Parameters:          allParam,
					VolumeContentSource: &volumecontensource,
				}

				secret := map[string]string{}

				d := NewFakeDriver()
				ctx := context.Background()

				expectedErr := fmt.Errorf("srcFileShareName(fileshare) or dstFileShareName() is empty")
				err := d.copyVolume(ctx, req, "", "", []string{}, "", "", secret, &fileclient.ShareOptions{}, nil, "core.windows.net")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "azcopy job is in progress",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				mp := map[string]string{}

				volumeSource := &csi.VolumeContentSource_VolumeSource{
					VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#",
				}
				volumeContentSourceVolumeSource := &csi.VolumeContentSource_Volume{
					Volume: volumeSource,
				}
				volumecontensource := csi.VolumeContentSource{
					Type: volumeContentSourceVolumeSource,
				}

				req := &csi.CreateVolumeRequest{
					Name:                "unit-test",
					VolumeCapabilities:  stdVolCap,
					Parameters:          mp,
					VolumeContentSource: &volumecontensource,
				}

				secret := map[string]string{}
				ctx := context.Background()

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				m := util.NewMockEXEC(ctrl)
				listStr1 := "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: InProgress\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false"
				m.EXPECT().RunCommand(gomock.Eq("azcopy jobs list | grep dstFileshare -B 3"), gomock.Any()).Return(listStr1, nil).Times(1)
				m.EXPECT().RunCommand(gomock.Not("azcopy jobs list | grep dstFileshare -B 3"), gomock.Any()).Return("Percent Complete (approx): 50.0", nil)

				d.azcopy.ExecCmd = m

				expectedErr := fmt.Errorf("wait for the existing AzCopy job to complete, current copy percentage is 50.0%%")
				err := d.copyVolume(ctx, req, "", "sastoken", []string{}, "", "", secret, &fileclient.ShareOptions{Name: "dstFileshare"}, nil, "core.windows.net")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestControllerGetVolume(t *testing.T) {
	d := NewFakeDriver()
	req := csi.ControllerGetVolumeRequest{}
	resp, err := d.ControllerGetVolume(context.Background(), &req)
	assert.Nil(t, resp)
	if !reflect.DeepEqual(err, status.Error(codes.Unimplemented, "")) {
		t.Errorf("Unexpected error: %v", err)
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
	fakeShareQuota := int32(100)

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
			expectedErr:        status.Errorf(codes.NotFound, "get account info from(vol_1) failed with error: <nil>"),
			mockedFileShareErr: nil,
		},
		{
			desc: "Check file share exists errors",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#",
				VolumeCapabilities: stdVolCap,
			},
			expectedErr:        status.Errorf(codes.Internal, "error checking if volume(vol_1#f5713de20cde511e8ba4900#fileshare#) exists: test error"),
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
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
				VolumeCapabilities: multiNodeVolCap,
			},
			expectedErr:        nil,
			mockedFileShareErr: nil,
		},
		{
			desc: "Valid request",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
				VolumeCapabilities: stdVolCap,
			},
			expectedErr:        nil,
			mockedFileShareErr: nil,
		},
		{
			desc: "Resource group empty",
			req: csi.ValidateVolumeCapabilitiesRequest{
				VolumeId:           "vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
				VolumeCapabilities: stdVolCap,
				VolumeContext: map[string]string{
					shareNameField: "sharename",
					diskNameField:  "diskname.vhd",
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
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(key, nil).AnyTimes()
		mockFileClient := mockfileclient.NewMockInterface(ctrl)
		d.cloud.FileClient = mockFileClient
		mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
		mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &fakeShareQuota}}, test.mockedFileShareErr).AnyTimes()

		_, err := d.ValidateVolumeCapabilities(context.TODO(), &test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test[%s]: unexpected error: %v, expected error: %v", test.desc, err, test.expectedErr)
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
			expectedErr: status.Errorf(codes.Internal, `GetFileShareInfo(vol_1) failed with error: error parsing volume id: "vol_1", should at least contain two #`),
		},
	}

	for _, test := range tests {
		_, err := d.CreateSnapshot(context.Background(), test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test[%s]: unexpected error: %v, expected error: %v", test.desc, err, test.expectedErr)
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
			expectedErr: nil,
		},
		{
			desc: "Invalid Snapshot ID",
			req: &csi.DeleteSnapshotRequest{
				SnapshotId: "testrg#testAccount#testFileShare#testuuid",
				Secrets:    map[string]string{"accountName": "TestAccountName", "accountKey": base64.StdEncoding.EncodeToString([]byte("TestAccountKey"))},
			},
			expectedErr: status.Error(codes.Internal, "failed to get snapshot name with (testrg#testAccount#testFileShare#testuuid): error parsing volume id: \"testrg#testAccount#testFileShare#testuuid\", should at least contain four #"),
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()

		_, err := d.DeleteSnapshot(context.Background(), test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test[%s]: unexpected error: %v, expected error: %v", test.desc, err, test.expectedErr)
		}
	}
}

func TestControllerExpandVolume(t *testing.T) {
	stdVolSize := int64(5 * 1024 * 1024 * 1024)
	stdCapRange := &csi.CapacityRange{RequiredBytes: stdVolSize}
	ctx := context.TODO()

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "Volume ID missing",
			testFunc: func(t *testing.T) {
				req := &csi.ControllerExpandVolumeRequest{}

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

				d := NewFakeDriver()
				d.Cap = []*csi.ControllerServiceCapability{}

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

				d := NewFakeDriver()
				d.Cap = []*csi.ControllerServiceCapability{}

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

				d := NewFakeDriver()

				expectedErr := status.Errorf(codes.InvalidArgument, "GetFileShareInfo(vol_1) failed with error: error parsing volume id: \"vol_1\", should at least contain two #")
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
					VolumeId:      "vol_1#f5713de20cde511e8ba4900#filename#diskname.vhd#",
					CapacityRange: stdCapRange,
				}

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				d.cloud.KubeClient = clientSet
				d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()

				expectErr := status.Error(codes.Unimplemented, "vhd disk volume(vol_1#f5713de20cde511e8ba4900#filename#diskname.vhd#, diskName:diskname.vhd) is not supported on ControllerExpandVolume")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectErr) {
					t.Errorf("Unexpected error: %v, expected error: %v", err, expectErr)
				}
			},
		},
		{
			name: "Resize file share returns error",
			testFunc: func(t *testing.T) {
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
				req := &csi.ControllerExpandVolumeRequest{
					VolumeId:      "vol_1#f5713de20cde511e8ba4900#filename#",
					CapacityRange: stdCapRange,
				}

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				d.cloud.KubeClient = clientSet
				d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "vol_1", gomock.Any()).Return(key, nil).AnyTimes()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().ResizeFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("test error")).AnyTimes()
				d.cloud.FileClient = mockFileClient

				expectErr := status.Errorf(codes.Internal, "expand volume error: test error")
				_, err := d.ControllerExpandVolume(ctx, req)
				if !reflect.DeepEqual(err, expectErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "get account info failed",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{
						ResourceGroup: "vol_2",
					},
				}
				d.dataPlaneAPIAccountCache, _ = azcache.NewTimedCache(10*time.Minute, func(key string) (interface{}, error) { return nil, nil }, false)
				d.dataPlaneAPIAccountCache.Set("f5713de20cde511e8ba4900", "1")
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
					VolumeId:      "#f5713de20cde511e8ba4900#filename##secret",
					CapacityRange: stdCapRange,
				}

				ctx := context.TODO()
				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				d.cloud.KubeClient = clientSet
				d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "vol_2", gomock.Any()).Return(key, &retry.Error{HTTPStatusCode: http.StatusBadGateway, RawError: cloudprovider.InstanceNotFound}).AnyTimes()

				expectErr := status.Error(codes.NotFound, "get account info from(#f5713de20cde511e8ba4900#filename##secret) failed with error: Retriable: false, RetryAfter: 0s, HTTPStatusCode: 502, RawError: instance not found")
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
					VolumeId:      "capz-d18sqm#f25f6e46c62274a4a8e433a#pvc-66ced8fb-a027-4eb6-87ca-e720ff36f683#pvc-66ced8fb-a027-4eb6-87ca-e720ff36f683#azurefile-2546",
					CapacityRange: stdCapRange,
				}

				mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
				d.cloud.StorageAccountClient = mockStorageAccountsClient
				d.cloud.KubeClient = clientSet
				d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "capz-d18sqm", gomock.Any()).Return(key, nil).AnyTimes()
				mockFileClient := mockfileclient.NewMockInterface(ctrl)
				mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
				mockFileClient.EXPECT().ResizeFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				shareQuota := int32(0)
				mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &shareQuota}}, nil).AnyTimes()
				d.cloud.FileClient = mockFileClient

				expectedResp := &csi.ControllerExpandVolumeResponse{CapacityBytes: stdVolSize}
				resp, err := d.ControllerExpandVolume(ctx, req)
				if !(reflect.DeepEqual(err, nil) && reflect.DeepEqual(resp, expectedResp)) {
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
			expectedErr:    fmt.Errorf("failed to get file share from vol_1"),
		},
		{
			desc:           "Volume ID error2",
			sourceVolumeID: "vol_1###",
			expectedErr:    fmt.Errorf("failed to get file share from vol_1###"),
		},
		{
			desc:           "Valid request",
			sourceVolumeID: "rg#accountname#fileshare#",
			expectedErr:    nil,
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "rg", gomock.Any()).Return(key, nil).AnyTimes()
		_, err := d.getShareURL(context.Background(), test.sourceVolumeID, validSecret)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test[%s]: unexpected error: %v, expected error: %v", test.desc, err, test.expectedErr)
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
			expectedErr:    nil,
		},
		{
			desc:           "Invalid Key",
			sourceVolumeID: "vol_1##",
			key:            errKey,
			expectedErr:    nil,
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
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "vol_1", gomock.Any()).Return(test.key, nil).AnyTimes()

		_, _, err := d.getServiceURL(context.Background(), test.sourceVolumeID, validSecret)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test[%s]: unexpected error: %v, expected error: %v", test.desc, err, test.expectedErr)
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
		secret         map[string]string
		expectedErr    error
	}{
		{
			desc:           "Invalid volume ID with data plane api",
			sourceVolumeID: "vol_1",
			key:            validKey,
			secret:         map[string]string{"accountName": "TestAccountName", "accountKey": base64.StdEncoding.EncodeToString([]byte("TestAccountKey"))},
			expectedErr:    fmt.Errorf("file share is empty after parsing sourceVolumeID: vol_1"),
		},
		{
			desc:           "Invalid volume ID with management api",
			sourceVolumeID: "vol_1",
			key:            validKey,
			secret:         validSecret,
			expectedErr:    fmt.Errorf("error parsing volume id: %q, should at least contain two #", "vol_1"),
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "", gomock.Any()).Return(test.key, nil).AnyTimes()

		_, _, _, _, err := d.snapshotExists(context.Background(), test.sourceVolumeID, "sname", test.secret, false)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test[%s]: unexpected error: %v, expected error: %v", test.desc, err, test.expectedErr)
		}
	}
}

func TestGetCapacity(t *testing.T) {
	d := NewFakeDriver()
	req := csi.GetCapacityRequest{}
	resp, err := d.GetCapacity(context.Background(), &req)
	assert.Nil(t, resp)
	if !reflect.DeepEqual(err, status.Error(codes.Unimplemented, "")) {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestListVolumes(t *testing.T) {
	d := NewFakeDriver()
	req := csi.ListVolumesRequest{}
	resp, err := d.ListVolumes(context.Background(), &req)
	assert.Nil(t, resp)
	if !reflect.DeepEqual(err, status.Error(codes.Unimplemented, "")) {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestListSnapshots(t *testing.T) {
	d := NewFakeDriver()
	req := csi.ListSnapshotsRequest{}
	resp, err := d.ListSnapshots(context.Background(), &req)
	assert.Nil(t, resp)
	if !reflect.DeepEqual(err, status.Error(codes.Unimplemented, "")) {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestSetAzureCredentials(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &azure.Cloud{
		Config: azure.Config{
			ResourceGroup: "rg",
			Location:      "loc",
			VnetName:      "fake-vnet",
			SubnetName:    "fake-subnet",
		},
	}
	fakeClient := fake.NewSimpleClientset()

	tests := []struct {
		desc            string
		kubeClient      kubernetes.Interface
		accountName     string
		accountKey      string
		secretName      string
		secretNamespace string
		expectedName    string
		expectedErr     error
	}{
		{
			desc:        "[failure] accountName is nil",
			kubeClient:  fakeClient,
			expectedErr: fmt.Errorf("the account info is not enough, accountName(), accountKey()"),
		},
		{
			desc:        "[failure] accountKey is nil",
			kubeClient:  fakeClient,
			accountName: "testName",
			accountKey:  "",
			expectedErr: fmt.Errorf("the account info is not enough, accountName(testName), accountKey()"),
		},
		{
			desc:        "[success] kubeClient is nil",
			kubeClient:  nil,
			expectedErr: nil,
		},
		{
			desc:         "[success] normal scenario",
			kubeClient:   fakeClient,
			accountName:  "testName",
			accountKey:   "testKey",
			expectedName: "azure-storage-account-testName-secret",
			expectedErr:  nil,
		},
		{
			desc:         "[success] already exist",
			kubeClient:   fakeClient,
			accountName:  "testName",
			accountKey:   "testKey",
			expectedName: "azure-storage-account-testName-secret",
			expectedErr:  nil,
		},
		{
			desc:            "[success] normal scenario using secretName",
			kubeClient:      fakeClient,
			accountName:     "testName",
			accountKey:      "testKey",
			secretName:      "secretName",
			secretNamespace: "secretNamespace",
			expectedName:    "secretName",
			expectedErr:     nil,
		},
	}

	for _, test := range tests {
		d.cloud.KubeClient = test.kubeClient
		result, err := d.SetAzureCredentials(context.TODO(), test.accountName, test.accountKey, test.secretName, test.secretNamespace)
		if result != test.expectedName || !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("desc: %s,\n input: accountName(%v), accountKey(%v),\n setAzureCredentials result: %v, expectedName: %v err: %v, expectedErr: %v",
				test.desc, test.accountName, test.accountKey, result, test.expectedName, err, test.expectedErr)
		}
	}
}

func TestGenerateSASToken(t *testing.T) {
	d := NewFakeDriver()
	storageEndpointSuffix := "core.windows.net"
	tests := []struct {
		name        string
		accountName string
		accountKey  string
		want        string
		expectedErr error
	}{
		{
			name:        "accountName nil",
			accountName: "",
			accountKey:  "",
			want:        "se=",
			expectedErr: nil,
		},
		{
			name:        "account key illegal",
			accountName: "unit-test",
			accountKey:  "fakeValue",
			want:        "",
			expectedErr: status.Errorf(codes.Internal, fmt.Sprintf("failed to generate sas token in creating new shared key credential, accountName: %s, err: %s", "unit-test", "decode account key: illegal base64 data at input byte 8")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sas, err := d.generateSASToken(tt.accountName, tt.accountKey, storageEndpointSuffix, 30)
			if !reflect.DeepEqual(err, tt.expectedErr) {
				t.Errorf("generateSASToken error = %v, expectedErr %v, sas token = %v, want %v", err, tt.expectedErr, sas, tt.want)
				return
			}
			if !strings.Contains(sas, tt.want) {
				t.Errorf("sas token = %v, want %v", sas, tt.want)
			}
		})
	}
}

func TestAuthorizeAzcopyWithIdentity(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "use service principal to authorize azcopy",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{
						AzureAuthConfig: config.AzureAuthConfig{
							ARMClientConfig: azclient.ARMClientConfig{
								TenantID: "TenantID",
							},
							AzureAuthConfig: azclient.AzureAuthConfig{
								AADClientID:     "AADClientID",
								AADClientSecret: "AADClientSecret",
							},
						},
					},
				}
				expectedAuthAzcopyEnv := []string{
					fmt.Sprintf(azcopyAutoLoginType + "=SPN"),
					fmt.Sprintf(azcopySPAApplicationID + "=AADClientID"),
					fmt.Sprintf(azcopySPAClientSecret + "=AADClientSecret"),
					fmt.Sprintf(azcopyTenantID + "=TenantID"),
				}
				var expectedErr error
				authAzcopyEnv, err := d.authorizeAzcopyWithIdentity()
				if !reflect.DeepEqual(authAzcopyEnv, expectedAuthAzcopyEnv) || !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected authAzcopyEnv: %v, Unexpected error: %v", authAzcopyEnv, err)
				}
			},
		},
		{
			name: "use service principal to authorize azcopy but client id is empty",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{
						AzureAuthConfig: config.AzureAuthConfig{
							ARMClientConfig: azclient.ARMClientConfig{
								TenantID: "TenantID",
							},
							AzureAuthConfig: azclient.AzureAuthConfig{
								AADClientSecret: "AADClientSecret",
							},
						},
					},
				}
				expectedAuthAzcopyEnv := []string{}
				expectedErr := fmt.Errorf("AADClientID and TenantID must be set when use service principal")
				authAzcopyEnv, err := d.authorizeAzcopyWithIdentity()
				if !reflect.DeepEqual(authAzcopyEnv, expectedAuthAzcopyEnv) || !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected authAzcopyEnv: %v, Unexpected error: %v", authAzcopyEnv, err)
				}
			},
		},
		{
			name: "use user assigned managed identity to authorize azcopy",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{
						AzureAuthConfig: config.AzureAuthConfig{
							AzureAuthConfig: azclient.AzureAuthConfig{
								UseManagedIdentityExtension: true,
								UserAssignedIdentityID:      "UserAssignedIdentityID",
							},
						},
					},
				}
				expectedAuthAzcopyEnv := []string{
					fmt.Sprintf(azcopyAutoLoginType + "=MSI"),
					fmt.Sprintf(azcopyMSIClientID + "=UserAssignedIdentityID"),
				}
				var expectedErr error
				authAzcopyEnv, err := d.authorizeAzcopyWithIdentity()
				if !reflect.DeepEqual(authAzcopyEnv, expectedAuthAzcopyEnv) || !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected authAzcopyEnv: %v, Unexpected error: %v", authAzcopyEnv, err)
				}
			},
		},
		{
			name: "use system assigned managed identity to authorize azcopy",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{
						AzureAuthConfig: config.AzureAuthConfig{
							AzureAuthConfig: azclient.AzureAuthConfig{
								UseManagedIdentityExtension: true,
							},
						},
					},
				}
				expectedAuthAzcopyEnv := []string{
					fmt.Sprintf(azcopyAutoLoginType + "=MSI"),
				}
				var expectedErr error
				authAzcopyEnv, err := d.authorizeAzcopyWithIdentity()
				if !reflect.DeepEqual(authAzcopyEnv, expectedAuthAzcopyEnv) || !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected authAzcopyEnv: %v, Unexpected error: %v", authAzcopyEnv, err)
				}
			},
		},
		{
			name: "AADClientSecret be nil and useManagedIdentityExtension is false",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{
						AzureAuthConfig: config.AzureAuthConfig{},
					},
				}
				expectedAuthAzcopyEnv := []string{}
				expectedErr := fmt.Errorf("neither the service principal nor the managed identity has been set")
				authAzcopyEnv, err := d.authorizeAzcopyWithIdentity()
				if !reflect.DeepEqual(authAzcopyEnv, expectedAuthAzcopyEnv) || !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected authAzcopyEnv: %v, Unexpected error: %v", authAzcopyEnv, err)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestGetAzcopyAuth(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "failed to get accountKey in secrets",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{},
				}
				secrets := map[string]string{
					defaultSecretAccountName: "accountName",
				}

				ctx := context.Background()
				expectedAccountSASToken := ""
				expectedErr := fmt.Errorf("could not find accountkey or azurestorageaccountkey field in secrets")
				accountSASToken, authAzcopyEnv, err := d.getAzcopyAuth(ctx, "accountName", "", "core.windows.net", &azure.AccountOptions{}, secrets, "secretsName", "secretsNamespace", false)
				if !reflect.DeepEqual(err, expectedErr) || authAzcopyEnv != nil || !reflect.DeepEqual(accountSASToken, expectedAccountSASToken) {
					t.Errorf("Unexpected accountSASToken: %s, Unexpected authAzcopyEnv: %v, Unexpected error: %v", accountSASToken, authAzcopyEnv, err)
				}
			},
		},
		{
			name: "generate SAS token failed for illegal account key",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &azure.Cloud{
					Config: azure.Config{},
				}
				secrets := map[string]string{
					defaultSecretAccountName: "accountName",
					defaultSecretAccountKey:  "fakeValue",
				}

				ctx := context.Background()
				expectedAccountSASToken := ""
				expectedErr := status.Errorf(codes.Internal, fmt.Sprintf("failed to generate sas token in creating new shared key credential, accountName: %s, err: %s", "accountName", "decode account key: illegal base64 data at input byte 8"))
				accountSASToken, authAzcopyEnv, err := d.getAzcopyAuth(ctx, "accountName", "", "core.windows.net", &azure.AccountOptions{}, secrets, "secretsName", "secretsNamespace", false)
				if !reflect.DeepEqual(err, expectedErr) || authAzcopyEnv != nil || !reflect.DeepEqual(accountSASToken, expectedAccountSASToken) {
					t.Errorf("Unexpected accountSASToken: %s, Unexpected authAzcopyEnv: %v, Unexpected error: %v", accountSASToken, authAzcopyEnv, err)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}
