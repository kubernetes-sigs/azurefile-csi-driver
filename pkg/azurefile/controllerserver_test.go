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
	"reflect"
	"runtime"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/azurefile-csi-driver/pkg/util"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/accountclient/mock_accountclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/fileshareclient/mock_fileshareclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/mock_azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/subnetclient/mock_subnetclient"
	azcache "sigs.k8s.io/cloud-provider-azure/pkg/cache"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/config"
	auth "sigs.k8s.io/cloud-provider-azure/pkg/provider/config"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/storage"
)

var _ = ginkgo.Describe("TestCreateVolume", func() {
	var d *Driver
	var ctrl *gomock.Controller
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
	lessThanPremCapRange := &csi.CapacityRange{RequiredBytes: int64(fakeShareQuota * 1024 * 1024 * 1024)}

	var computeClientFactory *mock_azclient.MockClientFactory
	var networkClientFactory *mock_azclient.MockClientFactory
	var mockFileClient *mock_fileshareclient.MockInterface
	ginkgo.BeforeEach(func() {
		stdVolCap = []*csi.VolumeCapability{
			{
				AccessType: &csi.VolumeCapability_Mount{
					Mount: &csi.VolumeCapability_MountVolume{},
				},
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
				},
			},
		}
		fakeShareQuota = int32(100)
		stdVolSize = int64(5 * 1024 * 1024 * 1024)
		stdCapRange = &csi.CapacityRange{RequiredBytes: stdVolSize}
		lessThanPremCapRange = &csi.CapacityRange{RequiredBytes: int64(fakeShareQuota * 1024 * 1024 * 1024)}
		d = NewFakeDriver()
		ctrl = gomock.NewController(ginkgo.GinkgoT())

		computeClientFactory = mock_azclient.NewMockClientFactory(ctrl)
		networkClientFactory = mock_azclient.NewMockClientFactory(ctrl)
		networkClientFactory.EXPECT().GetSubnetClient().Return(mock_subnetclient.NewMockInterface(ctrl)).AnyTimes()
		mockFileClient = mock_fileshareclient.NewMockInterface(ctrl)
		computeClientFactory.EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, nil).AnyTimes()
		accountClient := mock_accountclient.NewMockInterface(ctrl)
		computeClientFactory.EXPECT().GetAccountClient().Return(accountClient).AnyTimes()
		computeClientFactory.EXPECT().GetAccountClientForSub(gomock.Any()).Return(accountClient, nil).AnyTimes()

		var err error
		d.cloud, err = storage.NewRepository(
			config.Config{},
			&azclient.Environment{},
			computeClientFactory,
			networkClientFactory,
		)
		d.kubeClient = fake.NewSimpleClientset()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})
	ginkgo.AfterEach(func() {
		ctrl.Finish()
	})
	ginkgo.When("Controller Capability missing", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-cap-missing",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         nil,
			}

			d.Cap = []*csi.ControllerServiceCapability{}

			expectedErr := status.Errorf(codes.InvalidArgument, "CREATE_DELETE_VOLUME")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})

	ginkgo.When("Volume name missing", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.CreateVolumeRequest{
				Name:               "",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         nil,
			}
			expectedErr := status.Error(codes.InvalidArgument, "CreateVolume Name must be provided")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Volume capabilities missing", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.CreateVolumeRequest{
				Name:          "random-vol-name-vol-cap-missing",
				CapacityRange: stdCapRange,
				Parameters:    nil,
			}
			expectedErr := status.Error(codes.InvalidArgument, "CreateVolume Volume capabilities not valid: CreateVolume Volume capabilities must be provided")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid volume capabilities", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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
			expectedErr := status.Error(codes.InvalidArgument, "CreateVolume Volume capabilities not valid: driver does not support block volumes")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Volume lock already present", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-vol-cap-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         nil,
			}
			locks := newVolumeLocks()
			locks.locks.Insert(req.GetName())
			d.volumeLocks = locks

			expectedErr := status.Error(codes.Aborted, "An operation with the given Volume ID random-vol-name-vol-cap-invalid already exists")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Disabled fsType", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid fsType", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid protocol", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				protocolField: "test_protocol",
			}

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-vol-cap-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			expectedErr := status.Errorf(codes.InvalidArgument, "protocol(test_protocol) is not supported, supported protocol list: [smb nfs]")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("nfs protocol only supports premium storage", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				protocolField: "nfs",
				skuNameField:  "Standard_LRS",
			}

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-nfs-protocol-standard-SKU",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			expectedErr := status.Errorf(codes.InvalidArgument, "nfs protocol only supports premium storage, current account type: Standard_LRS")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid accessTier", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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
			expectedErr := status.Errorf(codes.InvalidArgument, "shareAccessTier(test_accessTier) is not supported, supported ShareAccessTier list: [Cool Hot Premium TransactionOptimized]")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid rootSquashType", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				rootSquashTypeField: "test_rootSquashType",
			}

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-vol-cap-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			expectedErr := status.Errorf(codes.InvalidArgument, "rootSquashType(test_rootSquashType) is not supported, supported RootSquashType list: [AllSquash NoRootSquash RootSquash]")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid fsGroupChangePolicy", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				fsGroupChangePolicyField: "test_fsGroupChangePolicy",
			}

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-vol-cap-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			expectedErr := status.Errorf(codes.InvalidArgument, "fsGroupChangePolicy(test_fsGroupChangePolicy) is not supported, supported fsGroupChangePolicy list: [None Always OnRootMismatch]")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid shareNamePrefix", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				shareNamePrefixField: "-invalid",
			}

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-vol-cap-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			expectedErr := status.Errorf(codes.InvalidArgument, "shareNamePrefix(-invalid) can only contain lowercase letters, numbers, hyphens, and length should be less than 21")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid accountQuota", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				accountQuotaField: "10",
			}

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-vol-cap-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			expectedErr := status.Errorf(codes.InvalidArgument, "invalid accountQuota %d in storage class, minimum quota: %d", 10, minimumAccountQuota)
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("invalid tags format to convert to map", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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
			expectedErr := status.Errorf(codes.InvalidArgument, "Tags 'tags' are invalid, the format should like: 'key1=value1,key2=value2'")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid protocol & fsType combination", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("storeAccountKey must set as true in cross subscription", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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
			d.cloud = &storage.AccountRepo{
				Config: config.Config{},
			}

			expectedErr := status.Errorf(codes.InvalidArgument, "resourceGroup must be provided in cross subscription(abc)")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("invalid selectRandomMatchingAccount value", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				selectRandomMatchingAccountField: "invalid",
			}

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-selectRandomMatchingAccount-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			d.cloud = &storage.AccountRepo{
				Config: config.Config{},
			}

			expectedErr := status.Errorf(codes.InvalidArgument, "invalid selectrandommatchingaccount: invalid in storage class")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("invalid getLatestAccountKey value", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				getLatestAccountKeyField: "invalid",
			}

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-getLatestAccountKey-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			d.cloud = &storage.AccountRepo{
				Config: config.Config{},
			}

			expectedErr := status.Errorf(codes.InvalidArgument, "invalid getlatestaccountkey: invalid in storage class")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("storageAccount and matchTags conflict", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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
			d.cloud = &storage.AccountRepo{
				Config: config.Config{},
			}

			expectedErr := status.Errorf(codes.InvalidArgument, "matchTags must set as false when storageAccount(abc) is provided")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("invalid privateEndpoint and subnetName combination", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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
			d.cloud = &storage.AccountRepo{
				Config: config.Config{},
			}

			expectedErr := status.Errorf(codes.InvalidArgument, "subnetName(subnet1,subnet2) can only contain one subnet for private endpoint")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Failed to update subnet service endpoints", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				protocolField: "nfs",
			}

			fakeCloud := &storage.AccountRepo{
				Config: config.Config{
					ResourceGroup: "rg",
					Location:      "loc",
					VnetName:      "fake-vnet",
					SubnetName:    "fake-subnet",
				},
			}
			retErr := fmt.Errorf("the subnet does not exist")

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-vol-cap-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			d.cloud = fakeCloud
			mockSubnetClient := mock_subnetclient.NewMockInterface(ctrl)
			fakeCloud.NetworkClientFactory = mock_azclient.NewMockClientFactory(ctrl)
			fakeCloud.NetworkClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetSubnetClient().Return(mockSubnetClient).AnyTimes()

			mockSubnetClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return([]*armnetwork.Subnet{}, retErr).Times(1)

			expectedErr := status.Errorf(codes.Internal, "update service endpoints failed with error: failed to list the subnets under rg rg vnet fake-vnet: the subnet does not exist")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Failed with storeAccountKey is not supported for account with shared access key disabled", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			allParam := map[string]string{
				skuNameField:              "premium",
				storageAccountTypeField:   "stoacctype",
				locationField:             "loc",
				storageAccountField:       "stoacc",
				resourceGroupField:        "rg",
				shareNameField:            "",
				diskNameField:             "diskname.vhd",
				fsTypeField:               "",
				storeAccountKeyField:      "storeaccountkey",
				secretNamespaceField:      "default",
				mountPermissionsField:     "0755",
				accountQuotaField:         "1000",
				allowSharedKeyAccessField: "false",
			}

			fakeCloud := &storage.AccountRepo{
				Config: config.Config{
					ResourceGroup: "rg",
					Location:      "loc",
					VnetName:      "fake-vnet",
					SubnetName:    "fake-subnet",
				},
			}

			req := &csi.CreateVolumeRequest{
				Name:               "random-vol-name-vol-cap-invalid",
				CapacityRange:      stdCapRange,
				VolumeCapabilities: stdVolCap,
				Parameters:         allParam,
			}
			d.cloud = fakeCloud

			expectedErr := status.Errorf(codes.InvalidArgument, "storeAccountKey is not supported for account with shared access key disabled")
			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("No valid key, check all params, with less than min premium volume", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			name := "baz"
			SKU := "SKU"
			kind := "StorageV2"
			location := "centralus"
			value := ""
			accounts := []*armstorage.Account{
				{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}, Kind: to.Ptr(armstorage.Kind(kind)), Location: &location},
			}
			keys := []*armstorage.AccountKey{
				{Value: &value},
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

			mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

			mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
			mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
			mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
			mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

			expectedErr := fmt.Errorf("no valid keys")

			_, err := d.CreateVolume(ctx, req)
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(expectedErr.Error()))
		})
	})
	ginkgo.When("management client", func() {
		ginkgo.When("Get file share returns error", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "baz"
				SKU := "SKU"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}, Kind: to.Ptr(armstorage.Kind(kind)), Location: &location, Properties: &armstorage.AccountProperties{}},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
				}

				req := &csi.CreateVolumeRequest{
					Name:               "random-vol-name-get-file-error",
					VolumeCapabilities: stdVolCap,
					CapacityRange:      stdCapRange,
					Parameters:         nil,
				}

				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

				mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, fmt.Errorf("test error")).AnyTimes()

				expectedErr := status.Errorf(codes.Internal, "test error")

				_, err := d.CreateVolume(ctx, req)
				gomega.Expect(err).To(gomega.Equal(expectedErr))
			})
		})
		ginkgo.When("Create file share error tests", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "baz"
				SKU := "SKU"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}, Kind: to.Ptr(armstorage.Kind(kind)), Location: &location},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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

				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()

				expectedErr := status.Errorf(codes.Internal, "FileShareProperties or FileShareProperties.ShareQuota is nil")

				_, err := d.CreateVolume(ctx, req)
				gomega.Expect(err).To(gomega.Equal(expectedErr))
			})
		})
		ginkgo.When("existing file share quota is smaller than request quota", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "baz"
				SKU := "SKU"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}, Kind: to.Ptr(armstorage.Kind(kind)), Location: &location},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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

				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: ptr.To(int32(1))}}, nil).AnyTimes()

				expectedErr := status.Errorf(codes.AlreadyExists, "request file share(random-vol-name-crete-file-error) already exists, but its capacity 1 is smaller than 100")
				_, err := d.CreateVolume(ctx, req)
				gomega.Expect(err).To(gomega.Equal(expectedErr))
			})
		})
		ginkgo.When("Create disk returns error", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				if runtime.GOOS == "windows" {
					ginkgo.Skip("Skipping test on Windows")
				}
				name := "baz"
				SKU := "SKU"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}, Kind: to.Ptr(armstorage.Kind(kind)), Location: &location},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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
				d.cloud = &storage.AccountRepo{}
				d.cloud.ComputeClientFactory = computeClientFactory
				d.kubeClient = fake.NewSimpleClientset()

				tests := []struct {
					desc          string
					fileSharename string
					expectedErr   error
				}{
					{
						desc:          "File share name empty",
						fileSharename: "",
						expectedErr:   status.Error(codes.Internal, "failed to create VHD disk: NewSharedKeyCredential(stoacc) failed with error: decode account key: illegal base64 data at input byte 0"),
					},
					{
						desc:          "File share name provided",
						fileSharename: "filesharename",
						expectedErr:   status.Error(codes.Internal, "failed to create VHD disk: NewSharedKeyCredential(stoacc) failed with error: decode account key: illegal base64 data at input byte 0"),
					},
				}
				for _, test := range tests {
					allParam[shareNameField] = test.fileSharename

					mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

					mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
					mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
					mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
					mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
					mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()

					_, err := d.CreateVolume(ctx, req)
					gomega.Expect(err).To(gomega.Equal(test.expectedErr))
				}
			})
		})
		ginkgo.When("Valid request", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "baz"
				SKU := "SKU"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}, Kind: to.Ptr(armstorage.Kind(kind)), Location: &location},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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

				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

				mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()
				_, err := d.CreateVolume(ctx, req)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			})
		})
		ginkgo.When("invalid mountPermissions", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "baz"
				SKU := "SKU"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}, Kind: to.Ptr(armstorage.Kind(kind)), Location: &location},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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

				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

				mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()

				expectedErr := status.Errorf(codes.InvalidArgument, "invalid %s %s in storage class", "mountPermissions", "0abc")
				_, err := d.CreateVolume(ctx, req)
				gomega.Expect(err).To(gomega.Equal(expectedErr))
			})
		})
		ginkgo.When("invalid parameter", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "baz"
				SKU := "SKU"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}, Kind: to.Ptr(armstorage.Kind(kind)), Location: &location},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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

				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

				mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()

				expectedErr := status.Errorf(codes.InvalidArgument, "invalid parameter %q in storage class", "invalidparameter")
				_, err := d.CreateVolume(ctx, req)
				gomega.Expect(err).To(gomega.Equal(expectedErr))
			})
		})
		ginkgo.When("Account limit exceeded", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "baz"
				SKU := "SKU"
				kind := "StorageV2"
				location := "centralus"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}, Kind: to.Ptr(armstorage.Kind(kind)), Location: &location},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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
				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)
				var err error

				tagValue := "TestTagValue"

				first := mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, fmt.Errorf(accountLimitExceedManagementAPI))
				second := mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil)
				gomock.InOrder(first, second)
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().GetProperties(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.Account{Tags: map[string]*string{"TestKey": &tagValue}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()

				_, err = d.CreateVolume(ctx, req)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())

			})
		})
		ginkgo.When("Premium storage account type (SKU) loads from storage account when not given as parameter and share request size is increased to min. size required by premium", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "stoacc"
				SKU := "premium"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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

				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

				mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().GetProperties(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts[0], nil).AnyTimes()

				_, err := d.CreateVolume(ctx, req)

				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			})
		})
		ginkgo.When("Premium storage account type (SKU) does not load from storage account for size request above min. premium size", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "stoacc"
				SKU := "premium"
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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

				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

				mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

				_, err := d.CreateVolume(ctx, req)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			})
		})
		ginkgo.When("Storage account type (SKU) defaults to standard type and share request size is unchanged", func() {
			ginkgo.It("should fail", func(ctx context.Context) {
				name := "stoacc"
				SKU := ""
				value := "foo bar"
				accounts := []*armstorage.Account{
					{Name: &name, SKU: &armstorage.SKU{Name: to.Ptr(armstorage.SKUName(SKU))}},
				}
				keys := []*armstorage.AccountKey{
					{Value: &value},
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

				mockStorageAccountsClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)

				mockFileClient.EXPECT().Create(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: nil}}, nil).AnyTimes()
				mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &fakeShareQuota}}, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(accounts, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				mockStorageAccountsClient.EXPECT().GetProperties(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(accounts[0], nil).AnyTimes()

				_, err := d.CreateVolume(ctx, req)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			})
		})
	})
})

