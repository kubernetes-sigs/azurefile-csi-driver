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
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2021-09-01/storage"
	"github.com/Azure/azure-storage-file-go/azfile"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/pborman/uuid"
	"github.com/rubiojr/go-vhd/vhd"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/volume/util"
	mount "k8s.io/mount-utils"

	csicommon "sigs.k8s.io/azurefile-csi-driver/pkg/csi-common"
	"sigs.k8s.io/azurefile-csi-driver/pkg/mounter"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient"
	azcache "sigs.k8s.io/cloud-provider-azure/pkg/cache"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
	"sigs.k8s.io/cloud-provider-azure/pkg/retry"
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
	mfsymlinks         = "mfsymlinks"
	defaultFileMode    = "0777"
	defaultDirMode     = "0777"
	defaultActimeo     = "30"

	// See https://docs.microsoft.com/en-us/rest/api/storageservices/naming-and-referencing-shares--directories--files--and-metadata#share-names
	fileShareNameMinLength = 3
	fileShareNameMaxLength = 63

	minimumPremiumShareSize = 100 // GB
	// Minimum size of Azure Premium Files is 100GiB
	// See https://docs.microsoft.com/en-us/azure/storage/files/storage-files-planning#provisioned-shares
	defaultAzureFileQuota = 100

	// key of snapshot name in metadata
	snapshotNameKey = "initiator"

	shareNameField                    = "sharename"
	accessTierField                   = "accesstier"
	shareAccessTierField              = "shareaccesstier"
	accountAccessTierField            = "accountaccesstier"
	rootSquashTypeField               = "rootsquashtype"
	diskNameField                     = "diskname"
	folderNameField                   = "foldername"
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
	useSecretCacheField               = "usesecretcache"
	getAccountKeyFromSecretField      = "getaccountkeyfromsecret"
	disableDeleteRetentionPolicyField = "disabledeleteretentionpolicy"
	allowBlobPublicAccessField        = "allowblobpublicaccess"
	storageEndpointSuffixField        = "storageendpointsuffix"
	fsGroupChangePolicyField          = "fsgroupchangepolicy"
	ephemeralField                    = "csi.storage.k8s.io/ephemeral"
	podNamespaceField                 = "csi.storage.k8s.io/pod.namespace"
	mountOptionsField                 = "mountoptions"
	mountPermissionsField             = "mountpermissions"
	falseValue                        = "false"
	trueValue                         = "true"
	defaultSecretAccountName          = "azurestorageaccountname"
	defaultSecretAccountKey           = "azurestorageaccountkey"
	proxyMount                        = "proxy-mount"
	cifs                              = "cifs"
	smb                               = "smb"
	nfs                               = "nfs"
	ext4                              = "ext4"
	ext3                              = "ext3"
	ext2                              = "ext2"
	xfs                               = "xfs"
	vhdSuffix                         = ".vhd"
	metaDataNode                      = "node"
	networkEndpointTypeField          = "networkendpointtype"
	vnetResourceGroupField            = "vnetresourcegroup"
	vnetNameField                     = "vnetname"
	subnetNameField                   = "subnetname"
	shareNamePrefixField              = "sharenameprefix"
	requireInfraEncryptionField       = "requireinfraencryption"
	premium                           = "premium"

	accountNotProvisioned = "StorageAccountIsNotProvisioned"
	// this is a workaround fix for 429 throttling issue, will update cloud provider for better fix later
	tooManyRequests   = "TooManyRequests"
	shareBeingDeleted = "The specified share is being deleted"
	clientThrottled   = "client throttled"
	// accountLimitExceed returned by different API
	accountLimitExceedManagementAPI = "TotalSharesProvisionedCapacityExceedsAccountLimit"
	accountLimitExceedDataPlaneAPI  = "specified share does not exist"

	fileShareNotFound  = "ErrorCode=ShareNotFound"
	statusCodeNotFound = "StatusCode=404"
	httpCodeNotFound   = "HTTPStatusCode: 404"

	// define different sleep time when hit throttling
	accountOpThrottlingSleepSec = 16
	fileOpThrottlingSleepSec    = 180

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
)

var (
	supportedFsTypeList              = []string{cifs, smb, nfs, ext4, ext3, ext2, xfs}
	supportedProtocolList            = []string{smb, nfs}
	supportedDiskFsTypeList          = []string{ext4, ext3, ext2, xfs}
	supportedFSGroupChangePolicyList = []string{FSGroupChangeNone, string(v1.FSGroupChangeAlways), string(v1.FSGroupChangeOnRootMismatch)}

	retriableErrors = []string{accountNotProvisioned, tooManyRequests, shareBeingDeleted, clientThrottled}
)

