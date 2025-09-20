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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	volume "github.com/kata-containers/kata-containers/src/runtime/pkg/direct-volume"

	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/volume/util"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	mount_azurefile "sigs.k8s.io/azurefile-csi-driver/pkg/azurefile-proxy/pb"
	volumehelper "sigs.k8s.io/azurefile-csi-driver/pkg/util"
	azcache "sigs.k8s.io/cloud-provider-azure/pkg/cache"
	"sigs.k8s.io/cloud-provider-azure/pkg/metrics"
)

var getRuntimeClassForPodFunc = getRuntimeClassForPod
var isConfidentialRuntimeClassFunc = isConfidentialRuntimeClass

type MountClient struct {
	service mount_azurefile.MountServiceClient
}

// NewMountClient returns a new mount client
func NewMountClient(cc *grpc.ClientConn) *MountClient {
	service := mount_azurefile.NewMountServiceClient(cc)
	return &MountClient{service}
}

// NodePublishVolume mount the volume from staging to target path
func (d *Driver) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	target := req.GetTargetPath()
	if len(target) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	volumeID := req.GetVolumeId()

	mountPermissions := d.mountPermissions
	context := req.GetVolumeContext()
	if context != nil {
		if !strings.EqualFold(getValueInMap(context, mountWithManagedIdentityField), trueValue) && getValueInMap(context, serviceAccountTokenField) != "" && getValueInMap(context, clientIDField) != "" {
			klog.V(2).Infof("NodePublishVolume: volume(%s) mount on %s with service account token, clientID: %s", volumeID, target, getValueInMap(context, clientIDField))
			_, err := d.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
				StagingTargetPath: target,
				VolumeContext:     context,
				VolumeCapability:  volCap,
				VolumeId:          volumeID,
			})
			return &csi.NodePublishVolumeResponse{}, err
		}

		// ephemeral volume
		if strings.EqualFold(context[ephemeralField], trueValue) {
			setKeyValueInMap(context, secretNamespaceField, context[podNamespaceField])
			if !d.allowInlineVolumeKeyAccessWithIdentity {
				// only get storage account from secret
				setKeyValueInMap(context, getAccountKeyFromSecretField, trueValue)
				setKeyValueInMap(context, storageAccountField, "")
			}
			klog.V(2).Infof("NodePublishVolume: ephemeral volume(%s) mount on %s", volumeID, target)
			_, err := d.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
				StagingTargetPath: target,
				VolumeContext:     context,
				VolumeCapability:  volCap,
				VolumeId:          volumeID,
			})
			return &csi.NodePublishVolumeResponse{}, err
		}

		if perm := getValueInMap(context, mountPermissionsField); perm != "" {
			var err error
			if mountPermissions, err = strconv.ParseUint(perm, 8, 32); err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid mountPermissions %s", perm)
			}
		}

		if d.enableKataCCMount && context[podNameField] != "" && context[podNamespaceField] != "" {
			confidentialContainerLabel := getValueInMap(context, confidentialContainerLabelField)
			if !d.isKataNode && confidentialContainerLabel != "" {
				klog.V(2).Infof("NodePublishVolume: checking if node %s is a kata node with confidential container label %s", d.NodeID, confidentialContainerLabel)
				d.isKataNode = isKataNode(ctx, d.NodeID, confidentialContainerLabel, d.kubeClient)
			}

			if d.isKataNode {
				runtimeClass, err := getRuntimeClassForPodFunc(ctx, d.kubeClient, context[podNameField], context[podNamespaceField])
				if err != nil {
					return nil, status.Errorf(codes.Internal, "failed to get runtime class for pod %s/%s: %v", context[podNamespaceField], context[podNameField], err)
				}
				klog.V(2).Infof("NodePublishVolume: volume(%s) mount on %s with runtimeClass %s", volumeID, target, runtimeClass)
				runtimeClassHandler := getValueInMap(context, runtimeClassHandlerField)
				if runtimeClassHandler == "" {
					runtimeClassHandler = defaultRuntimeClassHandler
				}
				isConfidentialRuntimeClass, err := isConfidentialRuntimeClassFunc(ctx, d.kubeClient, runtimeClass, runtimeClassHandler)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "failed to check if runtime class %s is confidential: %v", runtimeClass, err)
				}
				if isConfidentialRuntimeClass {
					klog.V(2).Infof("NodePublishVolume for volume(%s) where runtimeClass is %s", volumeID, runtimeClass)
					source := req.GetStagingTargetPath()
					if len(source) == 0 {
						return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
					}
					// Load the mount info from staging area
					mountInfo, err := d.directVolume.VolumeMountInfo(source)
					if err != nil {
						return nil, status.Errorf(codes.Internal, "failed to load mount info from %s: %v", source, err)
					}
					if mountInfo == nil {
						return nil, status.Errorf(codes.Internal, "mount info is nil for volume %s", volumeID)
					}
					data, err := json.Marshal(mountInfo)
					if err != nil {
						return nil, status.Errorf(codes.Internal, "failed to marshal mount info %s: %v", source, err)
					}
					if err = d.directVolume.Add(target, string(data)); err != nil {
						return nil, status.Errorf(codes.Internal, "failed to save mount info %s: %v", target, err)
					}
					klog.V(2).Infof("NodePublishVolume: direct volume mount %s at %s successfully", source, target)
					return &csi.NodePublishVolumeResponse{}, nil
				}
			}
		}
	}

	source := req.GetStagingTargetPath()
	if len(source) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	mountOptions := []string{"bind"}
	if req.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	}

	mnt, err := d.ensureMountPoint(target, os.FileMode(mountPermissions))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not mount target %s: %v", target, err)
	}
	if mnt {
		klog.V(2).Infof("NodePublishVolume: %s is already mounted", target)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	if err = preparePublishPath(target, d.mounter); err != nil {
		return nil, status.Errorf(codes.Internal, "prepare publish failed for %s with error: %v", target, err)
	}

	klog.V(2).Infof("NodePublishVolume: mounting %s at %s with mountOptions: %v", source, target, mountOptions)
	if err := d.mounter.Mount(source, target, "", mountOptions); err != nil {
		if removeErr := os.Remove(target); removeErr != nil {
			return nil, status.Errorf(codes.Internal, "Could not remove mount target %s: %v", target, removeErr)
		}
		return nil, status.Errorf(codes.Internal, "Could not mount %s at %s: %v", source, target, err)
	}
	klog.V(2).Infof("NodePublishVolume: mount %s at %s successfully", source, target)

	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmount the volume from the target path
func (d *Driver) NodeUnpublishVolume(_ context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	targetPath := req.GetTargetPath()
	volumeID := req.GetVolumeId()

	klog.V(2).Infof("NodeUnpublishVolume: unmounting volume %s on %s", volumeID, targetPath)
	if err := CleanupMountPoint(d.mounter, targetPath, true /*extensiveMountPointCheck*/); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount target %s: %v", targetPath, err)
	}

	if d.enableKataCCMount && d.isKataNode {
		klog.V(2).Infof("NodeUnpublishVolume: remove direct volume mount info %s from %s", volumeID, targetPath)
		// Remove deletes the direct volume path including all the files inside it.
		// if there is no kata-cc mountinfo present on this path, it will return nil.
		if err := d.directVolume.Remove(targetPath); err != nil {
			if strings.Contains(err.Error(), "file name too long") {
				klog.Warningf("NodeUnpublishVolume: direct volume mount info %s not found on %s, ignoring error", volumeID, targetPath)
				return &csi.NodeUnpublishVolumeResponse{}, nil
			}
			return nil, status.Errorf(codes.Internal, "failed to direct volume remove mount info %s: %v", targetPath, err)
		}
	}

	klog.V(2).Infof("NodeUnpublishVolume: unmount volume %s on %s successfully", volumeID, targetPath)

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeStageVolume mount the volume to a staging path
func (d *Driver) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	targetPath := req.GetStagingTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}
	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability not provided")
	}

	volumeID := req.GetVolumeId()
	context := req.GetVolumeContext()

	if getValueInMap(context, clientIDField) != "" && !strings.EqualFold(getValueInMap(context, mountWithManagedIdentityField), trueValue) && getValueInMap(context, serviceAccountTokenField) == "" {
		klog.V(2).Infof("Skip NodeStageVolume for volume(%s) since clientID %s is provided but service account token is empty", volumeID, getValueInMap(context, clientIDField))
		return &csi.NodeStageVolumeResponse{}, nil
	}

	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()
	volumeMountGroup := req.GetVolumeCapability().GetMount().GetVolumeMountGroup()
	gidPresent := checkGidPresentInMountFlags(mountFlags)

	if isReadOnlyFromCapability(volumeCapability) {
		mountFlags = util.JoinMountOptions(mountFlags, []string{"ro"})
		klog.V(2).Infof("CSI volume is read-only, mounting with extra option ro")
	}

	mc := metrics.NewMetricContext(azureFileCSIDriverName, "node_stage_volume", d.cloud.ResourceGroup, "", d.Name)
	isOperationSucceeded := false
	defer func() {
		mc.ObserveOperationWithResult(isOperationSucceeded, VolumeID, volumeID)
	}()

	_, accountName, accountKey, fileShareName, diskName, _, err := d.GetAccountInfo(ctx, volumeID, req.GetSecrets(), context)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("GetAccountInfo(%s) failed with error: %v", volumeID, err))
	}
	if fileShareName == "" {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to get file share name from %s", volumeID))
	}
	// don't respect fsType from req.GetVolumeCapability().GetMount().GetFsType()
	// since it's ext4 by default on Linux
	var fsType, server, protocol, ephemeralVolMountOptions, storageEndpointSuffix, folderName, clientID string
	var ephemeralVol, createFolderIfNotExist, encryptInTransit, mountWithManagedIdentity bool
	fileShareNameReplaceMap := map[string]string{}

	mountPermissions := d.mountPermissions
	performChmodOp := (mountPermissions > 0)
	fsGroupChangePolicy := d.fsGroupChangePolicy
	for k, v := range context {
		switch strings.ToLower(k) {
		case fsTypeField:
			fsType = v
		case protocolField:
			protocol = v
		case diskNameField:
			diskName = v
		case folderNameField:
			folderName = v
		case createFolderIfNotExistField:
			createFolderIfNotExist = strings.EqualFold(v, trueValue)
		case serverNameField:
			server = v
		case ephemeralField:
			ephemeralVol = strings.EqualFold(v, trueValue)
		case mountOptionsField:
			ephemeralVolMountOptions = v
		case storageEndpointSuffixField:
			storageEndpointSuffix = v
		case fsGroupChangePolicyField:
			fsGroupChangePolicy = v
		case pvcNamespaceKey:
			fileShareNameReplaceMap[pvcNamespaceMetadata] = v
		case pvcNameKey:
			fileShareNameReplaceMap[pvcNameMetadata] = v
		case pvNameKey:
			fileShareNameReplaceMap[pvNameMetadata] = v
		case mountPermissionsField:
			if v != "" {
				var perm uint64
				if perm, err = strconv.ParseUint(v, 8, 32); err != nil {
					return nil, status.Errorf(codes.InvalidArgument, "invalid mountPermissions %s", v)
				}
				if perm == 0 {
					performChmodOp = false
				} else {
					mountPermissions = perm
				}
			}
		case encryptInTransitField:
			encryptInTransit, err = strconv.ParseBool(v)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("Volume context property %q must be a boolean value: %v", k, err))
			}
		case mountWithManagedIdentityField:
			mountWithManagedIdentity, err = strconv.ParseBool(v)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("Volume context property %q must be a boolean value: %v", k, err))
			}
		case clientIDField:
			clientID = v
		}
	}

	if server == "" && accountName == "" {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to get account name from %s", volumeID))
	}

	if !isSupportedFsType(fsType) {
		return nil, status.Errorf(codes.InvalidArgument, "fsType(%s) is not supported, supported fsType list: %v", fsType, supportedFsTypeList)
	}

	if !isSupportedProtocol(protocol) {
		return nil, status.Errorf(codes.InvalidArgument, "protocol(%s) is not supported, supported protocol list: %v", protocol, supportedProtocolList)
	}

	if !isSupportedFSGroupChangePolicy(fsGroupChangePolicy) {
		return nil, status.Errorf(codes.InvalidArgument, "fsGroupChangePolicy(%s) is not supported, supported fsGroupChangePolicy list: %v", fsGroupChangePolicy, supportedFSGroupChangePolicyList)
	}

	lockKey := fmt.Sprintf("%s-%s", volumeID, targetPath)
	if acquired := d.volumeLocks.TryAcquire(lockKey); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(lockKey)

	if strings.TrimSpace(storageEndpointSuffix) == "" {
		storageEndpointSuffix = d.getStorageEndPointSuffix()
	}

	// replace pv/pvc name namespace metadata in fileShareName
	fileShareName = replaceWithMap(fileShareName, fileShareNameReplaceMap)

	osSeparator := string(os.PathSeparator)
	if strings.TrimSpace(server) == "" {
		// server address is "accountname.file.core.windows.net" by default
		server = fmt.Sprintf("%s.file.%s", accountName, storageEndpointSuffix)
	}
	source := fmt.Sprintf("%s%s%s%s%s", osSeparator, osSeparator, server, osSeparator, fileShareName)
	if protocol == nfs {
		source = fmt.Sprintf("%s:/%s/%s", server, accountName, fileShareName)
	}
	if folderName != "" {
		if createFolderIfNotExist {
			if err := d.createFolderIfNotExists(ctx, accountName, accountKey, fileShareName, folderName, storageEndpointSuffix); err != nil {
				klog.Warningf("Failed to create folder %s in share %s: %v", folderName, fileShareName, err)
				// Continue with mounting - folder might already exist or be created by other means
			}
		}
		source = fmt.Sprintf("%s%s%s", source, osSeparator, folderName)
	}

	cifsMountPath := targetPath
	cifsMountFlags := mountFlags
	if !gidPresent && volumeMountGroup != "" {
		cifsMountFlags = append(cifsMountFlags, fmt.Sprintf("gid=%s", volumeMountGroup))
	}
	isDiskMount := isDiskFsType(fsType)
	if isDiskMount {
		if !strings.HasSuffix(diskName, vhdSuffix) {
			return nil, status.Errorf(codes.Internal, "diskname could not be empty, targetPath: %s", targetPath)
		}
		cifsMountFlags = []string{"dir_mode=0777,file_mode=0777,cache=strict,actimeo=30", "nostrictsync"}
		cifsMountPath = filepath.Join(filepath.Dir(targetPath), proxyMount)
	}

	var mountOptions, sensitiveMountOptions []string
	if protocol == nfs {
		mountOptions = util.JoinMountOptions(mountFlags, []string{"vers=4,minorversion=1,sec=sys"})
		mountOptions = appendDefaultNfsMountOptions(mountOptions, d.appendNoResvPortOption, d.appendActimeoOption)
	} else {
		if mountWithManagedIdentity && runtime.GOOS != "windows" {
			if clientID == "" {
				clientID = d.cloud.Config.AzureAuthConfig.UserAssignedIdentityID
			}
			sensitiveMountOptions = []string{"sec=krb5,cruid=0,upcall_target=mount", fmt.Sprintf("username=%s", clientID)}
			klog.V(2).Infof("using managed identity %s for volume %s with mount options: %v", clientID, volumeID, sensitiveMountOptions)
		} else {
			if accountName == "" || accountKey == "" {
				return nil, status.Errorf(codes.Internal, "accountName(%s) or accountKey is empty", accountName)
			}
			if runtime.GOOS == "windows" {
				mountOptions = []string{fmt.Sprintf("AZURE\\%s", accountName)}
				sensitiveMountOptions = []string{accountKey}
			} else {
				if err := os.MkdirAll(targetPath, os.FileMode(mountPermissions)); err != nil {
					return nil, status.Error(codes.Internal, fmt.Sprintf("MkdirAll %s failed with error: %v", targetPath, err))
				}
				// parameters suggested by https://azure.microsoft.com/en-us/documentation/articles/storage-how-to-use-files-linux/
				sensitiveMountOptions = []string{fmt.Sprintf("username=%s,password=%s", accountName, accountKey)}
			}
		}

		if runtime.GOOS != "windows" {
			if ephemeralVol {
				cifsMountFlags = util.JoinMountOptions(cifsMountFlags, strings.Split(ephemeralVolMountOptions, ","))
			}
			mountOptions = appendDefaultCifsMountOptions(cifsMountFlags, d.appendNoShareSockOption, d.appendClosetimeoOption)
		}
	}

	klog.V(2).Infof("cifsMountPath(%v) fstype(%v) volumeID(%v) mountflags(%v) mountOptions(%v) volumeMountGroup(%s)", cifsMountPath, fsType, volumeID, mountFlags, mountOptions, volumeMountGroup)

	isDirMounted, err := d.ensureMountPoint(cifsMountPath, os.FileMode(mountPermissions))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not mount target %s: %v", cifsMountPath, err)
	}
	if isDirMounted {
		klog.V(2).Infof("NodeStageVolume: volume %s is already mounted on %s", volumeID, targetPath)
	} else {
		mountFsType := cifs
		if protocol == nfs {
			mountFsType = nfs
			if newOptions, exists := removeOptionIfExists(mountOptions, encryptInTransitField); exists {
				klog.V(2).Infof("encryptInTransit is set in mountOptions(%v), enabling encryptInTransit", mountOptions)
				encryptInTransit = true
				mountOptions = newOptions
			}
			if encryptInTransit {
				mountFsType = aznfs
			}
		}
		if mountFsType == aznfs && !d.enableAzurefileProxy {
			return nil, status.Error(codes.InvalidArgument, "encryptInTransit is only available when azurefile-proxy is enabled")
		}

		if err := prepareStagePath(cifsMountPath, d.mounter); err != nil {
			return nil, status.Errorf(codes.Internal, "prepare stage path failed for %s with error: %v", cifsMountPath, err)
		}
		if mountFsType == aznfs {
			klog.V(2).Infof("encryptInTransit is enabled, mount by azurefile-proxy")
			if err := d.mountWithProxy(ctx, source, cifsMountPath, mountFsType, mountOptions, sensitiveMountOptions); err != nil {
				return nil, status.Errorf(codes.Internal, "mount with proxy failed for %s with error: %v", cifsMountPath, err)
			}
			klog.V(2).Infof("mount with proxy succeeded for %s", cifsMountPath)
		} else {
			execFunc := func() error {
				if mountWithManagedIdentity && protocol != nfs && runtime.GOOS != "windows" {
					if out, err := setCredentialCache(server, clientID); err != nil {
						return fmt.Errorf("setCredentialCache failed for %s with error: %v, output: %s", server, err, out)
					}
				}
				return SMBMount(d.mounter, source, cifsMountPath, mountFsType, mountOptions, sensitiveMountOptions)
			}
			timeoutFunc := func() error { return fmt.Errorf("time out") }
			if err := volumehelper.WaitUntilTimeout(90*time.Second, execFunc, timeoutFunc); err != nil {
				var helpLinkMsg string
				if d.appendMountErrorHelpLink {
					helpLinkMsg = "\nPlease refer to http://aka.ms/filemounterror for possible causes and solutions for mount errors."
				}
				return nil, status.Error(codes.Internal, fmt.Sprintf("volume(%s) mount %s on %s failed with %v%s", volumeID, source, cifsMountPath, err, helpLinkMsg))
			}
		}
		if protocol == nfs {
			if performChmodOp {
				if err := chmodIfPermissionMismatch(targetPath, os.FileMode(mountPermissions)); err != nil {
					return nil, status.Error(codes.Internal, err.Error())
				}
			} else {
				klog.V(2).Infof("skip chmod on targetPath(%s) since mountPermissions is set as 0", targetPath)
			}
		}
		klog.V(2).Infof("volume(%s) mount %s on %s succeeded", volumeID, source, cifsMountPath)
	}

	// If runtime OS is not windows and protocol is not nfs, save mountInfo.json
	if d.enableKataCCMount && d.isKataNode {
		if runtime.GOOS != "windows" && protocol != nfs {
			// Check if mountInfo.json is already present at the targetPath
			isMountInfoPresent, err := d.directVolume.VolumeMountInfo(cifsMountPath)
			if err != nil && !os.IsNotExist(err) {
				return nil, status.Errorf(codes.Internal, "Could not save direct volume mount info %s: %v", cifsMountPath, err)
			}
			if isMountInfoPresent != nil {
				klog.V(2).Infof("NodeStageVolume: mount info for volume %s is already present on %s", volumeID, targetPath)
			} else {
				mountFsType := cifs
				ipAddr, err := d.resolver.ResolveIPAddr("ip", server)
				if err != nil {
					klog.V(2).ErrorS(err, "Couldn't resolve IP")
					return nil, err
				}
				mountOptions = append(mountOptions, "addr="+ipAddr.IP.String())
				mountInfo := volume.MountInfo{
					VolumeType: "azurefile",
					Device:     source,
					FsType:     mountFsType,
					Metadata: map[string]string{
						"sensitiveMountOptions": strings.Join(sensitiveMountOptions, ","),
					},
					Options: mountOptions,
				}
				data, _ := json.Marshal(mountInfo)
				if err := d.directVolume.Add(cifsMountPath, string(data)); err != nil {
					return nil, status.Errorf(codes.Internal, "Could not save direct volume mount info %s: %v", cifsMountPath, err)
				}
				klog.V(2).Infof("NodeStageVolume: mount info for volume %s saved on %s", volumeID, targetPath)
			}
		} else {
			klog.V(2).Infof("NodeStageVolume: skip saving mount info for volume %s on %s, runtime OS: %s, protocol: %s", volumeID, targetPath, runtime.GOOS, protocol)
		}
	}

	if isDiskMount {
		mnt, err := d.ensureMountPoint(targetPath, os.FileMode(mountPermissions))
		if err != nil {
			return nil, status.Errorf(codes.Internal, "mount %s on target %s failed with %v", volumeID, targetPath, err)
		}
		if mnt {
			klog.V(2).Infof("NodeStageVolume: volume %s is already mounted on %s", volumeID, targetPath)
			return &csi.NodeStageVolumeResponse{}, nil
		}

		diskPath := filepath.Join(cifsMountPath, diskName)
		options := util.JoinMountOptions(mountFlags, []string{"loop"})
		if strings.HasPrefix(fsType, "ext") {
			// following mount options are only valid for ext2/ext3/ext4 file systems
			options = util.JoinMountOptions(options, []string{"noatime", "barrier=1", "errors=remount-ro"})
		}

		klog.V(2).Infof("NodeStageVolume: volume %s formatting %s and mounting at %s with mount options(%s)", volumeID, targetPath, diskPath, options)
		// FormatAndMount will format only if needed
		if err := d.mounter.FormatAndMount(diskPath, targetPath, fsType, options); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("could not format %s and mount it at %s", targetPath, diskPath))
		}
		klog.V(2).Infof("NodeStageVolume: volume %s format %s and mounting at %s successfully", volumeID, targetPath, diskPath)
	}

	if protocol == nfs || isDiskMount {
		if volumeMountGroup != "" && fsGroupChangePolicy != FSGroupChangeNone {
			klog.V(2).Infof("set gid of volume(%s) as %s using fsGroupChangePolicy(%s)", volumeID, volumeMountGroup, fsGroupChangePolicy)
			if err := SetVolumeOwnership(cifsMountPath, volumeMountGroup, fsGroupChangePolicy); err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("SetVolumeOwnership with volume(%s) on %s failed with %v", volumeID, cifsMountPath, err))
			}
		}
	}

	isOperationSucceeded = true
	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume unmount the volume from the staging path
