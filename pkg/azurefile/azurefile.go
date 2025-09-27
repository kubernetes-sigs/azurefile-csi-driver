/*
Copyright 2019 The Kubernetes Authors.

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
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armstorage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/file"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/service"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/rubiojr/go-vhd/vhd"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	mount "k8s.io/mount-utils"
	"k8s.io/utils/ptr"

	csicommon "sigs.k8s.io/azurefile-csi-driver/pkg/csi-common"
	"sigs.k8s.io/azurefile-csi-driver/pkg/mounter"
	fileutil "sigs.k8s.io/azurefile-csi-driver/pkg/util"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/fileshareclient"
	azcache "sigs.k8s.io/cloud-provider-azure/pkg/cache"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/storage"
)

const (
	DefaultDriverName  = "file.csi.azure.com"
	separator          = "#"
	volumeIDTemplate   = "%s#%s#%s#%s#%s#%s"
	secretNameTemplate = "azure-storage-account-%s-secret"
	serviceURLTemplate = "https://%s.file.%s"
	fileURLTemplate    = "https://%s.file.%s/%s/%s"
	subnetTemplate     = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s"
	fileMode           = "file_mode"
	dirMode            = "dir_mode"
	actimeo            = "actimeo"
	noResvPort         = "noresvport"
	mfsymlinks         = "mfsymlinks"
	defaultFileMode    = "0777"
	defaultDirMode     = "0777"
	defaultActimeo     = "30"

	// See https://docs.microsoft.com/en-us/rest/api/storageservices/naming-and-referencing-shares--directories--files--and-metadata#share-names
	fileShareNameMinLength = 3
	fileShareNameMaxLength = 63

	minimumPremiumShareSize   = 100 // GB
	minimumPremiumV2ShareSize = 32  // GB
	// Minimum size of Azure Premium Files is 100GiB, and PremiumV2 Files is 32GiB.
	// See https://docs.microsoft.com/en-us/azure/storage/files/storage-files-planning#provisioned-shares
	defaultAzureFileQuota = 100
	minimumAccountQuota   = 100 // GB

	// key of snapshot name in metadata
	snapshotNameKey = "initiator"

	shareNameField                    = "sharename"
	accessTierField                   = "accesstier"
	shareAccessTierField              = "shareaccesstier"
	accountAccessTierField            = "accountaccesstier"
	rootSquashTypeField               = "rootsquashtype"
	diskNameField                     = "diskname"
	folderNameField                   = "foldername"
	createFolderIfNotExistField       = "createfolderifnotexist"
	serverNameField                   = "server"
	fsTypeField                       = "fstype"
	protocolField                     = "protocol"
	matchTagsField                    = "matchtags"
	tagsField                         = "tags"
	storageAccountField               = "storageaccount"
	storageAccountTypeField           = "storageaccounttype"
	skuNameField                      = "skuname"
	enableLargeFileSharesField        = "enablelargefileshares"
	subscriptionIDField               = "subscriptionid"
	resourceGroupField                = "resourcegroup"
	locationField                     = "location"
	secretNamespaceField              = "secretnamespace"
	secretNameField                   = "secretname"
	createAccountField                = "createaccount"
	useDataPlaneAPIField              = "usedataplaneapi"
	storeAccountKeyField              = "storeaccountkey"
	getLatestAccountKeyField          = "getlatestaccountkey"
	useSecretCacheField               = "usesecretcache"
	getAccountKeyFromSecretField      = "getaccountkeyfromsecret"
	disableDeleteRetentionPolicyField = "disabledeleteretentionpolicy"
	allowBlobPublicAccessField        = "allowblobpublicaccess"
	publicNetworkAccessField          = "publicnetworkaccess"
	allowSharedKeyAccessField         = "allowsharedkeyaccess"
	storageEndpointSuffixField        = "storageendpointsuffix"
	fsGroupChangePolicyField          = "fsgroupchangepolicy"
	ephemeralField                    = "csi.storage.k8s.io/ephemeral"
	podNameField                      = "csi.storage.k8s.io/pod.name"
	podNamespaceField                 = "csi.storage.k8s.io/pod.namespace"
	serviceAccountTokenField          = "csi.storage.k8s.io/serviceAccount.tokens"
	clientIDField                     = "clientid"
	tenantIDField                     = "tenantid"
	mountOptionsField                 = "mountoptions"
	mountPermissionsField             = "mountpermissions"
	encryptInTransitField             = "encryptintransit"
	provisionedBandwidthField         = "provisionedbandwidth"
	provisionedIopsField              = "provisionediops"
	falseValue                        = "false"
	trueValue                         = "true"
	defaultSecretAccountName          = "azurestorageaccountname"
	defaultSecretAccountKey           = "azurestorageaccountkey"
	proxyMount                        = "proxy-mount"
	cifs                              = "cifs"
	smb                               = "smb"
	nfs                               = "nfs"
	aznfs                             = "aznfs"
	ext4                              = "ext4"
	ext3                              = "ext3"
	ext2                              = "ext2"
	xfs                               = "xfs"
	vhdSuffix                         = ".vhd"
	metaDataNode                      = "node"
	networkEndpointTypeField          = "networkendpointtype"
	vnetResourceGroupField            = "vnetresourcegroup"
	vnetNameField                     = "vnetname"
	vnetLinkNameField                 = "vnetlinkname"
	subnetNameField                   = "subnetname"
	shareNamePrefixField              = "sharenameprefix"
	requireInfraEncryptionField       = "requireinfraencryption"
	enableMultichannelField           = "enablemultichannel"
	standard                          = "standard"
	premium                           = "premium"
	selectRandomMatchingAccountField  = "selectrandommatchingaccount"
	accountQuotaField                 = "accountquota"
	confidentialContainerLabelField   = "confidentialcontainerlabel"
	defaultConfidentialContainerLabel = "kubernetes.azure.com/kata-cc-isolation"
	runtimeClassHandlerField          = "runtimeclasshandler"
	defaultRuntimeClassHandler        = "kata-cc"
	mountWithManagedIdentityField     = "mountwithmanagedidentity"

	accountNotProvisioned = "StorageAccountIsNotProvisioned"
	// this is a workaround fix for 429 throttling issue, will update cloud provider for better fix later
	tooManyRequests   = "TooManyRequests"
	shareBeingDeleted = "The specified share is being deleted"
	clientThrottled   = "client throttled"
	// accountLimitExceed returned by different API
	accountLimitExceedManagementAPI = "TotalSharesProvisionedCapacityExceedsAccountLimit"
	accountLimitExceedDataPlaneAPI  = "specified share does not exist"

	fileShareNotFound  = "ShareNotFound"
	statusCodeNotFound = "StatusCode=404"
	httpCodeNotFound   = "HTTPStatusCode: 404"

	// define different sleep time when hit throttling
	accountOpThrottlingSleepSec = 16
	fileOpThrottlingSleepSec    = 180
	maxThrottlingSleepSec       = 1200

	defaultAccountNamePrefix = "f"

	defaultNamespace = "default"

	pvcNameKey           = "csi.storage.k8s.io/pvc/name"
	pvcNamespaceKey      = "csi.storage.k8s.io/pvc/namespace"
	pvNameKey            = "csi.storage.k8s.io/pv/name"
	pvcNameMetadata      = "${pvc.metadata.name}"
	pvcNamespaceMetadata = "${pvc.metadata.namespace}"
	pvNameMetadata       = "${pv.metadata.name}"

	defaultStorageEndPointSuffix = "core.windows.net"

	VolumeID         = "volumeid"
	SourceResourceID = "source_resource_id"
	SnapshotName     = "snapshot_name"
	SnapshotID       = "snapshot_id"

	FSGroupChangeNone = "None"
	// define tag value delimiter and default is comma
	tagValueDelimiterField = "tagvaluedelimiter"
	// for data plane API
	oauth = "oauth"

	standardv2 = "standardv2"
	premiumv2  = "premiumv2"
)

var (
	supportedFsTypeList              = []string{cifs, smb, nfs, ext4, ext3, ext2, xfs}
	supportedProtocolList            = []string{smb, nfs}
	supportedDiskFsTypeList          = []string{ext4, ext3, ext2, xfs}
	supportedFSGroupChangePolicyList = []string{FSGroupChangeNone, string(v1.FSGroupChangeAlways), string(v1.FSGroupChangeOnRootMismatch)}

	retriableErrors = []string{accountNotProvisioned, tooManyRequests, shareBeingDeleted, clientThrottled}

	// azcopyCloneVolumeOptions used in volume cloning and set --check-length to false because volume data may be in changing state, copy volume is not same as current source volume
	azcopyCloneVolumeOptions = []string{"--recursive", "--check-length=false", "--log-level=ERROR"}
	// azcopySnapshotRestoreOptions used in smb snapshot restore and set --check-length to true because snapshot data is changeless
	azcopySnapshotRestoreOptions = []string{"--recursive", "--check-length=true", "--log-level=ERROR"}
)

// Driver implements all interfaces of CSI drivers
type Driver struct {
	csicommon.CSIDriver
	// Embed UnimplementedXXXServer to ensure the driver returns Unimplemented for any
	// new RPC methods that might be introduced in future versions of the spec.
	csi.UnimplementedControllerServer
	csi.UnimplementedIdentityServer
	csi.UnimplementedNodeServer

	cloud                                  *storage.AccountRepo
	kubeClient                             clientset.Interface
	enableAzurefileProxy                   bool
	azurefileProxyEndpoint                 string
	cloudConfigSecretName                  string
	cloudConfigSecretNamespace             string
	customUserAgent                        string
	userAgentSuffix                        string
	fsGroupChangePolicy                    string
	allowEmptyCloudConfig                  bool
	allowInlineVolumeKeyAccessWithIdentity bool
	enableVHDDiskFeature                   bool
	enableGetVolumeStats                   bool
	enableVolumeMountGroup                 bool
	appendMountErrorHelpLink               bool
	mountPermissions                       uint64
	kubeAPIQPS                             float64
	kubeAPIBurst                           int
	enableWindowsHostProcess               bool
	removeSMBMountOnWindows                bool
	appendClosetimeoOption                 bool
	appendNoShareSockOption                bool
	appendNoResvPortOption                 bool
	appendActimeoOption                    bool
	printVolumeStatsCallLogs               bool
	enableKataCCMount                      bool
	useWinCIMAPI                           bool
	mounter                                *mount.SafeFormatAndMount
	server                                 *grpc.Server
	// lock per volume attach (only for vhd disk feature)
	volLockMap *lockMap
	// only for nfs feature
	subnetLockMap *lockMap
	// a map storing all volumes with ongoing operations so that additional operations
	// for that same volume (as defined by VolumeID) return an Aborted error
	volumeLocks *volumeLocks
	// a map storing all volumes created by this driver <volumeName, accountName>
	volMap sync.Map
	// a timed cache storing all account name and keys retrieved by this driver <accountName, accountkey>
	accountCacheMap azcache.Resource
	// a map storing all secret names created by this driver <secretCacheKey, "">
	secretCacheMap azcache.Resource
	// a map storing all volumes using data plane API <volumeID, value>
	dataPlaneAPIVolMap sync.Map
	// a timed cache storing all storage accounts that are using data plane API temporarily
	dataPlaneAPIAccountCache azcache.Resource
	// a timed cache storing account search history (solve account list throttling issue)
	accountSearchCache azcache.Resource
	// a timed cache storing whether skipMatchingTag is added or removed recently
	skipMatchingTagCache azcache.Resource
	// a timed cache when resize file share failed due to account limit exceeded
	resizeFileShareFailureCache azcache.Resource
	// a timed cache storing volume stats <volumeID, volumeStats>
	volStatsCache azcache.Resource
	// a timed cache storing account which should use sastoken for azcopy based volume cloning
	azcopySasTokenCache azcache.Resource
	// a timed cache storing subnet operations
	subnetCache azcache.Resource
	// a timed cache storing file share size <accountName-fileShareName, size(int32)>
	getFileShareSizeCache azcache.Resource
	// sas expiry time for azcopy in volume clone and snapshot restore
	sasTokenExpirationMinutes int
	// azcopy timeout for volume clone and snapshot restore
	waitForAzCopyTimeoutMinutes int
	// azcopy for provide exec mock for ut
	azcopy *fileutil.Azcopy

	kubeconfig   string
	endpoint     string
	resolver     Resolver
	directVolume DirectVolume
	isKataNode   bool
}

// NewDriver Creates a NewCSIDriver object. Assumes vendor version is equal to driver version &
// does not support optional driver plugin info manifest field. Refer to CSI spec for more details.
func NewDriver(options *DriverOptions) *Driver {
	driver := Driver{}
	driver.Name = options.DriverName
	driver.Version = driverVersion
	driver.NodeID = options.NodeID
	driver.enableAzurefileProxy = options.EnableAzurefileProxy
	driver.azurefileProxyEndpoint = options.AzureFileProxyEndpoint
	driver.cloudConfigSecretName = options.CloudConfigSecretName
	driver.cloudConfigSecretNamespace = options.CloudConfigSecretNamespace
	driver.customUserAgent = options.CustomUserAgent
	driver.userAgentSuffix = options.UserAgentSuffix
	driver.allowEmptyCloudConfig = options.AllowEmptyCloudConfig
	driver.allowInlineVolumeKeyAccessWithIdentity = options.AllowInlineVolumeKeyAccessWithIdentity
	driver.enableVHDDiskFeature = options.EnableVHDDiskFeature
	driver.enableVolumeMountGroup = options.EnableVolumeMountGroup
	driver.enableGetVolumeStats = options.EnableGetVolumeStats
	driver.enableKataCCMount = options.EnableKataCCMount
	driver.appendMountErrorHelpLink = options.AppendMountErrorHelpLink
	driver.mountPermissions = options.MountPermissions
	driver.fsGroupChangePolicy = options.FSGroupChangePolicy
	driver.kubeAPIQPS = options.KubeAPIQPS
	driver.kubeAPIBurst = options.KubeAPIBurst
	driver.enableWindowsHostProcess = options.EnableWindowsHostProcess
	driver.removeSMBMountOnWindows = options.RemoveSMBMountOnWindows
	driver.appendClosetimeoOption = options.AppendClosetimeoOption
	driver.appendNoShareSockOption = options.AppendNoShareSockOption
	driver.appendNoResvPortOption = options.AppendNoResvPortOption
	driver.appendActimeoOption = options.AppendActimeoOption
	driver.printVolumeStatsCallLogs = options.PrintVolumeStatsCallLogs
	driver.sasTokenExpirationMinutes = options.SasTokenExpirationMinutes
	driver.waitForAzCopyTimeoutMinutes = options.WaitForAzCopyTimeoutMinutes
	driver.volLockMap = newLockMap()
	driver.subnetLockMap = newLockMap()
	driver.volumeLocks = newVolumeLocks()
	driver.azcopy = &fileutil.Azcopy{ExecCmd: &fileutil.ExecCommand{}}
	driver.kubeconfig = options.KubeConfig
	driver.endpoint = options.Endpoint
	driver.resolver = new(NetResolver)
	driver.directVolume = new(directVolume)
	driver.isKataNode = false
	driver.useWinCIMAPI = options.UseWinCIMAPI

	var err error
	getter := func(_ context.Context, _ string) (interface{}, error) { return nil, nil }

	if driver.secretCacheMap, err = azcache.NewTimedCache(time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.accountSearchCache, err = azcache.NewTimedCache(time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	if options.SkipMatchingTagCacheExpireInMinutes <= 0 {
		options.SkipMatchingTagCacheExpireInMinutes = 30 // default expire in 30 minutes
	}
	if driver.skipMatchingTagCache, err = azcache.NewTimedCache(time.Duration(options.SkipMatchingTagCacheExpireInMinutes)*time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.accountCacheMap, err = azcache.NewTimedCache(3*time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.dataPlaneAPIAccountCache, err = azcache.NewTimedCache(10*time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.azcopySasTokenCache, err = azcache.NewTimedCache(15*time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.resizeFileShareFailureCache, err = azcache.NewTimedCache(3*time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	if options.VolStatsCacheExpireInMinutes <= 0 {
		options.VolStatsCacheExpireInMinutes = 10 // default expire in 10 minutes
	}
	if driver.volStatsCache, err = azcache.NewTimedCache(time.Duration(options.VolStatsCacheExpireInMinutes)*time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.subnetCache, err = azcache.NewTimedCache(10*time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.getFileShareSizeCache, err = azcache.NewTimedCache(10*time.Minute, getter, false); err != nil {
		klog.Fatalf("%v", err)
	}

	return &driver
}

// Run driver initialization
func (d *Driver) Run(ctx context.Context) error {
	versionMeta, err := GetVersionYAML(d.Name)
	if err != nil {
		klog.Fatalf("%v", err)
	}
	klog.Infof("\nDRIVER INFORMATION:\n-------------------\n%s\n\nStreaming logs below:", versionMeta)

	if d.NodeID == "" {
		// nodeid is not needed in controller component
		klog.Warning("nodeid is empty")
	}

	userAgent := GetUserAgent(d.Name, d.customUserAgent, d.userAgentSuffix)
	klog.V(2).Infof("driver userAgent: %s", userAgent)
	d.cloud, d.kubeClient, err = getCloudProvider(context.Background(), d.kubeconfig, d.NodeID, d.cloudConfigSecretName, d.cloudConfigSecretNamespace, userAgent, d.allowEmptyCloudConfig, d.enableWindowsHostProcess, d.kubeAPIQPS, d.kubeAPIBurst)
	if err != nil {
		klog.Fatalf("failed to get Azure Cloud Provider, error: %v", err)
	}
	klog.V(2).Infof("cloud: %s, location: %s, rg: %s, VnetName: %s, VnetResourceGroup: %s, SubnetName: %s", d.cloud.Cloud, d.cloud.Location, d.cloud.ResourceGroup, d.cloud.VnetName, d.cloud.VnetResourceGroup, d.cloud.SubnetName)

	d.mounter, err = mounter.NewSafeMounter(d.enableWindowsHostProcess, d.useWinCIMAPI)
	if err != nil {
		klog.Fatalf("Failed to get safe mounter. Error: %v", err)
	}

	// Initialize default library driver
	d.AddControllerServiceCapabilities(
		[]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
			csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
			csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
			csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		})
	d.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
	})

	nodeCap := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	}
	if d.enableVolumeMountGroup {
		nodeCap = append(nodeCap, csi.NodeServiceCapability_RPC_VOLUME_MOUNT_GROUP)
	}
	if d.enableGetVolumeStats {
		nodeCap = append(nodeCap, csi.NodeServiceCapability_RPC_GET_VOLUME_STATS)
	}
	d.AddNodeServiceCapabilities(nodeCap)

	//setup grpc server
	opts := []grpc.ServerOption{
		// TODO: add more interceptors.
		grpc.ChainUnaryInterceptor(
			grpcprom.NewServerMetrics().UnaryServerInterceptor(),
			csicommon.LogGRPC,
		),
	}
	server := grpc.NewServer(opts...)
	csi.RegisterIdentityServer(server, d)
	csi.RegisterControllerServer(server, d)
	csi.RegisterNodeServer(server, d)
	d.server = server
	d.isKataNode = isKataNode(ctx, d.NodeID, defaultConfidentialContainerLabel, d.kubeClient)

	listener, err := csicommon.ListenEndpoint(ctx, d.endpoint)
	if err != nil {
		klog.Fatalf("failed to listen endpoint: %v", err)
	}
	go func() {
		<-ctx.Done()
		d.server.GracefulStop()
	}()
	if err = d.server.Serve(listener); errors.Is(err, grpc.ErrServerStopped) {
		klog.Infof("gRPC server stopped serving")
		return nil
	}
	return err
}

// getFileShareQuota return (-1, nil) means file share does not exist
func (d *Driver) getFileShareQuota(ctx context.Context, accountOptions *storage.AccountOptions, fileShareName string, secrets map[string]string, useDataPlaneAPI string) (int, error) {
	var fileClient azureFileClient
	var err error
	if len(secrets) > 0 {
		accountName, accountKey, rerr := getStorageAccount(secrets)
		if rerr != nil {
			return -1, rerr
		}
		storageEndPointSuffix := d.getStorageEndPointSuffix()
		if accountOptions != nil && accountOptions.StorageEndpointSuffix != "" {
			storageEndPointSuffix = accountOptions.StorageEndpointSuffix
		}
		fileClient, err = newAzureFileClient(accountName, accountKey, storageEndPointSuffix)
	} else if d.cloud != nil && d.cloud.AuthProvider != nil && strings.EqualFold(useDataPlaneAPI, oauth) {
		fileClient, err = newAzureFileClientWithOAuth(d.cloud.AuthProvider.GetAzIdentity(), accountOptions.Name, d.getStorageEndPointSuffix())
	} else {
		fileClient, err = newAzureFileMgmtClient(d.cloud, accountOptions)
	}

	if err != nil {
		return -1, err
	}
	quota, err := fileClient.GetFileShareQuota(ctx, fileShareName)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr != nil && respErr.StatusCode == http.StatusNotFound {
			return -1, nil
		}
		return -1, err
	}
	return quota, nil

}

// get file share info according to volume id, e.g.
// input: "rg#f5713de20cde511e8ba4900#fileShareName#diskname.vhd#uuid#namespace#subsID"
// output: rg, f5713de20cde511e8ba4900, fileShareName, diskname.vhd, namespace, subsID
func GetFileShareInfo(id string) (string, string, string, string, string, string, error) {
	segments := strings.Split(id, separator)
	if len(segments) < 3 {
		return "", "", "", "", "", "", fmt.Errorf("error parsing volume id: %q, should at least contain two #", id)
	}
	rg := segments[0]
	var diskName, namespace, subsID string
	if len(segments) > 3 {
		diskName = segments[3]
	}
	if rg == "" {
		// in csi migration, rg could be empty, then the 5th element is namespace
		// https://github.com/kubernetes/kubernetes/blob/v1.23.5/staging/src/k8s.io/csi-translation-lib/plugins/azure_file.go#L137
		if len(segments) > 4 {
			namespace = segments[4]
		}
	} else {
		if len(segments) > 5 {
			namespace = segments[5]
		}
		if len(segments) > 6 {
			subsID = segments[6]
		}
	}
	return rg, segments[1], segments[2], diskName, namespace, subsID, nil
}

// get snapshot info according to snapshot id, e.g.
// input:
//
//	capz-qjbped#f3d5809ad977d4606b8997d#pvc-061c8214-2330-4b3e-88d0-6ef8d84636bc###azurefile-6654#2025-09-05T07:51:41.0000000Z#46678f10-4bbb-447e-98e8-d2829589f2d8
//	capz-qjbped#f3d5809ad977d4606b8997d#pvc-061c8214-2330-4b3e-88d0-6ef8d84636bc###azurefile-6654#46678f10-4bbb-447e-98e8-d2829589f2d8#2025-09-05T07:51:41.0000000Z
//
// output:
//
//	capz-qjbped, f3d5809ad977d4606b8997d, pvc-061c8214-2330-4b3e-88d0-6ef8d84636bc, snapshotTime, 46678f10-4bbb-447e-98e8-d2829589f2d8
func GetInfoFromSnapshotID(id string) (string, string, string, string, string, error) {
	segments := strings.Split(id, separator)
	if len(segments) < 7 {
		return "", "", "", "", "", fmt.Errorf("error parsing snapshot id: %q, should at least contain 6 #", id)
	}
	snapshotTime := segments[6]
	var subsID string
	if len(segments) > 7 {
		if isValidSubscriptionID(segments[7]) {
			subsID = segments[7]
		} else {
			if isValidSubscriptionID(segments[6]) {
				subsID = segments[6]
				snapshotTime = segments[7]
			}
		}
	}
	return segments[0], segments[1], segments[2], snapshotTime, subsID, nil
}

// check whether mountOptions contains file_mode, dir_mode, vers, if not, append default mode
func appendDefaultCifsMountOptions(mountOptions []string, appendNoShareSockOption, appendClosetimeoOption bool) []string {
	var defaultMountOptions = map[string]string{
		fileMode:   defaultFileMode,
		dirMode:    defaultDirMode,
		actimeo:    defaultActimeo,
		mfsymlinks: "",
	}

	if appendClosetimeoOption {
		defaultMountOptions["sloppy,closetimeo=0"] = ""
	}
	if appendNoShareSockOption {
		defaultMountOptions["nosharesock"] = ""
	}

	// stores the mount options already included in mountOptions
	included := make(map[string]bool)

	for _, mountOption := range mountOptions {
		for k := range defaultMountOptions {
			if strings.HasPrefix(mountOption, k) {
				included[k] = true
			}
		}
		// actimeo would set both acregmax and acdirmax, so we only need to check one of them
		if strings.Contains(mountOption, "acregmax") || strings.Contains(mountOption, "acdirmax") {
			included[actimeo] = true
		}
	}

	allMountOptions := mountOptions

	for k, v := range defaultMountOptions {
		if _, isIncluded := included[k]; !isIncluded {
			if v != "" {
				allMountOptions = append(allMountOptions, fmt.Sprintf("%s=%s", k, v))
			} else {
				allMountOptions = append(allMountOptions, k)
			}
		}
	}

	return allMountOptions
}

// check whether mountOptions contains actimeo, if not, append default mode
func appendDefaultNfsMountOptions(mountOptions []string, appendNoResvPortOption, appendActimeoOption bool) []string {
	var defaultMountOptions = map[string]string{}
	if appendNoResvPortOption {
		defaultMountOptions[noResvPort] = ""
	}
	if appendActimeoOption {
		defaultMountOptions[actimeo] = defaultActimeo
	}

	if len(defaultMountOptions) == 0 {
		return mountOptions
	}

	// stores the mount options already included in mountOptions
	included := make(map[string]bool)

	for _, mountOption := range mountOptions {
		for k := range defaultMountOptions {
			if strings.HasPrefix(mountOption, k) {
				included[k] = true
			}
		}
	}

	allMountOptions := mountOptions

	for k, v := range defaultMountOptions {
		if _, isIncluded := included[k]; !isIncluded {
			if v != "" {
				allMountOptions = append(allMountOptions, fmt.Sprintf("%s=%s", k, v))
			} else {
				allMountOptions = append(allMountOptions, k)
			}
		}
	}

	return allMountOptions
}

// get storage account from secrets map
func getStorageAccount(secrets map[string]string) (string, string, error) {
	if secrets == nil {
		return "", "", fmt.Errorf("unexpected: getStorageAccount secrets is nil")
	}

	var accountName, accountKey string
	for k, v := range secrets {
		v = strings.TrimSpace(v)
		switch strings.ToLower(k) {
		case "accountname":
			accountName = v
		case defaultSecretAccountName: // for compatibility with built-in azurefile plugin
			accountName = v
		case "accountkey":
			accountKey = v
		case defaultSecretAccountKey: // for compatibility with built-in azurefile plugin
			accountKey = v
		}
	}

	if accountName == "" {
		return "", "", fmt.Errorf("could not find accountname or azurestorageaccountname field in secrets")
	}
	if accountKey == "" {
		return "", "", fmt.Errorf("could not find accountkey or azurestorageaccountkey field in secrets")
	}
	accountName = strings.TrimSpace(accountName)

	klog.V(4).Infof("got storage account(%s) from secret", accountName)
	return accountName, accountKey, nil
}

// File share names can contain only lowercase letters, numbers, and hyphens,
// and must begin and end with a letter or a number,
// and must be from 3 through 63 characters long.
// The name cannot contain two consecutive hyphens.
//
// See https://docs.microsoft.com/en-us/rest/api/storageservices/naming-and-referencing-shares--directories--files--and-metadata#share-names
func getValidFileShareName(volumeName string) string {
	fileShareName := strings.ToLower(volumeName)
	if len(fileShareName) > fileShareNameMaxLength {
		fileShareName = fileShareName[0:fileShareNameMaxLength]
	}
	if !checkShareNameBeginAndEnd(fileShareName) || len(fileShareName) < fileShareNameMinLength {
		fileShareName = fileutil.GenerateVolumeName("pvc-file", uuid.NewString(), fileShareNameMaxLength)
		klog.Warningf("the requested volume name (%q) is invalid, so it is regenerated as (%q)", volumeName, fileShareName)
	}
	fileShareName = strings.Replace(fileShareName, "--", "-", -1)

	return strings.ToLower(fileShareName)
}

func checkShareNameBeginAndEnd(fileShareName string) bool {
	length := len(fileShareName)
	if (('a' <= fileShareName[0] && fileShareName[0] <= 'z') ||
		('0' <= fileShareName[0] && fileShareName[0] <= '9')) &&
		(('a' <= fileShareName[length-1] && fileShareName[length-1] <= 'z') ||
			('0' <= fileShareName[length-1] && fileShareName[length-1] <= '9')) {
		return true
	}

	return false
}

func getDirectoryClient(accountName, accountKey, storageEndpointSuffix, fileShareName, diskName string) (*file.Client, error) {
	credential, err := service.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, fmt.Errorf("NewSharedKeyCredential(%s) failed with error: %v", accountName, err)
	}
	u, err := url.Parse(fmt.Sprintf(fileURLTemplate, accountName, storageEndpointSuffix, fileShareName, diskName))
	if err != nil {
		return nil, fmt.Errorf("parse fileURLTemplate error: %v", err)
	}
	if u == nil {
		return nil, fmt.Errorf("parse fileURLTemplate error: url is nil")
	}
	serviceURL := getFileServiceURL(accountName, storageEndpointSuffix)

	serviceClient, err := service.NewClientWithSharedKeyCredential(serviceURL, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("NewClientWithSharedKeyCredential(%s) failed with error: %v", serviceURL, err)
	}
	shareClient := serviceClient.NewShareClient(fileShareName)
	return shareClient.NewRootDirectoryClient().NewFileClient(diskName), nil
}

func createDisk(ctx context.Context, accountName, accountKey, storageEndpointSuffix, fileShareName, diskName string, diskSizeBytes int64) error {
	vhdHeader := vhd.CreateFixedHeader(uint64(diskSizeBytes), &vhd.VHDOptions{})
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, vhdHeader); nil != err {
		return fmt.Errorf("failed to write VHDHeader(%+v): %v", vhdHeader, err)
	}
	headerBytes := buf.Bytes()
	start := diskSizeBytes - int64(len(headerBytes))
	end := diskSizeBytes - 1

	fileClient, err := getDirectoryClient(accountName, accountKey, storageEndpointSuffix, fileShareName, diskName)
	if err != nil {
		return err
	}
	if fileClient == nil {
		return fmt.Errorf("getFileURL(%s,%s,%s,%s) return empty fileURL", accountName, storageEndpointSuffix, fileShareName, diskName)
	}
	if _, err = fileClient.Create(ctx, diskSizeBytes, nil); err != nil {
		return err
	}
	if _, err = fileClient.UploadRange(ctx, end-start, streaming.NopCloser(bytes.NewReader(headerBytes[:vhd.VHD_HEADER_SIZE])), nil); err != nil {
		return err
	}
	return nil
}

func IsCorruptedDir(dir string) bool {
	_, pathErr := mount.PathExists(dir)
	return pathErr != nil && mount.IsCorruptedMnt(pathErr)
}

// GetAccountInfo get account info
// return <rgName, accountName, accountKey, fileShareName, diskName, subsID, err>
func (d *Driver) GetAccountInfo(ctx context.Context, volumeID string, secrets, reqContext map[string]string) (string, string, string, string, string, string, error) {
	rgName, accountName, fileShareName, diskName, secretNamespace, subsID, err := GetFileShareInfo(volumeID)
	if err != nil {
		// ignore volumeID parsing error
		klog.V(6).Infof("parsing volumeID(%s) return with error: %v", volumeID, err)
		err = nil
	}

	var protocol, accountKey, secretName, pvcNamespace string
	// getAccountKeyFromSecret indicates whether get account key only from k8s secret
	var getAccountKeyFromSecret, getLatestAccountKey, mountWithManagedIdentity bool
	var clientID, tenantID, serviceAccountToken string

	for k, v := range reqContext {
		switch strings.ToLower(k) {
		case subscriptionIDField:
			subsID = v
		case resourceGroupField:
			rgName = v
		case storageAccountField:
			accountName = v
		case getAccountKeyFromSecretField:
			if strings.EqualFold(v, trueValue) {
				getAccountKeyFromSecret = true
			}
		case shareNameField:
			fileShareName = v
		case diskNameField:
			diskName = v
		case protocolField:
			protocol = v
		case secretNameField:
			secretName = v
		case secretNamespaceField:
			secretNamespace = v
		case pvcNamespaceKey:
			pvcNamespace = v
		case getLatestAccountKeyField:
			if getLatestAccountKey, err = strconv.ParseBool(v); err != nil {
				return rgName, accountName, accountKey, fileShareName, diskName, subsID, fmt.Errorf("invalid %s: %s in volume context", getLatestAccountKeyField, v)
			}
		case clientIDField:
			clientID = v
		case mountWithManagedIdentityField:
			if mountWithManagedIdentity, err = strconv.ParseBool(v); err != nil {
				return rgName, accountName, accountKey, fileShareName, diskName, subsID, fmt.Errorf("invalid %s: %s in volume context", mountWithManagedIdentityField, v)
			}
		case tenantIDField:
			tenantID = v
		case strings.ToLower(serviceAccountTokenField):
			serviceAccountToken = v
		}
	}

	if tenantID == "" {
		tenantID = d.cloud.TenantID
	}
	if rgName == "" {
		rgName = d.cloud.ResourceGroup
	}
	if subsID == "" {
		subsID = d.cloud.SubscriptionID
	}
	if protocol == nfs && fileShareName != "" {
		// nfs protocol does not need account key, return directly
		return rgName, accountName, accountKey, fileShareName, diskName, subsID, err
	}

	if secretNamespace == "" {
		if pvcNamespace == "" {
			secretNamespace = defaultNamespace
		} else {
			secretNamespace = pvcNamespace
		}
	}

	if mountWithManagedIdentity {
		klog.V(2).Infof("mountWithManagedIdentity is true, use managed identity auth")
		return rgName, accountName, accountKey, fileShareName, diskName, subsID, nil
	}

	if clientID != "" {
		klog.V(2).Infof("clientID(%s) is specified, use service account token to get account key", clientID)
		accountKey, err := d.cloud.GetStorageAccesskeyFromServiceAccountToken(ctx, subsID, accountName, rgName, clientID, tenantID, serviceAccountToken)
		return rgName, accountName, accountKey, fileShareName, diskName, subsID, err
	}

	if len(secrets) == 0 {
		// if request context does not contain secrets, get secrets in the following order:
		// 1. get account key from cache first
		cache, errCache := d.accountCacheMap.Get(ctx, accountName, azcache.CacheReadTypeDefault)
		if errCache != nil {
			return rgName, accountName, accountKey, fileShareName, diskName, subsID, errCache
		}
		if cache != nil {
			accountKey = cache.(string)
		} else {
			if secretName == "" && accountName != "" {
				secretName = fmt.Sprintf(secretNameTemplate, accountName)
			}
			if secretName != "" {
				var name string
				// 2. if not found in cache, get account key from kubernetes secret
				name, accountKey, err = d.GetStorageAccountFromSecret(ctx, secretName, secretNamespace)
				if name != "" {
					accountName = name
				}
				if err != nil {
					// 3. if failed to get account key from kubernetes secret, use cluster identity to get account key
					klog.Warningf("GetStorageAccountFromSecret(%s, %s) failed with error: %v", secretName, secretNamespace, err)
					if !getAccountKeyFromSecret && accountName != "" {
						klog.V(2).Infof("use cluster identity to get account key from (%s, %s, %s)", subsID, rgName, accountName)
						accountKey, err = d.GetStorageAccesskeyWithSubsID(ctx, subsID, accountName, rgName, getLatestAccountKey)
						if err != nil {
							klog.Errorf("GetStorageAccesskey(%s, %s, %s) failed with error: %v", subsID, rgName, accountName, err)
						}
					}
				}
			}
		}
	} else { // if request context contains secrets, get account name and key directly
		var account string
		account, accountKey, err = getStorageAccount(secrets)
		if account != "" {
			accountName = account
		}
		if err != nil {
			klog.Errorf("getStorageAccount failed with error: %v", err)
		}
	}

	if err == nil && accountKey != "" {
		d.accountCacheMap.Set(accountName, accountKey)
	}
	return rgName, accountName, accountKey, fileShareName, diskName, subsID, err
}

func isSupportedProtocol(protocol string) bool {
	if protocol == "" {
		return true
	}
	for _, v := range supportedProtocolList {
		if protocol == v {
			return true
		}
	}
	return false
}

func isSupportedShareAccessTier(accessTier string) bool {
	if accessTier == "" {
		return true
	}
	for _, tier := range armstorage.PossibleShareAccessTierValues() {
		if accessTier == string(tier) {
			return true
		}
	}
	return false
}

func isSupportedAccountAccessTier(accessTier string) bool {
	if accessTier == "" {
		return true
	}
	for _, tier := range armstorage.PossibleAccessTierValues() {
		if accessTier == string(tier) {
			return true
		}
	}
	return false
}

func isSupportedPublicNetworkAccess(publicNetworkAccess string) bool {
	if publicNetworkAccess == "" {
		return true
	}
	for _, tier := range armstorage.PossiblePublicNetworkAccessValues() {
		if publicNetworkAccess == string(tier) {
			return true
		}
	}
	return false
}

func isSupportedRootSquashType(rootSquashType string) bool {
	if rootSquashType == "" {
		return true
	}
	for _, tier := range armstorage.PossibleRootSquashTypeValues() {
		if rootSquashType == string(tier) {
			return true
		}
	}
	return false
}

func isSupportedFSGroupChangePolicy(policy string) bool {
	if policy == "" {
		return true
	}
	for _, v := range supportedFSGroupChangePolicyList {
		if policy == v {
			return true
		}
	}
	return false
}

// CreateFileShare creates a file share
func (d *Driver) CreateFileShare(ctx context.Context, accountOptions *storage.AccountOptions, shareOptions *ShareOptions, secrets map[string]string, useDataPlaneAPI string) error {
	return wait.ExponentialBackoff(getBackOff(d.cloud.Config), func() (bool, error) {
		var err error
		var fileClient azureFileClient
		if len(secrets) > 0 {
			var accountName, accountKey string
			accountName, accountKey, err = getStorageAccount(secrets)
			if err != nil {
				return true, err
			}
			storageEndPointSuffix := d.getStorageEndPointSuffix()
			if accountOptions != nil && accountOptions.StorageEndpointSuffix != "" {
				storageEndPointSuffix = accountOptions.StorageEndpointSuffix
			}
			if fileClient, err = newAzureFileClient(accountName, accountKey, storageEndPointSuffix); err != nil {
				return true, err
			}
		} else if d.cloud != nil && d.cloud.AuthProvider != nil && strings.EqualFold(useDataPlaneAPI, oauth) {
			fileClient, err = newAzureFileClientWithOAuth(d.cloud.AuthProvider.GetAzIdentity(), accountOptions.Name, d.getStorageEndPointSuffix())
		} else {
			fileClient, err = newAzureFileMgmtClient(d.cloud, accountOptions)
		}
		if err != nil {
			return true, err
		}

		if err = fileClient.CreateFileShare(ctx, shareOptions); err != nil {
			if strings.Contains(err.Error(), "ShareAlreadyExists") {
				klog.Warningf("CreateFileShare(%s) on account(%s) failed with error(%v), return as success", shareOptions.Name, accountOptions.Name, err)
				return true, nil
			}
			if isRetriableError(err) {
				klog.Warningf("CreateFileShare(%s) on account(%s) failed with error(%v), waiting for retrying", shareOptions.Name, accountOptions.Name, err)
				sleepIfThrottled(err, fileOpThrottlingSleepSec)
				return false, nil
			}
			klog.Errorf("CreateFileShare(%s) on account(%s) failed with error(%v)", shareOptions.Name, accountOptions.Name, err)
		}
		return true, err
	})
}

// DeleteFileShare deletes a file share using storage account name and key
func (d *Driver) DeleteFileShare(ctx context.Context, subsID, resourceGroup, accountName, shareName string, secrets map[string]string, useDataPlaneAPI string) error {
	return wait.ExponentialBackoff(getBackOff(d.cloud.Config), func() (bool, error) {
		var err error
		if len(secrets) > 0 {
			accountName, accountKey, rerr := getStorageAccount(secrets)
			if rerr != nil {
				return true, rerr
			}
			fileClient, rerr := newAzureFileClient(accountName, accountKey, d.getStorageEndPointSuffix())
			if rerr != nil {
				return true, rerr
			}
			err = fileClient.DeleteFileShare(ctx, shareName)
		} else if d.cloud != nil && d.cloud.AuthProvider != nil && strings.EqualFold(useDataPlaneAPI, oauth) {
			fileClient, rerr := newAzureFileClientWithOAuth(d.cloud.AuthProvider.GetAzIdentity(), accountName, d.getStorageEndPointSuffix())
			if rerr != nil {
				return true, rerr
			}
			err = fileClient.DeleteFileShare(ctx, shareName)
		} else {
			fileClient, errGetClient := d.getFileShareClientForSub(subsID)
			if errGetClient != nil {
				return true, errGetClient
			}
			err = fileClient.Delete(ctx, resourceGroup, accountName, shareName, nil)
		}

		if err != nil {
			if strings.Contains(err.Error(), statusCodeNotFound) ||
				strings.Contains(err.Error(), httpCodeNotFound) ||
				strings.Contains(err.Error(), fileShareNotFound) {
				klog.Warningf("DeleteFileShare(%s) on account(%s) failed with error(%v), return as success", shareName, accountName, err)
				return true, nil
			}
		}

		if isRetriableError(err) {
			klog.Warningf("DeleteFileShare(%s) on account(%s) failed with error(%v), waiting for retrying", shareName, accountName, err)
			if isThrottlingError(err) {
				klog.Warningf("switch to use data plane API instead for account %s since it's throttled", accountName)
				d.dataPlaneAPIAccountCache.Set(accountName, "")
				return true, err
			}
			return false, nil
		}

		return true, err
	})
}

// ResizeFileShare resizes a file share
func (d *Driver) ResizeFileShare(ctx context.Context, subsID, resourceGroup, accountName, shareName string, sizeGiB int, secrets map[string]string, useDataPlaneAPI string) error {
	return wait.ExponentialBackoff(getBackOff(d.cloud.Config), func() (bool, error) {
		var err error
		if len(secrets) > 0 {
			accountName, accountKey, rerr := getStorageAccount(secrets)
			if rerr != nil {
				return true, rerr
			}
			fileClient, rerr := newAzureFileClient(accountName, accountKey, d.getStorageEndPointSuffix())
			if rerr != nil {
				return true, rerr
			}
			err = fileClient.ResizeFileShare(ctx, shareName, sizeGiB)
		} else if d.cloud != nil && d.cloud.AuthProvider != nil && strings.EqualFold(useDataPlaneAPI, oauth) {
			fileClient, rerr := newAzureFileClientWithOAuth(d.cloud.AuthProvider.GetAzIdentity(), accountName, d.getStorageEndPointSuffix())
			if rerr != nil {
				return true, rerr
			}
			err = fileClient.ResizeFileShare(ctx, shareName, sizeGiB)
		} else {
			fileClient, rerr := d.getFileShareClientForSub(subsID)
			if rerr != nil {
				return true, rerr
			}
			fileShare, rerr := fileClient.Get(ctx, resourceGroup, accountName, shareName, nil)
			if rerr != nil {
				return true, rerr
			}
			if ptr.Deref(fileShare.FileShareProperties.ShareQuota, 0) >= int32(sizeGiB) {
				klog.Warningf("file share size(%dGi) is already greater or equal than requested size(%dGi), accountName: %s, shareName: %s",
					ptr.Deref(fileShare.FileShareProperties.ShareQuota, 0), sizeGiB, accountName, shareName)
				return true, nil
			}
			fileShare.FileShareProperties.ShareQuota = to.Ptr(int32(sizeGiB))
			_, err = fileClient.Update(ctx, resourceGroup, accountName, shareName, *fileShare)
		}
		if isRetriableError(err) {
			klog.Warningf("ResizeFileShare(%s) on account(%s) with new size(%d) failed with error(%v), waiting for retrying", shareName, accountName, sizeGiB, err)
			sleepIfThrottled(err, fileOpThrottlingSleepSec)
			return false, nil
		}
		return true, err
	})
}

// copyFileShare copies a fileshare, if dstAccountName is empty, then copy in the same account
func (d *Driver) copyFileShare(ctx context.Context, req *csi.CreateVolumeRequest, dstAccountName string, dstAccountSasToken string, authAzcopyEnv []string, secretNamespace string, shareOptions *ShareOptions, accountOptions *storage.AccountOptions, storageEndpointSuffix string) error {
	var sourceVolumeID string
	if req.GetVolumeContentSource() != nil && req.GetVolumeContentSource().GetVolume() != nil {
		sourceVolumeID = req.GetVolumeContentSource().GetVolume().GetVolumeId()
	}
	srcResourceGroupName, srcAccountName, srcFileShareName, _, _, srcSubscriptionID, err := GetFileShareInfo(sourceVolumeID) //nolint:dogsled
	if err != nil {
		return status.Error(codes.NotFound, err.Error())
	}
	if dstAccountName == "" {
		dstAccountName = srcAccountName
	}
	dstFileShareName := shareOptions.Name
	if srcAccountName == "" || srcFileShareName == "" || dstFileShareName == "" {
		return fmt.Errorf("one or more of srcAccountName(%s), srcFileShareName(%s), dstFileShareName(%s) are empty", srcAccountName, srcFileShareName, dstFileShareName)
	}
	srcAccountSasToken := dstAccountSasToken
	if srcAccountName != dstAccountName && dstAccountSasToken != "" {
		srcAccountOptions := &storage.AccountOptions{
			Name:                srcAccountName,
			ResourceGroup:       srcResourceGroupName,
			SubscriptionID:      srcSubscriptionID,
			GetLatestAccountKey: accountOptions.GetLatestAccountKey,
		}
		if srcAccountSasToken, _, err = d.getAzcopyAuth(ctx, srcAccountName, "", storageEndpointSuffix, srcAccountOptions, nil, "", secretNamespace, true); err != nil {
			return err
		}
	}

	srcPath := fmt.Sprintf("https://%s.file.%s/%s%s", srcAccountName, storageEndpointSuffix, srcFileShareName, srcAccountSasToken)
	dstPath := fmt.Sprintf("https://%s.file.%s/%s%s", dstAccountName, storageEndpointSuffix, dstFileShareName, dstAccountSasToken)

	return d.copyFileShareByAzcopy(ctx, srcFileShareName, dstFileShareName, srcPath, dstPath, "", srcAccountName, dstAccountName, srcAccountSasToken, authAzcopyEnv, shareOptions, accountOptions)
}

// GetTotalAccountQuota returns the total quota in GB of all file shares in the storage account and the number of file shares
func (d *Driver) GetTotalAccountQuota(ctx context.Context, subsID, resourceGroup, accountName string) (int32, int32, error) {
	fileClient, err := d.getFileShareClientForSub(subsID)
	if err != nil {
		return -1, -1, err
	}
	fileshares, err := fileClient.List(ctx, resourceGroup, accountName, nil)
	if err != nil {
		return -1, -1, err
	}
	var totalQuotaGB int32
	for _, fs := range fileshares {
		if fs.Properties.ShareQuota != nil {
			totalQuotaGB += *fs.Properties.ShareQuota
		}
	}
	return totalQuotaGB, int32(len(fileshares)), nil
}

// RemoveStorageAccountTag remove tag from storage account
func (d *Driver) RemoveStorageAccountTag(ctx context.Context, subsID, resourceGroup, account, key string) error {
	if d.cloud == nil {
		return fmt.Errorf("cloud or StorageAccountClient is nil")
	}
	// search in cache first
	cache, err := d.skipMatchingTagCache.Get(ctx, account, azcache.CacheReadTypeDefault)
	if err != nil {
		return err
	}
	if cache != nil {
		klog.V(6).Infof("skip remove tag(%s) on account(%s) subsID(%s) resourceGroup(%s) since tag is added or removed in a short time", key, account, subsID, resourceGroup)
		return nil
	}

	klog.V(2).Infof("remove tag(%s) on account(%s) subsID(%s), resourceGroup(%s)", key, account, subsID, resourceGroup)
	defer d.skipMatchingTagCache.Set(account, "")
	if rerr := d.cloud.RemoveStorageAccountTag(ctx, subsID, resourceGroup, account, key); rerr != nil {
		return rerr
	}
	return nil
}

// GetStorageAccesskey get Azure storage account key from
//  1. secrets (if not empty)
//  2. use k8s client identity to read from k8s secret
//  3. use cluster identity to get from storage account directly
func (d *Driver) GetStorageAccesskey(ctx context.Context, accountOptions *storage.AccountOptions, secrets map[string]string, secretName, secretNamespace string) (string, error) {
	if len(secrets) > 0 {
		_, accountKey, err := getStorageAccount(secrets)
		return accountKey, err
	}

	accountName := accountOptions.Name
	// read account key from cache first
	cache, err := d.accountCacheMap.Get(ctx, accountName, azcache.CacheReadTypeDefault)
	if err != nil {
		return "", err
	}
	if cache != nil {
		return cache.(string), nil
	}

	// read from k8s secret first
	if secretName == "" {
		secretName = fmt.Sprintf(secretNameTemplate, accountName)
	}
	_, accountKey, err := d.GetStorageAccountFromSecret(ctx, secretName, secretNamespace)
	if err != nil {
		klog.V(2).Infof("could not get account(%s) key from secret(%s), error: %v, use cluster identity to get account key instead", accountOptions.Name, secretName, err)
		accountKey, err = d.GetStorageAccesskeyWithSubsID(ctx, accountOptions.SubscriptionID, accountOptions.Name, accountOptions.ResourceGroup, accountOptions.GetLatestAccountKey)
	}

	if err == nil && accountKey != "" {
		d.accountCacheMap.Set(accountName, accountKey)
	}
	return accountKey, err
}

// GetStorageAccesskeyWithSubsID get Azure storage account key from storage account directly
func (d *Driver) GetStorageAccesskeyWithSubsID(ctx context.Context, subsID, account, resourceGroup string, getLatestAccountKey bool) (string, error) {
	if d.cloud == nil || d.cloud.ComputeClientFactory == nil {
		return "", fmt.Errorf("could not get account key: cloud or ComputeClientFactory is nil")
	}
	accountClient, err := d.cloud.ComputeClientFactory.GetAccountClientForSub(subsID)
	if err != nil {
		return "", err
	}
	return d.cloud.GetStorageAccesskey(ctx, accountClient, account, resourceGroup, getLatestAccountKey)
}

// GetStorageAccountFromSecret get storage account key from k8s secret
// return <accountName, accountKey, error>
func (d *Driver) GetStorageAccountFromSecret(ctx context.Context, secretName, secretNamespace string) (string, string, error) {
	if d.kubeClient == nil {
		return "", "", fmt.Errorf("could not get account key from secret(%s): KubeClient is nil", secretName)
	}

	secret, err := d.kubeClient.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("could not get secret(%v): %v", secretName, err)
	}

	accountName := strings.TrimSpace(string(secret.Data[defaultSecretAccountName][:]))
	accountKey := strings.TrimSpace(string(secret.Data[defaultSecretAccountKey][:]))
	return accountName, accountKey, nil
}

// getSubnetResourceID get default subnet resource ID from cloud provider config
func (d *Driver) getSubnetResourceID(vnetResourceGroup, vnetName, subnetName string) string {
	subsID := d.cloud.SubscriptionID
	if len(d.cloud.NetworkResourceSubscriptionID) > 0 {
		subsID = d.cloud.NetworkResourceSubscriptionID
	}

	if len(vnetResourceGroup) == 0 {
		vnetResourceGroup = d.cloud.ResourceGroup
		if len(d.cloud.VnetResourceGroup) > 0 {
			vnetResourceGroup = d.cloud.VnetResourceGroup
		}
	}

	if len(vnetName) == 0 {
		vnetName = d.cloud.VnetName
	}

	if len(subnetName) == 0 {
		subnetName = d.cloud.SubnetName
	}
	return fmt.Sprintf(subnetTemplate, subsID, vnetResourceGroup, vnetName, subnetName)
}

func (d *Driver) useDataPlaneAPI(ctx context.Context, volumeID, accountName string) string {
	v, useDataPlaneAPI := d.dataPlaneAPIVolMap.Load(volumeID)
	if useDataPlaneAPI {
		return v.(string)
	}

	cache, err := d.dataPlaneAPIAccountCache.Get(ctx, accountName, azcache.CacheReadTypeDefault)
	if err != nil {
		klog.Errorf("get(%s) from dataPlaneAPIAccountCache failed with error: %v", accountName, err)
		return ""
	}
	if cache != nil {
		return cache.(string)
	}
	return ""
}

func (d *Driver) SetAzureCredentials(ctx context.Context, accountName, accountKey, secretName, secretNamespace string) (string, error) {
	if d.kubeClient == nil {
		klog.Warningf("could not create secret: kubeClient is nil")
		return "", nil
	}
	if accountName == "" || accountKey == "" {
		return "", fmt.Errorf("the account info is not enough, accountName(%v), accountKey(%v)", accountName, accountKey)
	}
	if secretName == "" {
		secretName = fmt.Sprintf(secretNameTemplate, accountName)
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: secretNamespace,
			Name:      secretName,
		},
		Data: map[string][]byte{
			defaultSecretAccountName: []byte(accountName),
			defaultSecretAccountKey:  []byte(accountKey),
		},
		Type: "Opaque",
	}
	_, err := d.kubeClient.CoreV1().Secrets(secretNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		err = nil
	}
	if err != nil {
		return "", fmt.Errorf("couldn't create secret %v", err)
	}
	return secretName, err
}

func (d *Driver) getStorageEndPointSuffix() string {
	if d.cloud == nil || d.cloud.Environment == nil || d.cloud.Environment.StorageEndpointSuffix == "" {
		return defaultStorageEndPointSuffix
	}
	return d.cloud.Environment.StorageEndpointSuffix
}

func (d *Driver) getFileShareClientForSub(subscriptionID string) (fileshareclient.Interface, error) {
	if d.cloud == nil || d.cloud.ComputeClientFactory == nil {
		return nil, fmt.Errorf("cloud or ComputeClientFactory is nil")
	}
	return d.cloud.ComputeClientFactory.GetFileShareClientForSub(subscriptionID)
}

func isKataNode(ctx context.Context, nodeID, confidentialContainerLabel string, kubeClient clientset.Interface) bool {
	if nodeID == "" {
		return false
	}

	if kubeClient == nil || kubeClient.CoreV1() == nil {
		klog.Warningf("kubeClient is nil, cannot check if node(%s) is a kata node", nodeID)
		return false
	}

	node, err := kubeClient.CoreV1().Nodes().Get(ctx, nodeID, metav1.GetOptions{})
	if err != nil {
		klog.Warningf("failed to get node(%s): %v", nodeID, err)
		return false
	}

	if node == nil || len(node.Labels) == 0 {
		return false
	}

	// Check for the kata isolation labels
	if _, ok := node.Labels[confidentialContainerLabel]; !ok {
		return false
	}
	klog.V(4).Infof("node(%s) is a kata node with labels: %v", nodeID, node.Labels)
	return true
}

// createFolderIfNotExists creates a folder in Azure File Share if it doesn't already exist
// This function handles nested paths by creating each directory level recursively
func (d *Driver) createFolderIfNotExists(ctx context.Context, accountName, accountKey, fileShareName, folderName, storageEndpointSuffix string) error {
	fileClient, err := newAzureFileClient(accountName, accountKey, storageEndpointSuffix)
	if err != nil || fileClient.(*azureFileDataplaneClient).Client == nil {
		return fmt.Errorf("create Azure File client(%s) failed: %v", accountName, err)
	}

	shareClient := fileClient.(*azureFileDataplaneClient).Client.NewShareClient(fileShareName)

	// Performance optimization: First check if the complete directory structure already exists
	// This is the most common case and avoids unnecessary recursive checking
	fullPathClient := shareClient.NewDirectoryClient(folderName)
	_, err = fullPathClient.GetProperties(ctx, nil)
	if err == nil {
		// Complete directory structure already exists - fast path
		klog.V(2).Infof("Folder path %s already exists in share %s", folderName, fileShareName)
		return nil
	}

	// Check if the error is something other than "not found"
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		// Some other error occurred (permissions, network, etc.)
		return fmt.Errorf("failed to check if folder path %s exists: %w", folderName, err)
	}

	// Directory doesn't exist - need to create it recursively
	// Split the path by '/' and create directories level by level
	pathComponents := strings.Split(strings.Trim(folderName, "/"), "/")

	// Build path incrementally and create each directory level
	currentPath := ""
	for i, component := range pathComponents {
		if component == "" {
			continue // Skip empty components
		}

		if i > 0 {
			currentPath += "/"
		}
		currentPath += component

		// Get directory client for the current path level
		directoryClient := shareClient.NewDirectoryClient(currentPath)

		// Check if directory already exists by trying to get its properties
		if _, err = directoryClient.GetProperties(ctx, nil); err == nil {
			// Directory already exists, continue to next level
			klog.V(4).Infof("Directory %s already exists in share %s", currentPath, fileShareName)
			continue
		}

		// Check if the error is because directory doesn't exist
		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
			// Some other error occurred
			return fmt.Errorf("failed to check if directory %s exists: %w", currentPath, err)
		}

		// Create the directory at this level
		if _, err = directoryClient.Create(ctx, nil); err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == 409 {
				// Directory already exists (race condition), which is fine
				klog.V(4).Infof("Directory %s already exists in share %s (detected during creation)", currentPath, fileShareName)
				continue
			}
			return fmt.Errorf("failed to create directory %s: %w", currentPath, err)
		}

		klog.V(2).Infof("Successfully created directory %s in share %s", currentPath, fileShareName)
	}

	klog.V(2).Infof("Successfully ensured folder path %s exists in share %s", folderName, fileShareName)
	return nil
}