var _ = ginkgo.Describe("TestDeleteVolume", func() {
	var ctrl *gomock.Controller
	var d *Driver
	ginkgo.BeforeEach(func() {
		ctrl = gomock.NewController(ginkgo.GinkgoT())
		d = NewFakeDriver()
		var err error
		computeClientFactory := mock_azclient.NewMockClientFactory(ctrl)
		accountClient := mock_accountclient.NewMockInterface(ctrl)
		computeClientFactory.EXPECT().GetAccountClientForSub(gomock.Any()).Return(accountClient, nil).AnyTimes()
		computeClientFactory.EXPECT().GetAccountClient().Return(accountClient).AnyTimes()
		mockFileClient := mock_fileshareclient.NewMockInterface(ctrl)
		computeClientFactory.EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, nil).AnyTimes()
		computeClientFactory.EXPECT().GetFileShareClient().Return(mockFileClient).AnyTimes()
		networkClientFactory := mock_azclient.NewMockClientFactory(ctrl)
		networkClientFactory.EXPECT().GetSubnetClient().Return(mock_subnetclient.NewMockInterface(ctrl)).AnyTimes()
		d.cloud, err = storage.NewRepository(config.Config{}, &azclient.Environment{}, computeClientFactory, networkClientFactory)
		gomega.Expect(err).To(gomega.BeNil())

		d.kubeClient = fake.NewSimpleClientset()
	})
	ginkgo.AfterEach(func() {
		ctrl.Finish()
	})
	ginkgo.When("Volume ID missing", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.DeleteVolumeRequest{
				Secrets: map[string]string{},
			}

			expectedErr := status.Error(codes.InvalidArgument, "Volume ID missing in request")
			_, err := d.DeleteVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Controller capability missing", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.DeleteVolumeRequest{
				VolumeId: "vol_1-cap-missing",
				Secrets:  map[string]string{},
			}

			d.Cap = []*csi.ControllerServiceCapability{}

			expectedErr := status.Errorf(codes.InvalidArgument, "invalid delete volume request: %v", req)
			_, err := d.DeleteVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid volume ID", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.DeleteVolumeRequest{
				VolumeId: "vol_1",
				Secrets:  map[string]string{},
			}

			d.Cap = []*csi.ControllerServiceCapability{
				{
					Type: &csi.ControllerServiceCapability_Rpc{
						Rpc: &csi.ControllerServiceCapability_RPC{Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME},
					},
				},
			}

			_, err := d.DeleteVolume(ctx, req)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

		})
	})
	ginkgo.When("Delete file share returns error", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.DeleteVolumeRequest{
				VolumeId: "#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
				Secrets:  map[string]string{},
			}
			var err error
			mockAccountClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)
			mockAccountClient.EXPECT().GetProperties(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.Account{}, nil).AnyTimes()
			mockFileClient := d.cloud.ComputeClientFactory.GetFileShareClient().(*mock_fileshareclient.MockInterface)

			mockFileClient.EXPECT().Delete(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("test error")).Times(1)
			expectedErr := status.Errorf(codes.Internal, "DeleteFileShare fileshare under account(f5713de20cde511e8ba4900) rg() failed with error: test error")
			_, err = d.DeleteVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Valid request", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.DeleteVolumeRequest{
				VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
				Secrets:  map[string]string{},
			}
			mockAccountClient := d.cloud.ComputeClientFactory.GetAccountClient().(*mock_accountclient.MockInterface)
			mockAccountClient.EXPECT().GetProperties(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.Account{}, nil).AnyTimes()
			mockFileClient := d.cloud.ComputeClientFactory.GetFileShareClient().(*mock_fileshareclient.MockInterface)
			var err error

			mockFileClient.EXPECT().Delete(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

			expectedResp := &csi.DeleteSnapshotResponse{}
			resp, err := d.DeleteVolume(ctx, req)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(resp).To(gomega.BeEquivalentTo(expectedResp))
		})
	})
})