func (d *Driver) NodeUnstageVolume(_ context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	lockKey := fmt.Sprintf("%s-%s", volumeID, stagingTargetPath)
	if acquired := d.volumeLocks.TryAcquire(lockKey); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(lockKey)

	mc := metrics.NewMetricContext(azureFileCSIDriverName, "node_unstage_volume", d.cloud.ResourceGroup, "", d.Name)
	isOperationSucceeded := false
	defer func() {
		mc.ObserveOperationWithResult(isOperationSucceeded, VolumeID, volumeID)
	}()

	klog.V(2).Infof("NodeUnstageVolume: unmount volume %s on %s", volumeID, stagingTargetPath)
	if err := SMBUnmount(d.mounter, stagingTargetPath, true /*extensiveMountPointCheck*/, d.removeSMBMountOnWindows); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount staging target %s: %v", stagingTargetPath, err)
	}

	if runtime.GOOS != "windows" {
		targetPath := filepath.Join(filepath.Dir(stagingTargetPath), proxyMount)
		klog.V(2).Infof("NodeUnstageVolume: CleanupMountPoint volume %s on %s", volumeID, targetPath)
		if err := CleanupMountPoint(d.mounter, targetPath, false); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to unmount staging target %s: %v", targetPath, err)
		}
	}

	if d.enableKataCCMount && d.isKataNode {
		klog.V(2).Infof("NodeUnstageVolume: remove direct volume mount info %s from %s", volumeID, stagingTargetPath)
		if err := d.directVolume.Remove(stagingTargetPath); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to remove mount info %s: %v", stagingTargetPath, err)
		}
	}

	klog.V(2).Infof("NodeUnstageVolume: unmount volume %s on %s successfully", volumeID, stagingTargetPath)

	isOperationSucceeded = true
	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodeGetCapabilities return the capabilities of the Node plugin