// DriverOptions defines driver parameters specified in driver deployment
type DriverOptions struct {
	NodeID                                 string
	DriverName                             string
	CloudConfigSecretName                  string
	CloudConfigSecretNamespace             string
	CustomUserAgent                        string
	UserAgentSuffix                        string
	AllowEmptyCloudConfig                  bool
	AllowInlineVolumeKeyAccessWithIdentity bool
	EnableVHDDiskFeature                   bool
	EnableGetVolumeStats                   bool
	MountPermissions                       uint64
	FSGroupChangePolicy                    string
	KubeAPIQPS                             float64
	KubeAPIBurst                           int
}

// Driver implements all interfaces of CSI drivers
type Driver struct {
	csicommon.CSIDriver
	cloud                                  *azure.Cloud
	cloudConfigSecretName                  string
	cloudConfigSecretNamespace             string
	customUserAgent                        string
	userAgentSuffix                        string
	fsGroupChangePolicy                    string
	allowEmptyCloudConfig                  bool
	allowInlineVolumeKeyAccessWithIdentity bool
	enableVHDDiskFeature                   bool
	enableGetVolumeStats                   bool
	mountPermissions                       uint64
	kubeAPIQPS                             float64
	kubeAPIBurst                           int
	fileClient                             *azureFileClient
	mounter                                *mount.SafeFormatAndMount
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
	accountCacheMap *azcache.TimedCache
	// a map storing all secret names created by this driver <secretCacheKey, "">
	secretCacheMap *azcache.TimedCache
	// a map storing all volumes using data plane API <volumeID, "">
	dataPlaneAPIVolMap sync.Map
	// a timed cache storing all storage accounts that are using data plane API temporarily
	dataPlaneAPIAccountCache *azcache.TimedCache
	// a timed cache storing account search history (solve account list throttling issue)
	accountSearchCache *azcache.TimedCache
	// a timed cache storing tag removing history (solve account update throttling issue)
	removeTagCache *azcache.TimedCache
}

// NewDriver Creates a NewCSIDriver object. Assumes vendor version is equal to driver version &
// does not support optional driver plugin info manifest field. Refer to CSI spec for more details.
func NewDriver(options *DriverOptions) *Driver {
	driver := Driver{}
	driver.Name = options.DriverName
	driver.Version = driverVersion
	driver.NodeID = options.NodeID
	driver.cloudConfigSecretName = options.CloudConfigSecretName
	driver.cloudConfigSecretNamespace = options.CloudConfigSecretNamespace
	driver.customUserAgent = options.CustomUserAgent
	driver.userAgentSuffix = options.UserAgentSuffix
	driver.allowEmptyCloudConfig = options.AllowEmptyCloudConfig
	driver.allowInlineVolumeKeyAccessWithIdentity = options.AllowInlineVolumeKeyAccessWithIdentity
	driver.enableVHDDiskFeature = options.EnableVHDDiskFeature
	driver.enableGetVolumeStats = options.EnableGetVolumeStats
	driver.mountPermissions = options.MountPermissions
	driver.fsGroupChangePolicy = options.FSGroupChangePolicy
	driver.kubeAPIQPS = options.KubeAPIQPS
	driver.kubeAPIBurst = options.KubeAPIBurst
	driver.volLockMap = newLockMap()
	driver.subnetLockMap = newLockMap()
	driver.volumeLocks = newVolumeLocks()

	var err error
	getter := func(key string) (interface{}, error) { return nil, nil }

	if driver.secretCacheMap, err = azcache.NewTimedcache(time.Minute, getter); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.accountSearchCache, err = azcache.NewTimedcache(time.Minute, getter); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.removeTagCache, err = azcache.NewTimedcache(3*time.Minute, getter); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.accountCacheMap, err = azcache.NewTimedcache(3*time.Minute, getter); err != nil {
		klog.Fatalf("%v", err)
	}

	if driver.dataPlaneAPIAccountCache, err = azcache.NewTimedcache(10*time.Minute, getter); err != nil {
		klog.Fatalf("%v", err)
	}

	return &driver
}

