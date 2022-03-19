/*
Copyright 2017 The Kubernetes Authors.

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
	"net/url"
	"strconv"
	"strings"

	volumehelper "sigs.k8s.io/azurefile-csi-driver/pkg/util"

	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2021-02-01/storage"
	"github.com/Azure/azure-storage-file-go/azfile"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/pborman/uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient"
	azcache "sigs.k8s.io/cloud-provider-azure/pkg/cache"
	"sigs.k8s.io/cloud-provider-azure/pkg/metrics"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
)

const (
	azureFileCSIDriverName = "azurefile_csi_driver"
	privateEndpoint        = "privateendpoint"
)

var (
	volumeCaps = []csi.VolumeCapability_AccessMode{
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		},
	}
)

// CreateVolume provisions an azure file
func (d *Driver) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if err := d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		klog.Errorf("invalid create volume req: %v", req)
		return nil, err
	}

	volName := req.GetName()
	if len(volName) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume Name must be provided")
	}
	volumeCapabilities := req.GetVolumeCapabilities()
	if err := isValidVolumeCapabilities(volumeCapabilities); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("CreateVolume Volume capabilities not valid: %v", err))
	}

	capacityBytes := req.GetCapacityRange().GetRequiredBytes()
	requestGiB := volumehelper.RoundUpGiB(capacityBytes)
	if requestGiB == 0 {
		requestGiB = defaultAzureFileQuota
		klog.Warningf("no quota specified, set as default value(%d GiB)", defaultAzureFileQuota)
	}

	if acquired := d.volumeLocks.TryAcquire(volName); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volName)
	}
	defer d.volumeLocks.Release(volName)

	parameters := req.GetParameters()
	if parameters == nil {
		parameters = make(map[string]string)
	}
	var sku, resourceGroup, location, account, fileShareName, diskName, fsType, secretName string
	var secretNamespace, protocol, customTags, storageEndpointSuffix, networkEndpointType, accessTier, rootSquashType string
	var createAccount, useDataPlaneAPI, useSeretCache, disableDeleteRetentionPolicy, enableLFS bool
	var vnetResourceGroup, vnetName, subnetName string
	// set allowBlobPublicAccess as false by default
	allowBlobPublicAccess := to.BoolPtr(false)

	// store account key to k8s secret by default
	storeAccountKey := true

	// Apply ProvisionerParameters (case-insensitive). We leave validation of
	// the values to the cloud provider.
	for k, v := range parameters {
		switch strings.ToLower(k) {
		case skuNameField:
			sku = v
		case storageAccountTypeField:
			sku = v
		case locationField:
			location = v
		case storageAccountField:
			account = v
		case resourceGroupField:
			resourceGroup = v
		case shareNameField:
			fileShareName = v
		case diskNameField:
			diskName = v
		case fsTypeField:
			fsType = v
		case storeAccountKeyField:
			if strings.EqualFold(v, falseValue) {
				storeAccountKey = false
			}
		case secretNameField:
			secretName = v
		case secretNamespaceField:
			secretNamespace = v
		case protocolField:
			protocol = v
		case tagsField:
			customTags = v
		case createAccountField:
			createAccount = strings.EqualFold(v, trueValue)
		case useSecretCacheField:
			useSeretCache = strings.EqualFold(v, trueValue)
		case enableLargeFileSharesField:
			enableLFS = strings.EqualFold(v, trueValue)
		case useDataPlaneAPIField:
			useDataPlaneAPI = strings.EqualFold(v, trueValue)
		case disableDeleteRetentionPolicyField:
			disableDeleteRetentionPolicy = strings.EqualFold(v, trueValue)
		case pvcNamespaceKey:
			if secretNamespace == "" {
				// respect `secretNamespace` field as first priority
				secretNamespace = v
			}
		case storageEndpointSuffixField:
			storageEndpointSuffix = v
		case networkEndpointTypeField:
			networkEndpointType = v
		case accessTierField:
			accessTier = v
		case rootSquashTypeField:
			rootSquashType = v
		case allowBlobPublicAccessField:
			if strings.EqualFold(v, trueValue) {
				allowBlobPublicAccess = to.BoolPtr(true)
			}
		case pvcNameKey:
			// no op
		case pvNameKey:
			// no op
		case serverNameField:
			// no op, only used in NodeStageVolume
		case folderNameField:
			// no op, only used in NodeStageVolume
		case mountPermissionsField:
			// only do validations here, used in NodeStageVolume, NodePublishVolume
			if v != "" {
				if _, err := strconv.ParseUint(v, 8, 32); err != nil {
					return nil, status.Errorf(codes.InvalidArgument, fmt.Sprintf("invalid mountPermissions %s in storage class", v))
				}
			}
		case vnetResourceGroupField:
			vnetResourceGroup = v
		case vnetNameField:
			vnetName = v
		case subnetNameField:
			subnetName = v
		default:
			return nil, status.Errorf(codes.InvalidArgument, fmt.Sprintf("invalid parameter %q in storage class", k))
		}
	}

	if !isSupportedFsType(fsType) {
		return nil, status.Errorf(codes.InvalidArgument, "fsType(%s) is not supported, supported fsType list: %v", fsType, supportedFsTypeList)
	}

	if !isSupportedProtocol(protocol) {
		return nil, status.Errorf(codes.InvalidArgument, "protocol(%s) is not supported, supported protocol list: %v", protocol, supportedProtocolList)
	}

	if !isSupportedAccessTier(accessTier) {
		return nil, status.Errorf(codes.InvalidArgument, "accessTier(%s) is not supported, supported AccessTier list: %v", accessTier, storage.PossibleShareAccessTierValues())
	}

	if !isSupportedRootSquashType(rootSquashType) {
		return nil, status.Errorf(codes.InvalidArgument, "rootSquashType(%s) is not supported, supported RootSquashType list: %v", rootSquashType, storage.PossibleRootSquashTypeValues())
	}

	if protocol == nfs && fsType != "" && fsType != nfs {
		return nil, status.Errorf(codes.InvalidArgument, "fsType(%s) is not supported with protocol(%s)", fsType, protocol)
	}

	enableHTTPSTrafficOnly := true
	shareProtocol := storage.EnabledProtocolsSMB
	createPrivateEndpoint := false
	if strings.EqualFold(networkEndpointType, privateEndpoint) {
		createPrivateEndpoint = true
	}
	var vnetResourceIDs []string
	if fsType == nfs || protocol == nfs {
		protocol = nfs
		enableHTTPSTrafficOnly = false
		if !strings.HasPrefix(strings.ToLower(sku), premium) {
			// NFS protocol only supports Premium storage
			sku = string(storage.SkuNamePremiumLRS)
		}
		shareProtocol = storage.EnabledProtocolsNFS
		// NFS protocol does not need account key
		storeAccountKey = false
		// reset protocol field (compatble with "fsType: nfs")
		parameters[protocolField] = protocol

		if !createPrivateEndpoint {
			// set VirtualNetworkResourceIDs for storage account firewall setting
			vnetResourceID := d.getSubnetResourceID()
			klog.V(2).Infof("set vnetResourceID(%s) for NFS protocol", vnetResourceID)
			vnetResourceIDs = []string{vnetResourceID}
			if account == "" {
				if err := d.updateSubnetServiceEndpoints(ctx, vnetResourceGroup, vnetName, subnetName); err != nil {
					return nil, status.Errorf(codes.Internal, "update service endpoints failed with error: %v", err)
				}
			}
		}
	}

	fileShareSize := int(requestGiB)
	// account kind should be FileStorage for Premium File
	accountKind := string(storage.KindStorageV2)
	if strings.HasPrefix(strings.ToLower(sku), premium) {
		accountKind = string(storage.KindFileStorage)
		if fileShareSize < minimumPremiumShareSize {
			fileShareSize = minimumPremiumShareSize
		}
	}

	validFileShareName := fileShareName
	if validFileShareName == "" {
		name := volName
		if protocol == nfs {
			// use "pvcn" prefix for nfs protocol file share
			name = strings.Replace(name, "pvc", "pvcn", 1)
		} else if isDiskFsType(fsType) {
			// use "pvcd" prefix for vhd disk file share
			name = strings.Replace(name, "pvc", "pvcd", 1)
		}
		validFileShareName = getValidFileShareName(name)
	}

	if resourceGroup == "" {
		resourceGroup = d.cloud.ResourceGroup
	}

	tags, err := ConvertTagsToMap(customTags)
	if err != nil {
		return nil, err
	}

	accountOptions := &azure.AccountOptions{
		Name:                                    account,
		Type:                                    sku,
		Kind:                                    accountKind,
		ResourceGroup:                           resourceGroup,
		Location:                                location,
		EnableHTTPSTrafficOnly:                  enableHTTPSTrafficOnly,
		Tags:                                    tags,
		VirtualNetworkResourceIDs:               vnetResourceIDs,
		CreateAccount:                           createAccount,
		CreatePrivateEndpoint:                   createPrivateEndpoint,
		EnableLargeFileShare:                    enableLFS,
		DisableFileServiceDeleteRetentionPolicy: disableDeleteRetentionPolicy,
		AllowBlobPublicAccess:                   allowBlobPublicAccess,
		VNetResourceGroup:                       vnetResourceGroup,
		VNetName:                                vnetName,
		SubnetName:                              subnetName,
	}

	var accountKey, lockKey string
	accountName := account
	if len(req.GetSecrets()) == 0 && accountName == "" {
		if v, ok := d.volMap.Load(volName); ok {
			accountName = v.(string)
		} else {
			lockKey = fmt.Sprintf("%s%s%s%s%s%v", sku, accountKind, resourceGroup, location, protocol, createPrivateEndpoint)
			// search in cache first
			cache, err := d.accountSearchCache.Get(lockKey, azcache.CacheReadTypeDefault)
			if err != nil {
				return nil, err
			}
			if cache != nil {
				accountName = cache.(string)
			} else {
				d.volLockMap.LockEntry(lockKey)
				err = wait.ExponentialBackoff(d.cloud.RequestBackoff(), func() (bool, error) {
					var retErr error
					accountName, accountKey, retErr = d.cloud.EnsureStorageAccount(ctx, accountOptions, fileShareAccountNamePrefix)
					if isRetriableError(retErr) {
						klog.Warningf("EnsureStorageAccount(%s) failed with error(%v), waiting for retrying", account, retErr)
						sleepIfThrottled(retErr, accountOpThrottlingSleepSec)
						return false, nil
					}
					return true, retErr
				})
				d.volLockMap.UnlockEntry(lockKey)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "failed to ensure storage account: %v", err)
				}
				d.accountSearchCache.Set(lockKey, accountName)
				d.volMap.Store(volName, accountName)
				if accountKey != "" {
					d.accountCacheMap.Set(accountName, accountKey)
				}
			}
		}
	}

	if strings.TrimSpace(storageEndpointSuffix) == "" {
		if d.cloud.Environment.StorageEndpointSuffix != "" {
			storageEndpointSuffix = d.cloud.Environment.StorageEndpointSuffix
		} else {
			storageEndpointSuffix = defaultStorageEndPointSuffix
		}
	}
	if d.fileClient != nil {
		d.fileClient.StorageEndpointSuffix = storageEndpointSuffix
	}
	if createPrivateEndpoint {
		parameters[serverNameField] = fmt.Sprintf("%s.privatelink.file.%s", accountName, storageEndpointSuffix)
	}

	accountOptions.Name = accountName
	secret := req.GetSecrets()
	if len(secret) == 0 && useDataPlaneAPI {
		if accountKey == "" {
			if accountKey, err = d.GetStorageAccesskey(ctx, accountOptions, secret, secretName, secretNamespace); err != nil {
				return nil, fmt.Errorf("failed to GetStorageAccesskey on account(%s) rg(%s), error: %v", accountOptions.Name, accountOptions.ResourceGroup, err)
			}
		}
		secret = createStorageAccountSecret(accountName, accountKey)
		// skip validating file share quota if useDataPlaneAPI
	} else {
		if quota, err := d.getFileShareQuota(resourceGroup, accountName, validFileShareName, secret); err != nil {
			return nil, status.Errorf(codes.Internal, err.Error())
		} else if quota != -1 && quota != fileShareSize {
			return nil, status.Errorf(codes.AlreadyExists, "request file share(%s) already exists, but its capacity(%v) is different from (%v)", validFileShareName, quota, fileShareSize)
		}
	}

	shareOptions := &fileclient.ShareOptions{
		Name:       validFileShareName,
		Protocol:   shareProtocol,
		RequestGiB: fileShareSize,
		AccessTier: accessTier,
		RootSquash: rootSquashType,
	}

	var volumeID string
	mc := metrics.NewMetricContext(azureFileCSIDriverName, "controller_create_volume", d.cloud.ResourceGroup, d.cloud.SubscriptionID, d.Name)
	isOperationSucceeded := false
	defer func() {
		mc.ObserveOperationWithResult(isOperationSucceeded, volumeID, "")
	}()

	klog.V(2).Infof("begin to create file share(%s) on account(%s) type(%s) rg(%s) location(%s) size(%d) protocol(%s)", validFileShareName, accountName, sku, resourceGroup, location, fileShareSize, shareProtocol)
	if err := d.CreateFileShare(accountOptions, shareOptions, secret); err != nil {
		if strings.Contains(err.Error(), accountLimitExceedManagementAPI) || strings.Contains(err.Error(), accountLimitExceedDataPlaneAPI) {
			klog.Warningf("create file share(%s) on account(%s) type(%s) rg(%s) location(%s) size(%d), error: %v, skip matching current account", validFileShareName, account, sku, resourceGroup, location, fileShareSize, err)
			tags := map[string]*string{
				azure.SkipMatchingTag: to.StringPtr(""),
			}
			if rerr := d.cloud.AddStorageAccountTags(ctx, resourceGroup, accountName, tags); rerr != nil {
				klog.Warningf("AddStorageAccountTags(%v) on account(%s) rg(%s) failed with error: %v", tags, accountName, resourceGroup, rerr.Error())
			}
			// release volume lock first to prevent deadlock
			d.volumeLocks.Release(volName)
			// clean search cache
			if err := d.accountSearchCache.Delete(lockKey); err != nil {
				return nil, err
			}
			// remove the volName from the volMap to stop it matching the same storage account
			d.volMap.Delete(volName)
			return d.CreateVolume(ctx, req)
		}
		return nil, fmt.Errorf("failed to create file share(%s) on account(%s) type(%s) rg(%s) location(%s) size(%d), error: %v", validFileShareName, account, sku, resourceGroup, location, fileShareSize, err)
	}
	klog.V(2).Infof("create file share %s on storage account %s successfully", validFileShareName, accountName)

	if isDiskFsType(fsType) && diskName == "" {
		if accountKey == "" {
			if accountKey, err = d.GetStorageAccesskey(ctx, accountOptions, req.GetSecrets(), secretName, secretNamespace); err != nil {
				return nil, fmt.Errorf("failed to GetStorageAccesskey on account(%s) rg(%s), error: %v", accountOptions.Name, accountOptions.ResourceGroup, err)
			}
		}
		if fileShareName == "" {
			// use pvc name as vhd disk name if file share not specified
			diskName = validFileShareName + vhdSuffix
		} else {
			// use uuid as vhd disk name if file share specified
			diskName = uuid.NewUUID().String() + vhdSuffix
		}
		diskSizeBytes := volumehelper.GiBToBytes(requestGiB)
		klog.V(2).Infof("begin to create vhd file(%s) size(%d) on share(%s) on account(%s) type(%s) rg(%s) location(%s)",
			diskName, diskSizeBytes, validFileShareName, account, sku, resourceGroup, location)
		if err := createDisk(ctx, accountName, accountKey, d.cloud.Environment.StorageEndpointSuffix, validFileShareName, diskName, diskSizeBytes); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create VHD disk: %v", err)
		}
		klog.V(2).Infof("create vhd file(%s) size(%d) on share(%s) on account(%s) type(%s) rg(%s) location(%s) successfully",
			diskName, diskSizeBytes, validFileShareName, account, sku, resourceGroup, location)
		parameters[diskNameField] = diskName
	}

	if storeAccountKey && len(req.GetSecrets()) == 0 {
		secretCacheKey := accountName + secretName + secretNamespace
		if useSeretCache {
			cache, err := d.secretCacheMap.Get(secretCacheKey, azcache.CacheReadTypeDefault)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "get cache key(%s) failed with %v", secretCacheKey, err)
			}
			useSeretCache = (cache != nil)
		}
		if !useSeretCache {
			if accountKey == "" {
				if accountKey, err = d.GetStorageAccesskey(ctx, accountOptions, req.GetSecrets(), secretName, secretNamespace); err != nil {
					return nil, fmt.Errorf("failed to GetStorageAccesskey on account(%s) rg(%s), error: %v", accountOptions.Name, accountOptions.ResourceGroup, err)
				}
			}
			storeSecretName, err := d.SetAzureCredentials(ctx, accountName, accountKey, secretName, secretNamespace)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to store storage account key: %v", err)
			}
			if storeSecretName != "" {
				klog.V(2).Infof("store account key to k8s secret(%v) in %s namespace", storeSecretName, secretNamespace)
			}
			d.secretCacheMap.Set(secretCacheKey, "")
		}
	}

	volumeID = fmt.Sprintf(volumeIDTemplate, resourceGroup, accountName, validFileShareName, diskName)
	if fileShareName != "" {
		// add volume name as suffix to differentiate volumeID since "shareName" is specified
		// not necessary for dynamic file share name creation since volumeID already contains volume name
		volumeID = volumeID + "#" + volName
	}

	if useDataPlaneAPI {
		d.dataPlaneAPIVolMap.Store(volumeID, "")
		d.dataPlaneAPIVolMap.Store(accountName, "")
	}

	isOperationSucceeded = true

	// reset secretNamespace field in VolumeContext
	parameters[secretNamespaceField] = secretNamespace
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volumeID,
			CapacityBytes: capacityBytes,
			VolumeContext: parameters,
		},
	}, nil
}

// DeleteVolume delete an azure file
func (d *Driver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	if err := d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid delete volume request: %v", req)
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	resourceGroupName, accountName, fileShareName, _, err := GetFileShareInfo(volumeID)
	if err != nil {
		// According to CSI Driver Sanity Tester, should succeed when an invalid volume id is used
		klog.Errorf("GetFileShareInfo(%s) in DeleteVolume failed with error: %v", volumeID, err)
		return &csi.DeleteVolumeResponse{}, nil
	}

	if resourceGroupName == "" {
		resourceGroupName = d.cloud.ResourceGroup
	}

	secret := req.GetSecrets()
	if len(secret) == 0 {
		// use data plane api, get account key first
		if d.useDataPlaneAPI(volumeID, accountName) {
			_, _, accountKey, _, _, err := d.GetAccountInfo(ctx, volumeID, req.GetSecrets(), map[string]string{})
			if err != nil {
				return nil, status.Errorf(codes.NotFound, "get account info from(%s) failed with error: %v", volumeID, err)
			}
			secret = createStorageAccountSecret(accountName, accountKey)
		}
	}

	mc := metrics.NewMetricContext(azureFileCSIDriverName, "controller_delete_volume", d.cloud.ResourceGroup, d.cloud.SubscriptionID, d.Name)
	isOperationSucceeded := false
	defer func() {
		mc.ObserveOperationWithResult(isOperationSucceeded, volumeID, "")
	}()

	if err := d.DeleteFileShare(resourceGroupName, accountName, fileShareName, secret); err != nil {
		return nil, status.Errorf(codes.Internal, "DeleteFileShare %s under account(%s) rg(%s) failed with error: %v", fileShareName, accountName, resourceGroupName, err)
	}
	klog.V(2).Infof("azure file(%s) under rg(%s) account(%s) volume(%s) is deleted successfully", fileShareName, resourceGroupName, accountName, volumeID)
	if err := d.RemoveStorageAccountTag(ctx, resourceGroupName, accountName, azure.SkipMatchingTag); err != nil {
		klog.Warningf("RemoveStorageAccountTag(%s) under rg(%s) account(%s) failed with %v", azure.SkipMatchingTag, resourceGroupName, accountName, err)
	}

	isOperationSucceeded = true
	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerGetVolume get volume
func (d *Driver) ControllerGetVolume(context.Context, *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ValidateVolumeCapabilities return the capabilities of the volume
func (d *Driver) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}
	volCaps := req.GetVolumeCapabilities()
	if len(volCaps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities not provided")
	}

	resourceGroupName, accountName, _, fileShareName, diskName, err := d.GetAccountInfo(ctx, volumeID, req.GetSecrets(), req.GetVolumeContext())
	if err != nil || accountName == "" || fileShareName == "" {
		return nil, status.Errorf(codes.NotFound, "get account info from(%s) failed with error: %v", volumeID, err)
	}
	if resourceGroupName == "" {
		resourceGroupName = d.cloud.ResourceGroup
	}

	if quota, err := d.getFileShareQuota(resourceGroupName, accountName, fileShareName, req.GetSecrets()); err != nil {
		return nil, status.Errorf(codes.Internal, "error checking if volume(%s) exists: %v", volumeID, err)
	} else if quota == -1 {
		return nil, status.Errorf(codes.NotFound, "the requested volume(%s) does not exist.", volumeID)
	}

	confirmed := &csi.ValidateVolumeCapabilitiesResponse_Confirmed{VolumeCapabilities: volCaps}
	if diskName == "" {
		return &csi.ValidateVolumeCapabilitiesResponse{Confirmed: confirmed}, nil
	}
	for _, c := range volCaps {
		if c.GetAccessMode().Mode == csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER {
			return &csi.ValidateVolumeCapabilitiesResponse{}, nil
		}
	}
	return &csi.ValidateVolumeCapabilitiesResponse{Confirmed: confirmed}, nil
}

// ControllerGetCapabilities returns the capabilities of the Controller plugin
func (d *Driver) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: d.Cap,
	}, nil
}

// GetCapacity returns the capacity of the total available storage pool
func (d *Driver) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ListVolumes return all available volumes
func (d *Driver) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerPublishVolume make a volume available on some required node
func (d *Driver) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not provided")
	}

	nodeID := req.GetNodeId()
	if len(nodeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Node ID not provided")
	}

	volContext := req.GetVolumeContext()
	_, accountName, accountKey, fileShareName, diskName, err := d.GetAccountInfo(ctx, volumeID, req.GetSecrets(), volContext)
	// always check diskName first since if it's not vhd disk attach, ControllerPublishVolume is not necessary
	if diskName == "" {
		klog.V(2).Infof("skip ControllerPublishVolume(%s) since it's not vhd disk attach", volumeID)
		if useDataPlaneAPI(volContext) {
			d.dataPlaneAPIVolMap.Store(volumeID, "")
			d.dataPlaneAPIVolMap.Store(accountName, "")
		}
		return &csi.ControllerPublishVolumeResponse{}, nil
	}
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("GetAccountInfo(%s) failed with error: %v", volumeID, err))
	}

	accessMode := volCap.GetAccessMode()
	if accessMode == nil || accessMode.Mode == csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("unsupported AccessMode(%v) for volume(%s)", volCap.GetAccessMode(), volumeID))
	}

	if accessMode.Mode == csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY ||
		accessMode.Mode == csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY {
		// don't lock vhd disk here since it's readonly, while it's user's responsibility to make sure
		// volume is used as ReadOnly, otherwise there would be data corruption for MULTI_NODE_MULTI_WRITER
		klog.V(2).Infof("skip ControllerPublishVolume(%s) since volume is readonly mode", volumeID)
		return &csi.ControllerPublishVolumeResponse{}, nil
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	storageEndpointSuffix := d.cloud.Environment.StorageEndpointSuffix
	fileURL, err := getFileURL(accountName, accountKey, storageEndpointSuffix, fileShareName, diskName)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("getFileURL(%s,%s,%s,%s) returned with error: %v", accountName, storageEndpointSuffix, fileShareName, diskName, err))
	}
	if fileURL == nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("getFileURL(%s,%s,%s,%s) returned empty fileURL", accountName, storageEndpointSuffix, fileShareName, diskName))
	}

	properties, err := fileURL.GetProperties(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("GetProperties for volume(%s) on node(%s) returned with error: %v", volumeID, nodeID, err))
	}

	attachedNodeID, ok := properties.NewMetadata()[metaDataNode]
	if ok && attachedNodeID != "" && !strings.EqualFold(attachedNodeID, nodeID) {
		return nil, status.Error(codes.Internal, fmt.Sprintf("volume(%s) cannot be attached to node(%s) since it's already attached to node(%s)", volumeID, nodeID, attachedNodeID))
	}
	if _, err = fileURL.SetMetadata(ctx, azfile.Metadata{metaDataNode: nodeID}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("SetMetadata for volume(%s) on node(%s) returned with error: %v", volumeID, nodeID, err))
	}
	klog.V(2).Infof("ControllerPublishVolume: volume(%s) attached to node(%s) successfully", volumeID, nodeID)
	return &csi.ControllerPublishVolumeResponse{}, nil
}

// ControllerUnpublishVolume detach the volume on a specified node
func (d *Driver) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	nodeID := req.GetNodeId()
	if len(nodeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Node ID not provided")
	}

	_, accountName, accountKey, fileShareName, diskName, err := d.GetAccountInfo(ctx, volumeID, req.GetSecrets(), map[string]string{})
	// always check diskName first since if it's not vhd disk detach, ControllerUnpublishVolume is not necessary
	if diskName == "" {
		klog.V(2).Infof("skip ControllerUnpublishVolume(%s) since it's not vhd disk detach", volumeID)
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("GetAccountInfo(%s) failed with error: %v", volumeID, err))
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	storageEndpointSuffix := d.cloud.Environment.StorageEndpointSuffix
	fileURL, err := getFileURL(accountName, accountKey, storageEndpointSuffix, fileShareName, diskName)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("getFileURL(%s,%s,%s,%s) returned with error: %v", accountName, storageEndpointSuffix, fileShareName, diskName, err))
	}
	if fileURL == nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("getFileURL(%s,%s,%s,%s) returned empty fileURL", accountName, storageEndpointSuffix, fileShareName, diskName))
	}

	if _, err = fileURL.SetMetadata(ctx, azfile.Metadata{metaDataNode: ""}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("SetMetadata for volume(%s) on node(%s) returned with error: %v", volumeID, nodeID, err))
	}
	klog.V(2).Infof("ControllerUnpublishVolume: volume(%s) detached from node(%s) successfully", volumeID, nodeID)
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// CreateSnapshot create a snapshot
func (d *Driver) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	sourceVolumeID := req.GetSourceVolumeId()
	snapshotName := req.Name
	if len(snapshotName) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Snapshot name must be provided")
	}
	if len(sourceVolumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateSnapshot Source Volume ID must be provided")
	}

	mc := metrics.NewMetricContext(azureFileCSIDriverName, "controller_create_snapshot", d.cloud.ResourceGroup, d.cloud.SubscriptionID, d.Name)
	isOperationSucceeded := false
	defer func() {
		mc.ObserveOperationWithResult(isOperationSucceeded, sourceVolumeID, snapshotName)
	}()

	exists, item, err := d.snapshotExists(ctx, sourceVolumeID, snapshotName, req.GetSecrets())
	if err != nil {
		if exists {
			return nil, status.Errorf(codes.AlreadyExists, "%v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to check if snapshot(%v) exists: %v", snapshotName, err)
	}
	if exists {
		klog.V(2).Infof("snapshot(%s) already exists", snapshotName)
		tp := timestamppb.New(item.Properties.LastModified)
		if tp == nil {
			return nil, status.Errorf(codes.Internal, "Failed to convert timestamp(%v)", item.Properties.LastModified)
		}
		return &csi.CreateSnapshotResponse{
			Snapshot: &csi.Snapshot{
				SizeBytes:      volumehelper.GiBToBytes(int64(item.Properties.Quota)),
				SnapshotId:     sourceVolumeID + "#" + *item.Snapshot,
				SourceVolumeId: sourceVolumeID,
				CreationTime:   tp,
				// Since the snapshot of azurefile has no field of ReadyToUse, here ReadyToUse is always set to true.
				ReadyToUse: true,
			},
		}, nil
	}

	shareURL, err := d.getShareURL(ctx, sourceVolumeID, req.GetSecrets())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get share url with (%s): %v", sourceVolumeID, err)
	}

	snapshotShare, err := shareURL.CreateSnapshot(ctx, azfile.Metadata{snapshotNameKey: snapshotName})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create snapshot from(%s) failed with %v, shareURL: %q", sourceVolumeID, err, shareURL)
	}

	klog.V(2).Infof("Created share snapshot: %s", snapshotShare.Snapshot())

	properties, err := shareURL.GetProperties(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get snapshot properties from (%s): %v", snapshotShare.Snapshot(), err)
	}

	tp := timestamppb.New(properties.LastModified())
	if tp == nil {
		return nil, status.Errorf(codes.Internal, "Failed to convert timestamp(%v)", properties.LastModified())
	}

	createResp := &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SizeBytes:      volumehelper.GiBToBytes(int64(properties.Quota())),
			SnapshotId:     sourceVolumeID + "#" + snapshotShare.Snapshot(),
			SourceVolumeId: sourceVolumeID,
			CreationTime:   tp,
			// Since the snapshot of azurefile has no field of ReadyToUse, here ReadyToUse is always set to true.
			ReadyToUse: true,
		},
	}
	isOperationSucceeded = true
	return createResp, nil
}

// DeleteSnapshot delete a snapshot (todo)
func (d *Driver) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	if len(req.SnapshotId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Snapshot ID must be provided")
	}

	shareURL, err := d.getShareURL(ctx, req.SnapshotId, req.GetSecrets())
	if err != nil {
		// According to CSI Driver Sanity Tester, should succeed when an invalid snapshot id is used
		klog.V(4).Infof("failed to get share url with (%s): %v, returning with success", req.SnapshotId, err)
		return &csi.DeleteSnapshotResponse{}, nil
	}

	snapshot, err := getSnapshot(req.SnapshotId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get snapshot name with (%s): %v", req.SnapshotId, err)
	}

	mc := metrics.NewMetricContext(azureFileCSIDriverName, "controller_delete_snapshot", d.cloud.ResourceGroup, d.cloud.SubscriptionID, d.Name)
	isOperationSucceeded := false
	defer func() {
		mc.ObserveOperationWithResult(isOperationSucceeded, req.SnapshotId, "")
	}()

	if _, err := shareURL.WithSnapshot(snapshot).Delete(ctx, azfile.DeleteSnapshotsOptionNone); err != nil {
		if strings.Contains(err.Error(), "ShareSnapshotNotFound") {
			klog.Warningf("the specify snapshot(%s) was not found", snapshot)
			return &csi.DeleteSnapshotResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to delete snapshot(%s): %v", snapshot, err)
	}

	klog.V(2).Infof("delete snapshot(%s) successfully", snapshot)
	isOperationSucceeded = true
	return &csi.DeleteSnapshotResponse{}, nil
}

// ListSnapshots list all snapshots (todo)
func (d *Driver) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerExpandVolume controller expand volume
func (d *Driver) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	capacityBytes := req.GetCapacityRange().GetRequiredBytes()
	if capacityBytes == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume capacity range missing in request")
	}
	requestGiB := volumehelper.RoundUpGiB(capacityBytes)
	if err := d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_EXPAND_VOLUME); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid expand volume request: %v", req)
	}

	resourceGroupName, accountName, fileShareName, diskName, err := GetFileShareInfo(volumeID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("GetAccountInfo(%s) failed with error: %v", volumeID, err))
	}
	if diskName != "" {
		// todo: figure out how to support vhd disk resize
		return nil, status.Error(codes.Unimplemented, fmt.Sprintf("vhd disk volume(%s) is not supported on ControllerExpandVolume", volumeID))
	}

	mc := metrics.NewMetricContext(azureFileCSIDriverName, "controller_expand_volume", d.cloud.ResourceGroup, d.cloud.SubscriptionID, d.Name)
	isOperationSucceeded := false
	defer func() {
		mc.ObserveOperationWithResult(isOperationSucceeded, volumeID, "")
	}()

	secrets := req.GetSecrets()
	if len(secrets) == 0 && d.useDataPlaneAPI(volumeID, accountName) {
		// use data plane api, get account key first
		_, _, accountKey, _, _, err := d.GetAccountInfo(ctx, volumeID, secrets, map[string]string{})
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "get account info from(%s) failed with error: %v", volumeID, err)
		}
		secrets = createStorageAccountSecret(accountName, accountKey)
	}

	if err = d.ResizeFileShare(resourceGroupName, accountName, fileShareName, int(requestGiB), secrets); err != nil {
		return nil, status.Errorf(codes.Internal, "expand volume error: %v", err)
	}

	isOperationSucceeded = true
	klog.V(2).Infof("ControllerExpandVolume(%s) successfully, currentQuota: %d Gi", volumeID, int(requestGiB))
	return &csi.ControllerExpandVolumeResponse{CapacityBytes: capacityBytes}, nil
}

// getShareURL: sourceVolumeID is the id of source file share, returns a ShareURL of source file share.
// A ShareURL < https://<account>.file.core.windows.net/<fileShareName> > represents a URL to the Azure Storage share allowing you to manipulate its directories and files.
// e.g. The ID of source file share is #fb8fff227be6511e9b24123#createsnapshot-volume-1. Returns https://fb8fff227be6511e9b24123.file.core.windows.net/createsnapshot-volume-1
func (d *Driver) getShareURL(ctx context.Context, sourceVolumeID string, secrets map[string]string) (azfile.ShareURL, error) {
	serviceURL, fileShareName, err := d.getServiceURL(ctx, sourceVolumeID, secrets)
	if err != nil {
		return azfile.ShareURL{}, err
	}
	if fileShareName == "" {
		return azfile.ShareURL{}, fmt.Errorf("failed to get file share from %s", sourceVolumeID)
	}

	return serviceURL.NewShareURL(fileShareName), nil
}

func (d *Driver) getServiceURL(ctx context.Context, sourceVolumeID string, secrets map[string]string) (azfile.ServiceURL, string, error) {
	_, accountName, accountKey, fileShareName, _, err := d.GetAccountInfo(ctx, sourceVolumeID, secrets, map[string]string{})
	if err != nil {
		return azfile.ServiceURL{}, "", err
	}

	credential, err := azfile.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		klog.Errorf("NewSharedKeyCredential(%s) in CreateSnapshot failed with error: %v", accountName, err)
		return azfile.ServiceURL{}, "", err
	}

	u, err := url.Parse(fmt.Sprintf(serviceURLTemplate, accountName, d.cloud.Environment.StorageEndpointSuffix))
	if err != nil {
		klog.Errorf("parse serviceURLTemplate error: %v", err)
		return azfile.ServiceURL{}, "", err
	}
	if u == nil {
		return azfile.ServiceURL{}, "", fmt.Errorf("url is nil")
	}

	serviceURL := azfile.NewServiceURL(*u, azfile.NewPipeline(credential, azfile.PipelineOptions{}))

	return serviceURL, fileShareName, nil
}

// snapshotExists: sourceVolumeID is the id of source file share, returns the existence of snapshot and its detail info.
// Since `ListSharesSegment` lists all file shares and snapshots, the process of checking existence is divided into two steps.
// 1. Judge if the specify snapshot name already exists.
// 2. If it exists, we should judge if its source file share name equals that we specify.
//    As long as the snapshot already exists, returns true. But when the source is different, an error will be returned.
func (d *Driver) snapshotExists(ctx context.Context, sourceVolumeID, snapshotName string, secrets map[string]string) (bool, azfile.ShareItem, error) {
	serviceURL, fileShareName, err := d.getServiceURL(ctx, sourceVolumeID, secrets)
	if err != nil {
		return false, azfile.ShareItem{}, err
	}
	if fileShareName == "" {
		return false, azfile.ShareItem{}, fmt.Errorf("file share is empty after parsing sourceVolumeID: %s", sourceVolumeID)
	}

	// List share snapshots.
	listSnapshot, err := serviceURL.ListSharesSegment(ctx, azfile.Marker{}, azfile.ListSharesOptions{Detail: azfile.ListSharesDetail{Metadata: true, Snapshots: true}})
	if err != nil {
		return false, azfile.ShareItem{}, err
	}
	for _, share := range listSnapshot.ShareItems {
		if share.Metadata[snapshotNameKey] == snapshotName {
			if share.Name == fileShareName {
				klog.V(2).Infof("found share(%s) snapshot(%s) Metadata(%v)", share.Name, *share.Snapshot, share.Metadata)
				return true, share, nil
			}
			return true, azfile.ShareItem{}, fmt.Errorf("snapshot(%s) already exists, while the current file share name(%s) does not equal to %s, SourceVolumeId(%s)", snapshotName, share.Name, fileShareName, sourceVolumeID)
		}
	}

	return false, azfile.ShareItem{}, nil
}

// isValidVolumeCapabilities validates the given VolumeCapability array is valid
func isValidVolumeCapabilities(volCaps []*csi.VolumeCapability) error {
	if len(volCaps) == 0 {
		return fmt.Errorf("CreateVolume Volume capabilities must be provided")
	}
	hasSupport := func(cap *csi.VolumeCapability) error {
		if blk := cap.GetBlock(); blk != nil {
			return fmt.Errorf("driver does not support block volumes")
		}
		for _, c := range volumeCaps {
			if c.GetMode() == cap.AccessMode.GetMode() {
				return nil
			}
		}
		return fmt.Errorf("driver does not support access mode %v", cap.AccessMode.GetMode())
	}

	for _, c := range volCaps {
		if err := hasSupport(c); err != nil {
			return err
		}
	}
	return nil
}