func (d *Driver) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: d.NSCap,
	}, nil
}

// NodeGetInfo return info of the node on which this plugin is running
func (d *Driver) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: d.NodeID,
	}, nil
}

// NodeGetVolumeStats get volume stats
func (d *Driver) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	if len(req.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume ID was empty")
	}
	if len(req.VolumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume path was empty")
	}

	// check if the volume stats is cached
	cache, err := d.volStatsCache.Get(ctx, req.VolumeId, azcache.CacheReadTypeDefault)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if cache != nil {
		resp := cache.(*csi.NodeGetVolumeStatsResponse)
		klog.V(6).Infof("NodeGetVolumeStats: volume stats for volume %s path %s is cached", req.VolumeId, req.VolumePath)
		return resp, nil
	}

	// fileShareName in volumeID may contain subPath, e.g. csi-shared-config/ASCP01/certs
	// get the file share name without subPath from volumeID and check the cache again using new volumeID
	var newVolID string
	if _, accountName, fileShareName, _, secretNamespace, _, err := GetFileShareInfo(req.VolumeId); err == nil {
		if splitStr := strings.Split(fileShareName, "/"); len(splitStr) > 1 {
			fileShareName = splitStr[0]
		}
		// get new volumeID
		if accountName != "" && fileShareName != "" {
			newVolID = fmt.Sprintf(volumeIDTemplate, "", accountName, fileShareName, "", "", secretNamespace)
		}
	}

	if cache, err = d.volStatsCache.Get(ctx, newVolID, azcache.CacheReadTypeDefault); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if cache != nil {
		resp := cache.(*csi.NodeGetVolumeStatsResponse)
		klog.V(6).Infof("NodeGetVolumeStats: volume stats for volume %s path %s is cached", req.VolumeId, req.VolumePath)
		return resp, nil
	}

	if _, err := os.Lstat(req.VolumePath); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "path %s does not exist", req.VolumePath)
		}
		return nil, status.Errorf(codes.Internal, "failed to stat file %s: %v", req.VolumePath, err)
	}

	if d.printVolumeStatsCallLogs {
		klog.V(2).Infof("NodeGetVolumeStats: begin to get VolumeStats on volume %s path %s", req.VolumeId, req.VolumePath)
	} else {
		klog.V(6).Infof("NodeGetVolumeStats: begin to get VolumeStats on volume %s path %s", req.VolumeId, req.VolumePath)
	}

	mc := metrics.NewMetricContext(azureFileCSIDriverName, "node_get_volume_stats", d.cloud.ResourceGroup, "", d.Name)
	mc.LogLevel = 6 // change log level
	isOperationSucceeded := false
	defer func() {
		mc.ObserveOperationWithResult(isOperationSucceeded, VolumeID, req.VolumeId)
	}()

	resp, err := GetVolumeStats(req.VolumePath, d.enableWindowsHostProcess)
	if err == nil && resp != nil {
		if d.printVolumeStatsCallLogs {
			klog.V(2).Infof("NodeGetVolumeStats: volume stats for volume %s path %s is %v", req.VolumeId, req.VolumePath, resp)
		} else {
			klog.V(6).Infof("NodeGetVolumeStats: volume stats for volume %s path %s is %v", req.VolumeId, req.VolumePath, resp)
		}
		// cache the volume stats per volume
		d.volStatsCache.Set(req.VolumeId, resp)
		if newVolID != "" {
			d.volStatsCache.Set(newVolID, resp)
		}
	}
	isOperationSucceeded = true
	return resp, err
}