// Run driver initialization
func (d *Driver) Run(endpoint, kubeconfig string, testBool bool) {
	versionMeta, err := GetVersionYAML(d.Name)
	if err != nil {
		klog.Fatalf("%v", err)
	}
	klog.Infof("\nDRIVER INFORMATION:\n-------------------\n%s\n\nStreaming logs below:", versionMeta)

	userAgent := GetUserAgent(d.Name, d.customUserAgent, d.userAgentSuffix)
	klog.V(2).Infof("driver userAgent: %s", userAgent)
	d.cloud, err = getCloudProvider(kubeconfig, d.NodeID, d.cloudConfigSecretName, d.cloudConfigSecretNamespace, userAgent, d.allowEmptyCloudConfig, d.kubeAPIQPS, d.kubeAPIBurst)
	if err != nil {
		klog.Fatalf("failed to get Azure Cloud Provider, error: %v", err)
	}
	klog.V(2).Infof("cloud: %s, location: %s, rg: %s, VnetName: %s, VnetResourceGroup: %s, SubnetName: %s", d.cloud.Cloud, d.cloud.Location, d.cloud.ResourceGroup, d.cloud.VnetName, d.cloud.VnetResourceGroup, d.cloud.SubnetName)

	// todo: set backoff from cloud provider config
	d.fileClient = newAzureFileClient(&d.cloud.Environment, &retry.Backoff{Steps: 1})

	d.mounter, err = mounter.NewSafeMounter()
	if err != nil {
		klog.Fatalf("Failed to get safe mounter. Error: %v", err)
	}

	// Initialize default library driver
	d.AddControllerServiceCapabilities(
		[]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
			//csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
			csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
			csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
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
		csi.NodeServiceCapability_RPC_VOLUME_MOUNT_GROUP,
	}
	if d.enableGetVolumeStats {
		nodeCap = append(nodeCap, csi.NodeServiceCapability_RPC_GET_VOLUME_STATS)
	}
	d.AddNodeServiceCapabilities(nodeCap)

	s := csicommon.NewNonBlockingGRPCServer()
	// Driver d act as IdentityServer, ControllerServer and NodeServer
	s.Start(endpoint, d, d, d, testBool)
	s.Wait()
}