var _ = ginkgo.Describe("TestCopyVolume", func() {
	var ctrl *gomock.Controller
	var d *Driver
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
	ginkgo.BeforeEach(func() {
		stdVolCap = []*csi.VolumeCapability{
			{
				AccessType: &csi.VolumeCapability_Mount{
					Mount: &csi.VolumeCapability_MountVolume{},
				},
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
				},
			},
		}
		fakeShareQuota = int32(100)
		lessThanPremCapRange = &csi.CapacityRange{RequiredBytes: int64(fakeShareQuota * 1024 * 1024 * 1024)}
		ctrl = gomock.NewController(ginkgo.GinkgoT())
		d = NewFakeDriver()
	})
	ginkgo.AfterEach(func() {
		ctrl.Finish()
	})
	ginkgo.When("restore volume from volumeSnapshot nfs is not supported", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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

			expectedErr := fmt.Errorf("protocol nfs is not supported for snapshot restore")
			err := d.copyVolume(ctx, req, "", "", []string{}, "", &ShareOptions{Protocol: armstorage.EnabledProtocolsNFS}, nil, "core.windows.net")
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("restore volume from volumeSnapshot not found", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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

			expectedErr := status.Errorf(codes.NotFound, "error parsing volume id: \"unit-test\", should at least contain two #")
			err := d.copyVolume(ctx, req, "", "", []string{}, "", &ShareOptions{Name: "dstFileshare"}, nil, "core.windows.net")
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("restore volume from volumeSnapshot src fileshare is empty", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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

			expectedErr := fmt.Errorf("one or more of srcAccountName(unit-test), srcFileShareName(), dstFileShareName(dstFileshare) are empty")
			err := d.copyVolume(ctx, req, "", "", []string{}, "", &ShareOptions{Name: "dstFileshare"}, nil, "core.windows.net")
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("copy volume nfs is not supported", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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

			expectedErr := fmt.Errorf("protocol nfs is not supported for volume cloning")
			err := d.copyVolume(ctx, req, "", "", []string{}, "", &ShareOptions{Protocol: armstorage.EnabledProtocolsNFS}, nil, "core.windows.net")
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("copy volume from volume not found", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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

			expectedErr := status.Errorf(codes.NotFound, "error parsing volume id: \"unit-test\", should at least contain two #")
			err := d.copyVolume(ctx, req, "", "", []string{}, "", &ShareOptions{Name: "dstFileshare"}, nil, "core.windows.net")
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("src fileshare is empty", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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

			expectedErr := fmt.Errorf("one or more of srcAccountName(unit-test), srcFileShareName(), dstFileShareName(dstFileshare) are empty")
			err := d.copyVolume(ctx, req, "", "", []string{}, "", &ShareOptions{Name: "dstFileshare"}, nil, "core.windows.net")
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("dst fileshare is empty", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
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

			expectedErr := fmt.Errorf("one or more of srcAccountName(f5713de20cde511e8ba4900), srcFileShareName(fileshare), dstFileShareName() are empty")
			err := d.copyVolume(ctx, req, "", "", []string{}, "", &ShareOptions{}, nil, "core.windows.net")
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("azcopy job is in progress", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			accountOptions := storage.AccountOptions{}
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

			m := util.NewMockEXEC(ctrl)
			listStr1 := "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: InProgress\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false"
			m.EXPECT().RunCommand(gomock.Eq("azcopy jobs list | grep dstFileshare -B 3"), gomock.Any()).Return(listStr1, nil).AnyTimes()
			m.EXPECT().RunCommand(gomock.Not("azcopy jobs list | grep dstFileshare -B 3"), gomock.Any()).Return("Percent Complete (approx): 50.0", nil).AnyTimes()

			d.azcopy.ExecCmd = m
			d.waitForAzCopyTimeoutMinutes = 1

			err := d.copyVolume(ctx, req, "", "sastoken", []string{}, "", &ShareOptions{Name: "dstFileshare"}, &accountOptions, "core.windows.net")
			gomega.Expect(err).To(gomega.Equal(wait.ErrWaitTimeout))
		})
	})
})

var _ = ginkgo.Describe("ControllerGetVolume", func() {
	ginkgo.When("test", func() {
		ginkgo.It("should work", func(_ context.Context) {
			d := NewFakeDriver()
			req := csi.ControllerGetVolumeRequest{}
			resp, err := d.ControllerGetVolume(context.Background(), &req)
			gomega.Expect(resp).To(gomega.BeNil())
			gomega.Expect(err).To(gomega.Equal(status.Error(codes.Unimplemented, "")))
		})
	})
})

var _ = ginkgo.Describe("ControllerGetCapabilities", func() {
	ginkgo.When("test", func() {
		ginkgo.It("should work", func(_ context.Context) {

			d := NewFakeDriver()
			controlCap := []*csi.ControllerServiceCapability{
				{
					Type: &csi.ControllerServiceCapability_Rpc{},
				},
			}
			d.Cap = controlCap
			req := csi.ControllerGetCapabilitiesRequest{}
			resp, err := d.ControllerGetCapabilities(context.Background(), &req)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(resp).NotTo(gomega.BeNil())
			gomega.Expect(resp.Capabilities).To(gomega.Equal(controlCap))
		})
	})
})

var _ = ginkgo.DescribeTable("ValidateVolumeCapabilities", func(
	req *csi.ValidateVolumeCapabilitiesRequest,
	expectedErr error,
	mockedFileShareErr error,
) {
	ctrl := gomock.NewController(ginkgo.GinkgoT())
	defer ctrl.Finish()
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}
	computeClientFactory := mock_azclient.NewMockClientFactory(ctrl)
	d.cloud.ComputeClientFactory = computeClientFactory
	mockFileClient := mock_fileshareclient.NewMockInterface(ctrl)
	computeClientFactory.EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, nil).AnyTimes()
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := []*armstorage.AccountKey{
		{Value: &value},
	}

	clientSet := fake.NewSimpleClientset()

	fakeShareQuota := int32(100)
	mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClientForSub(gomock.Any()).Return(mockStorageAccountsClient, nil).AnyTimes()

	d.kubeClient = clientSet
	d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
	mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(key, nil).AnyTimes()
	mockFileClient.EXPECT().Get(context.Background(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &fakeShareQuota}}, mockedFileShareErr).AnyTimes()

	_, err := d.ValidateVolumeCapabilities(context.Background(), req)
	if expectedErr == nil {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	} else {
		gomega.Expect(err).To(gomega.Equal(expectedErr))
	}
},
	ginkgo.Entry("Volume ID missing",
		&csi.ValidateVolumeCapabilitiesRequest{},
		status.Error(codes.InvalidArgument, "Volume ID not provided"),
		nil,
	),
	ginkgo.Entry("Volume capabilities missing",
		&csi.ValidateVolumeCapabilitiesRequest{VolumeId: "vol_1"},
		status.Error(codes.InvalidArgument, "Volume capabilities not provided"),
		nil,
	),
	ginkgo.Entry("Volume ID not valid",
		&csi.ValidateVolumeCapabilitiesRequest{
			VolumeId: "vol_1",
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
		},
		status.Errorf(codes.NotFound, "get account info from(vol_1) failed with error: <nil>"),
		nil,
	),
	ginkgo.Entry("Check file share exists errors",
		&csi.ValidateVolumeCapabilitiesRequest{
			VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#",
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
		},
		status.Errorf(codes.Internal, "error checking if volume(vol_1#f5713de20cde511e8ba4900#fileshare#) exists: test error"),
		fmt.Errorf("test error"),
	),
	ginkgo.Entry("Valid request disk name is empty",
		&csi.ValidateVolumeCapabilitiesRequest{
			VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#",
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
		},
		nil,
		nil,
	),
	ginkgo.Entry("Valid request volume capability is multi node single writer",
		&csi.ValidateVolumeCapabilitiesRequest{
			VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
					},
				},
			},
		},
		nil,
		nil,
	),
	ginkgo.Entry("Valid request",
		&csi.ValidateVolumeCapabilitiesRequest{
			VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
		},
		nil,
		nil,
	),
	ginkgo.Entry("Resource group empty",
		&csi.ValidateVolumeCapabilitiesRequest{
			VolumeId: "vol_1#f5713de20cde511e8ba4900#fileshare#diskname.vhd#",
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
			VolumeContext: map[string]string{
				shareNameField: "sharename",
				diskNameField:  "diskname.vhd",
			},
		},
		nil,
		nil,
	),
)
var _ = ginkgo.Describe("CreateSnapshot", func() {
	var ctrl *gomock.Controller
	var d *Driver
	var mockFileClient *mock_fileshareclient.MockInterface
	ginkgo.BeforeEach(func() {
		ctrl = gomock.NewController(ginkgo.GinkgoT())
		d = NewFakeDriver()
		var err error
		computeClientFactory := mock_azclient.NewMockClientFactory(ctrl)
		mockFileClient = mock_fileshareclient.NewMockInterface(ctrl)
		computeClientFactory.EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, nil).AnyTimes()
		computeClientFactory.EXPECT().GetFileShareClient().Return(mockFileClient).AnyTimes()
		networkClientFactory := mock_azclient.NewMockClientFactory(ctrl)
		networkClientFactory.EXPECT().GetSubnetClient().Return(mock_subnetclient.NewMockInterface(ctrl)).AnyTimes()
		d.cloud, err = storage.NewRepository(config.Config{}, &azclient.Environment{}, computeClientFactory, networkClientFactory)
		gomega.Expect(err).To(gomega.BeNil())

		d.kubeClient = fake.NewSimpleClientset()
	})
	ginkgo.AfterEach(func() {
		ctrl.Finish()
	})
	ginkgo.When("test", func() {
		ginkgo.It("should work", func(_ context.Context) {
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
				{
					desc: "Snapshot already exists",
					req: &csi.CreateSnapshotRequest{
						SourceVolumeId: "rg#f5713de20cde511e8ba4900#fileShareName#diskname.vhd#uuid#namespace#subsID",
						Name:           "snapname",
					},
					expectedErr: nil,
				},
				{
					desc: "Create snapshot success",
					req: &csi.CreateSnapshotRequest{
						SourceVolumeId: "rg#f5713de20cde511e8ba4900#fileShareName#diskname.vhd#uuid#namespace#subsID",
						Name:           "snapname",
					},
					expectedErr: nil,
				},
			}

			for _, test := range tests {
				if test.desc == "Snapshot already exists" {
					mockFileClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]*armstorage.FileShareItem{
						{
							Name: to.Ptr("fileShareName"),
							Properties: &armstorage.FileShareProperties{
								SnapshotTime: to.Ptr(time.Now()),
							},
						},
					}, nil).Times(1)
					mockFileClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{
						Name: to.Ptr("fileShareName"),
						FileShareProperties: &armstorage.FileShareProperties{
							Metadata: map[string]*string{
								snapshotNameKey: to.Ptr("snapname"),
							},
						},
					}, nil).Times(1)
				}
				if test.desc == "Create snapshot success" {
					mockFileClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]*armstorage.FileShareItem{
						{
							Name: to.Ptr("fileShareName"),
							Properties: &armstorage.FileShareProperties{
								SnapshotTime: to.Ptr(time.Now()),
							},
						},
					}, nil).Times(1)
					mockFileClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{
						Name: to.Ptr("fileShareName"),
						FileShareProperties: &armstorage.FileShareProperties{
							ShareQuota: to.Ptr(int32(100)),
						},
					}, nil).Times(2)
					mockFileClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{
						Name: to.Ptr("fileShareName"),
						FileShareProperties: &armstorage.FileShareProperties{
							SnapshotTime: to.Ptr(time.Now()),
							ShareQuota:   to.Ptr(int32(0)),
						},
					}, nil).Times(1)
				}

				_, err := d.CreateSnapshot(context.Background(), test.req)
				if err != nil {
					gomega.Expect(err).To(gomega.Equal(test.expectedErr))
				} else {
					gomega.Expect(err).To(gomega.BeNil())
				}
			}
		})
	})
})
var _ = ginkgo.DescribeTable("DeleteSnapshot", func(req *csi.DeleteSnapshotRequest, expectedErr error) {
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}
	ctrl := gomock.NewController(ginkgo.GinkgoT())
	defer ctrl.Finish()
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := []*armstorage.AccountKey{
		{Value: &value},
	}

	clientSet := fake.NewSimpleClientset()
	mockClientFactory := mock_azclient.NewMockClientFactory(ctrl)
	d.cloud.ComputeClientFactory = mockClientFactory
	mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClient().Return(mockStorageAccountsClient).AnyTimes()

	d.kubeClient = clientSet
	d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
	mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "vol_1").Return(key, nil).AnyTimes()

	mockFileClient := mock_fileshareclient.NewMockInterface(ctrl)
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, nil).AnyTimes()
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetFileShareClient().Return(mockFileClient).AnyTimes()
	mockFileClient.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	_, err := d.DeleteSnapshot(context.Background(), req)
	if expectedErr == nil {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	} else {
		gomega.Expect(err).To(gomega.Equal(expectedErr))
	}
},
	ginkgo.Entry("Snapshot name missing", &csi.DeleteSnapshotRequest{}, status.Error(codes.InvalidArgument, "Snapshot ID must be provided")),
	ginkgo.Entry("Invalid volume ID", &csi.DeleteSnapshotRequest{
		SnapshotId: "vol_1#",
	}, nil,
	),
	ginkgo.Entry("Invalid volume ID for snapshot name", &csi.DeleteSnapshotRequest{
		SnapshotId: "vol_1##",
		Secrets:    map[string]string{},
	}, nil),
	ginkgo.Entry("Invalid Snapshot ID", &csi.DeleteSnapshotRequest{
		SnapshotId: "testrg#testAccount#testFileShare#testuuid",
		Secrets:    map[string]string{"accountName": "TestAccountName", "accountKey": base64.StdEncoding.EncodeToString([]byte("TestAccountKey"))},
	}, status.Error(codes.Internal, "failed to get snapshot name with (testrg#testAccount#testFileShare#testuuid): error parsing volume id: \"testrg#testAccount#testFileShare#testuuid\", should at least contain four #"),
	),
	ginkgo.Entry("Delete snapshot success", &csi.DeleteSnapshotRequest{
		SnapshotId: "rg#f5713de20cde511e8ba4900#fileShareName#diskname.vhd#2019-08-22T07:17:53.0000000Z",
		Secrets:    map[string]string{},
	}, nil),
)