// NodeExpandVolume node expand volume
// N/A for azure file
func (d *Driver) NodeExpandVolume(_ context.Context, _ *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ensureMountPoint: create mount point if not exists
// return <true, nil> if it's already a mounted point otherwise return <false, nil>
func (d *Driver) ensureMountPoint(target string, perm os.FileMode) (bool, error) {
	notMnt, err := d.mounter.IsLikelyNotMountPoint(target)
	if err != nil && !os.IsNotExist(err) {
		if IsCorruptedDir(target) {
			notMnt = false
			klog.Warningf("detected corrupted mount for targetPath [%s]", target)
		} else {
			return !notMnt, err
		}
	}

	if runtime.GOOS != "windows" {
		// Check all the mountpoints in case IsLikelyNotMountPoint
		// cannot handle --bind mount
		mountList, err := d.mounter.List()
		if err != nil {
			return !notMnt, err
		}

		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return !notMnt, err
		}

		for _, mountPoint := range mountList {
			if mountPoint.Path == targetAbs {
				notMnt = false
				break
			}
		}
	}

	if !notMnt {
		// testing original mount point, make sure the mount link is valid
		_, err := os.ReadDir(target)
		if err == nil {
			klog.V(2).Infof("already mounted to target %s", target)
			return !notMnt, nil
		}
		// mount link is invalid, now unmount and remount later
		klog.Warningf("ReadDir %s failed with %v, unmount this directory", target, err)
		if err := d.mounter.Unmount(target); err != nil {
			klog.Errorf("Unmount directory %s failed with %v", target, err)
			return !notMnt, err
		}
		notMnt = true
		return !notMnt, err
	}
	if err := makeDir(target, perm); err != nil {
		klog.Errorf("MakeDir failed on target: %s (%v)", target, err)
		return !notMnt, err
	}
	return !notMnt, nil
}