// getFileShareQuota return (-1, nil) means file share does not exist
func (d *Driver) getFileShareQuota(ctx context.Context, subsID, resourceGroupName, accountName, fileShareName string, secrets map[string]string) (int, error) {
	if len(secrets) > 0 {
		accountName, accountKey, err := getStorageAccount(secrets)
		if err != nil {
			return -1, err
		}
		fileClient, err := d.fileClient.getFileSvcClient(accountName, accountKey)
		if err != nil {
			return -1, err
		}
		share := fileClient.GetShareReference(fileShareName)
		exists, err := share.Exists()
		if err != nil {
			return -1, err
		}
		if !exists {
			return -1, nil
		}
		return share.Properties.Quota, nil
	}

	fileShare, err := d.cloud.GetFileShare(ctx, subsID, resourceGroupName, accountName, fileShareName)
	if err != nil {
		if strings.Contains(err.Error(), "ShareNotFound") {
			return -1, nil
		}
		return -1, err
	}

	if fileShare.FileShareProperties == nil || fileShare.FileShareProperties.ShareQuota == nil {
		return -1, fmt.Errorf("FileShareProperties or FileShareProperties.ShareQuota is nil")
	}
	return int(*fileShare.FileShareProperties.ShareQuota), nil
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

// check whether mountOptions contains file_mode, dir_mode, vers, if not, append default mode
func appendDefaultMountOptions(mountOptions []string) []string {
	var defaultMountOptions = map[string]string{
		fileMode:   defaultFileMode,
		dirMode:    defaultDirMode,
		actimeo:    defaultActimeo,
		mfsymlinks: "",
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

	/* todo: looks like fsGroup is not included in CSI
	if !gidFlag && fsGroup != nil {
		allMountOptions = append(allMountOptions, fmt.Sprintf("%s=%d", gid, *fsGroup))
	}
	*/
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
		return "", "", fmt.Errorf("could not find accountname or azurestorageaccountname field secrets(%v)", secrets)
	}
	if accountKey == "" {
		return "", "", fmt.Errorf("could not find accountkey or azurestorageaccountkey field in secrets(%v)", secrets)
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
		fileShareName = util.GenerateVolumeName("pvc-file", uuid.NewUUID().String(), fileShareNameMaxLength)
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

// get snapshot name according to snapshot id, e.g.
// input: "rg#f5713de20cde511e8ba4900#csivolumename#diskname#2019-08-22T07:17:53.0000000Z"
// output: 2019-08-22T07:17:53.0000000Z (last element)
func getSnapshot(id string) (string, error) {
	segments := strings.Split(id, separator)
	if len(segments) < 5 {
		return "", fmt.Errorf("error parsing volume id: %q, should at least contain four #", id)
	}
	return segments[len(segments)-1], nil
}

func getFileURL(accountName, accountKey, storageEndpointSuffix, fileShareName, diskName string) (*azfile.FileURL, error) {
	credential, err := azfile.NewSharedKeyCredential(accountName, accountKey)
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
	po := azfile.PipelineOptions{
		// Set RetryOptions to control how HTTP request are retried when retryable failures occur
		Retry: azfile.RetryOptions{
			Policy:        azfile.RetryPolicyExponential, // Use exponential backoff as opposed to linear
			MaxTries:      3,                             // Try at most 3 times to perform the operation (set to 1 to disable retries)
			TryTimeout:    time.Second * 3,               // Maximum time allowed for any single try
			RetryDelay:    time.Second * 1,               // Backoff amount for each retry (exponential or linear)
			MaxRetryDelay: time.Second * 3,               // Max delay between retries
		},
	}
	fileURL := azfile.NewFileURL(*u, azfile.NewPipeline(credential, po))
	return &fileURL, nil
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

	fileURL, err := getFileURL(accountName, accountKey, storageEndpointSuffix, fileShareName, diskName)
	if err != nil {
		return err
	}
	if fileURL == nil {
		return fmt.Errorf("getFileURL(%s,%s,%s,%s) return empty fileURL", accountName, storageEndpointSuffix, fileShareName, diskName)
	}
	if _, err = fileURL.Create(ctx, diskSizeBytes, azfile.FileHTTPHeaders{}, azfile.Metadata{}); err != nil {
		return err
	}
	if _, err = fileURL.UploadRange(ctx, end-start, bytes.NewReader(headerBytes[:vhd.VHD_HEADER_SIZE]), nil); err != nil {
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
		klog.Warningf("parsing volumeID(%s) return with error: %v", volumeID, err)
		err = nil
	}

	var protocol, accountKey, secretName, pvcNamespace string
	// indicates whether get account key only from k8s secret
	getAccountKeyFromSecret := false

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
		}
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

	if len(secrets) == 0 {
		// read account key from cache first
		cache, errCache := d.accountCacheMap.Get(accountName, azcache.CacheReadTypeDefault)
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
				name, accountKey, err = d.GetStorageAccountFromSecret(ctx, secretName, secretNamespace)
				if name != "" {
					accountName = name
				}
				if err != nil {
					klog.Warningf("GetStorageAccountFromSecret(%s, %s) failed with error: %v", secretName, secretNamespace, err)
					if !getAccountKeyFromSecret && d.cloud.StorageAccountClient != nil && accountName != "" {
						klog.V(2).Infof("use cluster identity to get account key from (%s, %s, %s)", subsID, rgName, accountName)
						accountKey, err = d.cloud.GetStorageAccesskey(ctx, subsID, accountName, rgName)
						if err != nil {
							klog.Errorf("GetStorageAccesskey(%s, %s, %s) failed with error: %v", subsID, rgName, accountName, err)
						}
					}
				}
			}
		}
	} else {
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
	for _, tier := range storage.PossibleShareAccessTierValues() {
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
	for _, tier := range storage.PossibleAccessTierValues() {
		if accessTier == string(tier) {
			return true
		}
	}
	return false
}

func isSupportedRootSquashType(rootSquashType string) bool {
	if rootSquashType == "" {
		return true
	}
	for _, tier := range storage.PossibleRootSquashTypeValues() {
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
func (d *Driver) CreateFileShare(ctx context.Context, accountOptions *azure.AccountOptions, shareOptions *fileclient.ShareOptions, secrets map[string]string) error {
	return wait.ExponentialBackoff(d.cloud.RequestBackoff(), func() (bool, error) {
		var err error
		if len(secrets) > 0 {
			accountName, accountKey, rerr := getStorageAccount(secrets)
			if rerr != nil {
				return true, rerr
			}
			err = d.fileClient.CreateFileShare(accountName, accountKey, shareOptions)
		} else {
			_, err = d.cloud.FileClient.WithSubscriptionID(accountOptions.SubscriptionID).CreateFileShare(ctx, accountOptions.ResourceGroup, accountOptions.Name, shareOptions, "")
		}
		if isRetriableError(err) {
			klog.Warningf("CreateFileShare(%s) on account(%s) failed with error(%v), waiting for retrying", shareOptions.Name, accountOptions.Name, err)
			sleepIfThrottled(err, fileOpThrottlingSleepSec)
			return false, nil
		}
		return true, err
	})
}

// DeleteFileShare deletes a file share using storage account name and key
func (d *Driver) DeleteFileShare(ctx context.Context, subsID, resourceGroup, accountName, shareName string, secrets map[string]string) error {
	return wait.ExponentialBackoff(d.cloud.RequestBackoff(), func() (bool, error) {
		var err error
		if len(secrets) > 0 {
			accountName, accountKey, rerr := getStorageAccount(secrets)
			if rerr != nil {
				return true, rerr
			}
			err = d.fileClient.deleteFileShare(accountName, accountKey, shareName)
		} else {
			err = d.cloud.DeleteFileShare(ctx, subsID, resourceGroup, accountName, shareName)
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
			if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tooManyRequests)) {
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
func (d *Driver) ResizeFileShare(ctx context.Context, subsID, resourceGroup, accountName, shareName string, sizeGiB int, secrets map[string]string) error {
	return wait.ExponentialBackoff(d.cloud.RequestBackoff(), func() (bool, error) {
		var err error
		if len(secrets) > 0 {
			accountName, accountKey, rerr := getStorageAccount(secrets)
			if rerr != nil {
				return true, rerr
			}
			err = d.fileClient.resizeFileShare(accountName, accountKey, shareName, sizeGiB)
		} else {
			err = d.cloud.ResizeFileShare(ctx, subsID, resourceGroup, accountName, shareName, sizeGiB)
		}
		if isRetriableError(err) {
			klog.Warningf("ResizeFileShare(%s) on account(%s) with new size(%d) failed with error(%v), waiting for retrying", shareName, accountName, sizeGiB, err)
			sleepIfThrottled(err, fileOpThrottlingSleepSec)
			return false, nil
		}
		return true, err
	})
}

// RemoveStorageAccountTag remove tag from storage account
func (d *Driver) RemoveStorageAccountTag(ctx context.Context, subsID, resourceGroup, account, key string) error {
	// search in cache first
	cache, err := d.removeTagCache.Get(account, azcache.CacheReadTypeDefault)
	if err != nil {
		return err
	}
	if cache != nil {
		klog.V(6).Infof("skip remove tag(%s) on account(%s) subsID(%s) resourceGroup(%s) since tag already removed in a short time", key, account, subsID, resourceGroup)
		return nil
	}

	klog.V(2).Infof("remove tag(%s) on account(%s) subsID(%s), resourceGroup(%s)", key, account, subsID, resourceGroup)
	defer d.removeTagCache.Set(account, key)
	if rerr := d.cloud.RemoveStorageAccountTag(ctx, subsID, resourceGroup, account, key); rerr != nil {
		return rerr.Error()
	}
	return nil
}

// GetStorageAccesskey get Azure storage account key from
//  1. secrets (if not empty)
//  2. use k8s client identity to read from k8s secret
//  3. use cluster identity to get from storage account directly
func (d *Driver) GetStorageAccesskey(ctx context.Context, accountOptions *azure.AccountOptions, secrets map[string]string, secretName, secretNamespace string) (string, error) {
	if len(secrets) > 0 {
		_, accountKey, err := getStorageAccount(secrets)
		return accountKey, err
	}

	accountName := accountOptions.Name
	// read account key from cache first
	cache, err := d.accountCacheMap.Get(accountName, azcache.CacheReadTypeDefault)
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
		accountKey, err = d.cloud.GetStorageAccesskey(ctx, accountOptions.SubscriptionID, accountName, accountOptions.ResourceGroup)
	}

	if err == nil && accountKey != "" {
		d.accountCacheMap.Set(accountName, accountKey)
	}
	return accountKey, err
}

// GetStorageAccountFromSecret get storage account key from k8s secret
// return <accountName, accountKey, error>
func (d *Driver) GetStorageAccountFromSecret(ctx context.Context, secretName, secretNamespace string) (string, string, error) {
	if d.cloud.KubeClient == nil {
		return "", "", fmt.Errorf("could not get account key from secret(%s): KubeClient is nil", secretName)
	}

	secret, err := d.cloud.KubeClient.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
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

func (d *Driver) useDataPlaneAPI(volumeID, accountName string) bool {
	_, useDataPlaneAPI := d.dataPlaneAPIVolMap.Load(volumeID)
	if useDataPlaneAPI {
		return true
	}

	cache, err := d.dataPlaneAPIAccountCache.Get(accountName, azcache.CacheReadTypeDefault)
	if err != nil {
		klog.Errorf("get(%s) from dataPlaneAPIAccountCache failed with error: %v", accountName, err)
	}
	if cache != nil {
		return true
	}
	return false
}

func (d *Driver) SetAzureCredentials(ctx context.Context, accountName, accountKey, secretName, secretNamespace string) (string, error) {
	if d.cloud.KubeClient == nil {
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
	_, err := d.cloud.KubeClient.CoreV1().Secrets(secretNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		err = nil
	}
	if err != nil {
		return "", fmt.Errorf("couldn't create secret %v", err)
	}
	return secretName, err
}