var _ = ginkgo.Describe("TestControllerExpandVolume", func() {
	stdVolSize := int64(5 * 1024 * 1024 * 1024)
	stdCapRange := &csi.CapacityRange{RequiredBytes: stdVolSize}
	var ctrl *gomock.Controller
	var d *Driver
	ginkgo.BeforeEach(func() {
		stdVolSize = int64(5 * 1024 * 1024 * 1024)
		stdCapRange = &csi.CapacityRange{RequiredBytes: stdVolSize}
		ctrl = gomock.NewController(ginkgo.GinkgoT())
		d = NewFakeDriver()
		d.cloud = &storage.AccountRepo{}
		d.cloud.ComputeClientFactory = mock_azclient.NewMockClientFactory(ctrl)
	})
	ginkgo.AfterEach(func() {
		ctrl.Finish()
	})
	ginkgo.When("Volume ID missing", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.ControllerExpandVolumeRequest{}

			expectedErr := status.Error(codes.InvalidArgument, "Volume ID missing in request")
			_, err := d.ControllerExpandVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Volume Capacity range missing", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.ControllerExpandVolumeRequest{
				VolumeId: "vol_1",
			}

			d.Cap = []*csi.ControllerServiceCapability{}

			expectedErr := status.Error(codes.InvalidArgument, "volume capacity range missing in request")
			_, err := d.ControllerExpandVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Invalid Volume ID", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			req := &csi.ControllerExpandVolumeRequest{
				VolumeId:      "vol_1",
				CapacityRange: stdCapRange,
			}

			expectedErr := status.Errorf(codes.InvalidArgument, "GetFileShareInfo(vol_1) failed with error: error parsing volume id: \"vol_1\", should at least contain two #")
			_, err := d.ControllerExpandVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("Disk name not empty", func() {
		ginkgo.It("should fail", func(ctx context.Context) {

			value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
			key := []*armstorage.AccountKey{
				{Value: &value},
			}
			clientSet := fake.NewSimpleClientset()
			req := &csi.ControllerExpandVolumeRequest{
				VolumeId:      "vol_1#f5713de20cde511e8ba4900#filename#diskname.vhd#",
				CapacityRange: stdCapRange,
			}

			mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
			d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClient().Return(mockStorageAccountsClient).AnyTimes()

			d.kubeClient = clientSet
			d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
			mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "vol_1").Return(key, nil).AnyTimes()

			expectErr := status.Error(codes.Unimplemented, "vhd disk volume(vol_1#f5713de20cde511e8ba4900#filename#diskname.vhd#, diskName:diskname.vhd) is not supported on ControllerExpandVolume")
			_, err := d.ControllerExpandVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectErr))
		})
	})
	ginkgo.When("Resize file share returns error", func() {
		ginkgo.It("should fail", func(ctx context.Context) {

			value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
			key := []*armstorage.AccountKey{
				{Value: &value},
			}

			clientSet := fake.NewSimpleClientset()
			req := &csi.ControllerExpandVolumeRequest{
				VolumeId:      "vol_1#f5713de20cde511e8ba4900#filename#",
				CapacityRange: stdCapRange,
			}
			mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
			d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClient().Return(mockStorageAccountsClient).AnyTimes()
			d.kubeClient = clientSet
			d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
			mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "vol_1").Return(key, nil).AnyTimes()
			mockFileClient := mock_fileshareclient.NewMockInterface(ctrl)
			d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, nil).AnyTimes()
			mockFileClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("test error")).AnyTimes()
			mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{
				FileShareProperties: &armstorage.FileShareProperties{},
			}, nil).AnyTimes()

			expectErr := status.Errorf(codes.Internal, "expand volume error: test error")
			_, err := d.ControllerExpandVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectErr))
		})
	})
	ginkgo.When("get account info failed", func() {
		ginkgo.It("should fail", func(ctx context.Context) {

			d.cloud = &storage.AccountRepo{
				Config: config.Config{
					ResourceGroup: "vol_2",
				},
				ComputeClientFactory: mock_azclient.NewMockClientFactory(ctrl),
			}
			d.dataPlaneAPIAccountCache, _ = azcache.NewTimedCache(10*time.Minute, func(_ context.Context, _ string) (interface{}, error) { return nil, nil }, false)
			d.dataPlaneAPIAccountCache.Set("f5713de20cde511e8ba4900", "1")

			value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
			key := []*armstorage.AccountKey{
				{Value: &value},
			}

			clientSet := fake.NewSimpleClientset()
			req := &csi.ControllerExpandVolumeRequest{
				VolumeId:      "#f5713de20cde511e8ba4900#filename##secret",
				CapacityRange: stdCapRange,
			}

			mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
			d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClientForSub(gomock.Any()).Return(mockStorageAccountsClient, nil).AnyTimes()
			d.kubeClient = clientSet
			d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
			mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "f5713de20cde511e8ba4900").Return(key, &azcore.ResponseError{StatusCode: http.StatusBadGateway, ErrorCode: cloudprovider.InstanceNotFound.Error()}).AnyTimes()

			expectErr := status.Error(codes.NotFound, `get account info from(#f5713de20cde511e8ba4900#filename##secret) failed with error: Missing RawResponse
--------------------------------------------------------------------------------
ERROR CODE: instance not found
--------------------------------------------------------------------------------
`)
			_, err := d.ControllerExpandVolume(ctx, req)
			gomega.Expect(err).To(gomega.Equal(expectErr))

		})
	})
	ginkgo.When("Valid request", func() {
		ginkgo.It("should fail", func(ctx context.Context) {

			value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
			key := []*armstorage.AccountKey{
				{Value: &value},
			}
			clientSet := fake.NewSimpleClientset()
			req := &csi.ControllerExpandVolumeRequest{
				VolumeId:      "capz-d18sqm#f25f6e46c62274a4a8e433a#pvc-66ced8fb-a027-4eb6-87ca-e720ff36f683#pvc-66ced8fb-a027-4eb6-87ca-e720ff36f683#azurefile-2546",
				CapacityRange: stdCapRange,
			}

			mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
			d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClient().Return(mockStorageAccountsClient).AnyTimes()
			d.kubeClient = clientSet
			d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
			mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "capz-d18sqm").Return(key, nil).AnyTimes()
			mockFileClient := mock_fileshareclient.NewMockInterface(ctrl)
			mockFileClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			shareQuota := int32(0)
			mockFileClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &shareQuota}}, nil).AnyTimes()
			d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, nil).AnyTimes()

			expectedResp := &csi.ControllerExpandVolumeResponse{CapacityBytes: stdVolSize}
			resp, err := d.ControllerExpandVolume(ctx, req)
			if !(reflect.DeepEqual(err, nil) && reflect.DeepEqual(resp, expectedResp)) {
				ginkgo.GinkgoT().Errorf("Expected response: %v received response: %v expected error: %v received error: %v", expectedResp, resp, nil, err)
			}
		})
	})
})