func (d *Driver) mountWithProxy(ctx context.Context, source, target, fsType string, options, sensitiveMountOptions []string) error {
	klog.V(2).Infof("start connecting to azurefile proxy")
	conn, err := grpc.NewClient(d.azurefileProxyEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		klog.Error("failed to connect to azurefile proxy:", err)
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			klog.Error("failed to close connection to azurefile proxy:", err)
		}
	}()
	klog.V(2).Infof("connected to azurefile proxy successfully")

	mountClient := NewMountClient(conn)
	mountreq := mount_azurefile.MountAzureFileRequest{
		Source:           source,
		Target:           target,
		Fstype:           fsType,
		MountOptions:     options,
		SensitiveOptions: sensitiveMountOptions,
	}
	klog.V(2).Infof("begin to mount with azurefile proxy, source: %s, target: %s, fstype: %s, mountOptions: %v", source, target, fsType, options)
	_, err = mountClient.service.MountAzureFile(ctx, &mountreq)
	if err != nil {
		klog.Error("GRPC call returned with an error:", err)
		return err
	}

	return err
}

func makeDir(pathname string, perm os.FileMode) error {
	err := os.MkdirAll(pathname, perm)
	if err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	return nil
}

func checkGidPresentInMountFlags(mountFlags []string) bool {
	for _, mountFlag := range mountFlags {
		if strings.HasPrefix(mountFlag, "gid") {
			return true
		}
	}
	return false
}