var _ = ginkgo.DescribeTable("GetShareURL", func(sourceVolumeID string, expectedErr error) {

	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}
	d.cloud.ComputeClientFactory = mock_azclient.NewMockClientFactory(gomock.NewController(ginkgo.GinkgoT()))
	d.cloud.NetworkClientFactory = mock_azclient.NewMockClientFactory(gomock.NewController(ginkgo.GinkgoT()))
	validSecret := map[string]string{}

	ctrl := gomock.NewController(ginkgo.GinkgoT())

	defer ctrl.Finish()
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := []*armstorage.AccountKey{
		{Value: &value},
	}

	clientSet := fake.NewSimpleClientset()

	mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClientForSub(gomock.Any()).Return(mockStorageAccountsClient, nil).AnyTimes()

	d.kubeClient = clientSet
	d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
	mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "rg", gomock.Any()).Return(key, nil).AnyTimes()
	_, err := d.getShareURL(context.Background(), sourceVolumeID, validSecret)
	if expectedErr == nil {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	} else {
		gomega.Expect(err).To(gomega.Equal(expectedErr))
	}

},
	ginkgo.Entry("Volume ID error",
		"vol_1",
		fmt.Errorf("failed to get file share from vol_1"),
	),
	ginkgo.Entry("Volume ID error2",
		"vol_1###",
		fmt.Errorf("failed to get file share from vol_1###"),
	),
	ginkgo.Entry("Valid request",
		"rg#accountname#fileshare#",
		nil,
	),
)

var _ = ginkgo.DescribeTable("GetServiceURL", func(sourceVolumeID string, key []*armstorage.AccountKey, expectedErr error) {
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}
	validSecret := map[string]string{}

	ctrl := gomock.NewController(ginkgo.GinkgoT())
	defer ctrl.Finish()

	clientSet := fake.NewSimpleClientset()
	mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
	mockClientFactory := mock_azclient.NewMockClientFactory(ctrl)
	d.cloud.ComputeClientFactory = mockClientFactory
	d.cloud.NetworkClientFactory = mockClientFactory
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClient().Return(mockStorageAccountsClient).AnyTimes()
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClientForSub(gomock.Any()).Return(mockStorageAccountsClient, nil).AnyTimes()

	d.kubeClient = clientSet
	d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
	mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(key, nil).AnyTimes()

	_, _, err := d.getServiceURL(context.Background(), sourceVolumeID, validSecret)
	if expectedErr == nil {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	} else {
		gomega.Expect(err).To(gomega.Equal(expectedErr))
	}
},
	ginkgo.Entry("Invalid volume ID", "vol_1", []*armstorage.AccountKey{
		{Value: to.Ptr(base64.StdEncoding.EncodeToString([]byte("acc_key")))},
	}, nil),
	ginkgo.Entry("Invalid Key",
		"vol_1##",
		[]*armstorage.AccountKey{
			{Value: to.Ptr("acc_key")},
		},
		nil,
	),
	ginkgo.Entry("Valid call",
		"vol_1##",
		[]*armstorage.AccountKey{
			{Value: to.Ptr(base64.StdEncoding.EncodeToString([]byte("acc_key")))},
		},
		nil,
	),
)

var _ = ginkgo.DescribeTable("SnapshotExists", func(sourceVolumeID string,
	key []*armstorage.AccountKey,
	secret map[string]string,
	expectedErr error) {
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}

	ctrl := gomock.NewController(ginkgo.GinkgoT())
	defer ctrl.Finish()

	clientSet := fake.NewSimpleClientset()
	mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
	clientFactory := mock_azclient.NewMockClientFactory(ctrl)
	d.cloud.ComputeClientFactory = clientFactory
	d.cloud.NetworkClientFactory = clientFactory
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClient().Return(mockStorageAccountsClient).AnyTimes()

	d.kubeClient = clientSet
	d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
	mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), "").Return(key, nil).AnyTimes()

	_, _, _, _, err := d.snapshotExists(context.Background(), sourceVolumeID, "sname", secret, false)
	gomega.Expect(err).To(gomega.Equal(expectedErr))
},
	ginkgo.Entry("Invalid volume ID with data plane api",
		"vol_1",
		[]*armstorage.AccountKey{
			{Value: to.Ptr(base64.StdEncoding.EncodeToString([]byte("acc_key")))},
		},
		map[string]string{"accountName": "TestAccountName", "accountKey": base64.StdEncoding.EncodeToString([]byte("TestAccountKey"))},
		fmt.Errorf("file share is empty after parsing sourceVolumeID: vol_1"),
	),
	ginkgo.Entry("Invalid volume ID with management api",
		"vol_1",
		[]*armstorage.AccountKey{
			{Value: to.Ptr(base64.StdEncoding.EncodeToString([]byte("acc_key")))},
		},
		map[string]string{},
		fmt.Errorf("error parsing volume id: %q, should at least contain two #", "vol_1"),
	))

var _ = ginkgo.Describe("GetCapacity", func() {
	ginkgo.When("test", func() {
		ginkgo.It("should work", func(_ context.Context) {

			d := NewFakeDriver()
			req := csi.GetCapacityRequest{}
			resp, err := d.GetCapacity(context.Background(), &req)
			gomega.Expect(resp).To(gomega.BeNil())
			gomega.Expect(err).To(gomega.Equal(status.Error(codes.Unimplemented, "")))
		})
	})
})

var _ = ginkgo.Describe("ListVolumes", func() {
	ginkgo.When("test", func() {
		ginkgo.It("should work", func(_ context.Context) {
			d := NewFakeDriver()
			req := csi.ListVolumesRequest{}
			resp, err := d.ListVolumes(context.Background(), &req)
			gomega.Expect(resp).To(gomega.BeNil())
			gomega.Expect(err).To(gomega.Equal(status.Error(codes.Unimplemented, "")))
		})
	})
})

var _ = ginkgo.Describe("ListSnapshots", func() {
	ginkgo.When("test", func() {
		ginkgo.It("should work", func(_ context.Context) {
			d := NewFakeDriver()
			req := csi.ListSnapshotsRequest{}
			resp, err := d.ListSnapshots(context.Background(), &req)
			gomega.Expect(resp).To(gomega.BeNil())
			gomega.Expect(err).To(gomega.Equal(status.Error(codes.Unimplemented, "")))
		})
	})
})

var _ = ginkgo.Describe("SetAzureCredentials", func() {
	ginkgo.When("test", func() {
		ginkgo.It("should work", func(_ context.Context) {
			d := NewFakeDriver()
			d.cloud = &storage.AccountRepo{
				Config: config.Config{
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
				d.kubeClient = test.kubeClient
				result, err := d.SetAzureCredentials(context.Background(), test.accountName, test.accountKey, test.secretName, test.secretNamespace)
				gomega.Expect(result).To(gomega.Equal(test.expectedName))
				if test.expectedErr == nil {
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
				} else {
					gomega.Expect(err).To(gomega.Equal(test.expectedErr))
				}
			}
		})
	})
})

var _ = ginkgo.Describe("GenerateSASToken", func() {
	ginkgo.When("test", func() {
		ginkgo.It("should work", func(_ context.Context) {
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
					expectedErr: status.Errorf(codes.Internal, "failed to generate sas token in creating new shared key credential, accountName: %s, err: %s", "unit-test", "decode account key: illegal base64 data at input byte 8"),
				},
			}
			for _, tt := range tests {
				sas, err := d.generateSASToken(context.Background(), tt.accountName, tt.accountKey, storageEndpointSuffix, 30)
				if tt.expectedErr == nil {
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
				} else {
					gomega.Expect(err).To(gomega.Equal(tt.expectedErr))
				}
				gomega.Expect(sas).To(gomega.ContainSubstring(tt.want))
			}
		})
	})
})

var _ = ginkgo.Describe("TestAuthorizeAzcopyWithIdentity", func() {
	var d *Driver
	ginkgo.BeforeEach(func() {
		d = NewFakeDriver()
	})
	ginkgo.When("use service principal to authorize azcopy", func() {
		ginkgo.It("should fail", func(_ context.Context) {
			d.cloud = &storage.AccountRepo{
				Config: config.Config{
					AzureClientConfig: config.AzureClientConfig{
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
			authAzcopyEnv, err := d.authorizeAzcopyWithIdentity()
			if !reflect.DeepEqual(authAzcopyEnv, expectedAuthAzcopyEnv) || err != nil {
				ginkgo.GinkgoT().Errorf("Unexpected authAzcopyEnv: %v, Unexpected error: %v", authAzcopyEnv, err)
			}
		})
	})
	ginkgo.When("use service principal to authorize azcopy but client id is empty", func() {
		ginkgo.It("should fail", func(_ context.Context) {
			d.cloud = &storage.AccountRepo{
				Config: config.Config{
					AzureClientConfig: config.AzureClientConfig{
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
			gomega.Expect(authAzcopyEnv).To(gomega.Equal(expectedAuthAzcopyEnv))
			gomega.Expect(err).To(gomega.Equal(expectedErr))
		})
	})
	ginkgo.When("use user assigned managed identity to authorize azcopy", func() {
		ginkgo.It("should fail", func(_ context.Context) {
			d.cloud = &storage.AccountRepo{
				Config: config.Config{
					AzureClientConfig: config.AzureClientConfig{
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
			var expected error
			authAzcopyEnv, err := d.authorizeAzcopyWithIdentity()
			if !reflect.DeepEqual(authAzcopyEnv, expectedAuthAzcopyEnv) || !reflect.DeepEqual(err, expected) {
				ginkgo.GinkgoT().Errorf("Unexpected authAzcopyEnv: %v, Unexpected error: %v", authAzcopyEnv, err)
			}
		})
	})
	ginkgo.When("use system assigned managed identity to authorize azcopy", func() {
		ginkgo.It("should fail", func(_ context.Context) {
			d.cloud = &storage.AccountRepo{
				Config: config.Config{
					AzureClientConfig: auth.AzureClientConfig{
						AzureAuthConfig: azclient.AzureAuthConfig{
							UseManagedIdentityExtension: true,
						},
					},
				},
			}
			expectedAuthAzcopyEnv := []string{
				fmt.Sprintf(azcopyAutoLoginType + "=MSI"),
			}
			var expected error
			authAzcopyEnv, err := d.authorizeAzcopyWithIdentity()
			if !reflect.DeepEqual(authAzcopyEnv, expectedAuthAzcopyEnv) || !reflect.DeepEqual(err, expected) {

				ginkgo.GinkgoT().Errorf("Unexpected authAzcopyEnv: %v, Unexpected error: %v", authAzcopyEnv, err)
			}
		})
	})
	ginkgo.When("AADClientSecret be nil and useManagedIdentityExtension is false", func() {
		ginkgo.It("should fail", func(_ context.Context) {

			d.cloud = &storage.AccountRepo{
				Config: config.Config{
					AzureClientConfig: config.AzureClientConfig{},
				},
			}
			expectedAuthAzcopyEnv := []string{}
			expected := fmt.Errorf("neither the service principal nor the managed identity has been set")
			authAzcopyEnv, err := d.authorizeAzcopyWithIdentity()
			if !reflect.DeepEqual(authAzcopyEnv, expectedAuthAzcopyEnv) || !reflect.DeepEqual(err, expected) {
				ginkgo.GinkgoT().Errorf("Unexpected authAzcopyEnv: %v, Unexpected error: %v", authAzcopyEnv, err)
			}
		})
	})

})

var _ = ginkgo.Describe("TestGetAzcopyAuth", func() {
	var d *Driver
	ginkgo.BeforeEach(func() {
		d = NewFakeDriver()
	})
	ginkgo.When("failed to get accountKey in secrets", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			d.cloud = &storage.AccountRepo{
				Config: config.Config{},
			}
			secrets := map[string]string{
				defaultSecretAccountName: "accountName",
			}

			expectedAccountSASToken := ""
			expectedErr := fmt.Errorf("could not find accountkey or azurestorageaccountkey field in secrets")
			accountSASToken, authAzcopyEnv, err := d.getAzcopyAuth(ctx, "accountName", "", "core.windows.net", &storage.AccountOptions{}, secrets, "secretsName", "secretsNamespace", false)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
			gomega.Expect(authAzcopyEnv).To(gomega.BeNil())
			gomega.Expect(accountSASToken).To(gomega.Equal(expectedAccountSASToken))
		})
	})
	ginkgo.When("generate SAS token failed for illegal account key", func() {
		ginkgo.It("should fail", func(ctx context.Context) {
			d.cloud = &storage.AccountRepo{
				Config: config.Config{},
			}
			secrets := map[string]string{
				defaultSecretAccountName: "accountName",
				defaultSecretAccountKey:  "fakeValue",
			}

			expectedAccountSASToken := ""
			expectedErr := status.Errorf(codes.Internal, "failed to generate sas token in creating new shared key credential, accountName: %s, err: %s", "accountName", "decode account key: illegal base64 data at input byte 8")
			accountSASToken, authAzcopyEnv, err := d.getAzcopyAuth(ctx, "accountName", "", "core.windows.net", &storage.AccountOptions{}, secrets, "secretsName", "secretsNamespace", false)
			gomega.Expect(err).To(gomega.Equal(expectedErr))
			gomega.Expect(authAzcopyEnv).To(gomega.BeNil())
			gomega.Expect(accountSASToken).To(gomega.Equal(expectedAccountSASToken))
		})
	})
})
