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
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"golang.org/x/net/context"
)

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
		if strings.EqualFold(context[ephemeralField], trueValue) {
			context[secretNamespaceField] = context[podNamespaceField]
			// only get storage account from secret
			context[getAccountKeyFromSecretField] = trueValue
			context[storageAccountField] = ""
			klog.V(2).Infof("NodePublishVolume: ephemeral volume(%s) mount on %s, VolumeContext: %v", volumeID, target, context)
			_, err := d.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
				StagingTargetPath: target,
				VolumeContext:     context,
				VolumeCapability:  volCap,
				VolumeId:          volumeID,
			})
			return &csi.NodePublishVolumeResponse{}, err
		}

		if perm := context[mountPermissionsField]; perm != "" {
			var err error
			if mountPermissions, err = strconv.ParseUint(perm, 8, 32); err != nil {
				return nil, status.Errorf(codes.InvalidArgument, fmt.Sprintf("invalid mountPermissions %s", perm))
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
		return nil, status.Errorf(codes.Internal, "Could not mount target %q: %v", target, err)
	}
	if mnt {
		klog.V(2).Infof("NodePublishVolume: %s is already mounted", target)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	if err = preparePublishPath(target, d.mounter); err != nil {
		return nil, fmt.Errorf("prepare publish failed for %s with error: %v", target, err)
	}

	klog.V(2).Infof("NodePublishVolume: mounting %s at %s with mountOptions: %v", source, target, mountOptions)
	if err := d.mounter.Mount(source, target, "", mountOptions); err != nil {
		if removeErr := os.Remove(target); removeErr != nil {
			return nil, status.Errorf(codes.Internal, "Could not remove mount target %q: %v", target, removeErr)
		}
		return nil, status.Errorf(codes.Internal, "Could not mount %q at %q: %v", source, target, err)
	}
	klog.V(2).Infof("NodePublishVolume: mount %s at %s successfully", source, target)

	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmount the volume from the target path
func (d *Driver) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	targetPath := req.GetTargetPath()
	volumeID := req.GetVolumeId()

	klog.V(2).Infof("NodeUnpublishVolume: unmounting volume %s on %s", volumeID, targetPath)
	if err := CleanupSMBMountPoint(d.mounter, targetPath, true /*extensiveMountPointCheck*/); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount target %q: %v", targetPath, err)
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
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()
	volumeMountGroup := req.GetVolumeCapability().GetMount().GetVolumeMountGroup()
	gidPresent := checkGidPresentInMountFlags(mountFlags)

	_, accountName, accountKey, fileShareName, diskName, err := d.GetAccountInfo(ctx, volumeID, req.GetSecrets(), context)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("GetAccountInfo(%s) failed with error: %v", volumeID, err))
	}
	if fileShareName == "" {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to get file share name from %s", volumeID))
	}
	// don't respect fsType from req.GetVolumeCapability().GetMount().GetFsType()
	// since it's ext4 by default on Linux
	var fsType, server, protocol, ephemeralVolMountOptions, storageEndpointSuffix, folderName string
	var ephemeralVol bool
	mountPermissions := d.mountPermissions
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
		case serverNameField:
			server = v
		case ephemeralField:
			ephemeralVol = strings.EqualFold(v, trueValue)
		case mountOptionsField:
			ephemeralVolMountOptions = v
		case storageEndpointSuffixField:
			storageEndpointSuffix = v
		case mountPermissionsField:
			if v != "" {
				var err error
				if mountPermissions, err = strconv.ParseUint(v, 8, 32); err != nil {
					return nil, status.Errorf(codes.InvalidArgument, fmt.Sprintf("invalid mountPermissions %s", v))
				}
			}
		}
	}

	if server == "" && accountName == "" {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to get account name from %s", volumeID))
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	if strings.TrimSpace(storageEndpointSuffix) == "" {
		if d.cloud.Environment.StorageEndpointSuffix != "" {
			storageEndpointSuffix = d.cloud.Environment.StorageEndpointSuffix
		} else {
			storageEndpointSuffix = defaultStorageEndPointSuffix
		}
	}

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
		source = fmt.Sprintf("%s%s%s", source, osSeparator, folderName)
	}

	cifsMountPath := targetPath
	cifsMountFlags := mountFlags
	if !gidPresent && volumeMountGroup != "" {
		cifsMountFlags = append(cifsMountFlags, fmt.Sprintf("gid=%s", volumeMountGroup))
	}
	isDiskMount := isDiskFsType(fsType)
	if isDiskMount {
		if diskName == "" {
			return nil, status.Errorf(codes.Internal, "diskname could not be empty, targetPath: %s", targetPath)
		}
		cifsMountFlags = []string{"dir_mode=0777,file_mode=0777,cache=strict,actimeo=30", "nostrictsync"}
		cifsMountPath = filepath.Join(filepath.Dir(targetPath), proxyMount)
	}

	var mountOptions, sensitiveMountOptions []string
	if protocol == nfs {
		mountOptions = util.JoinMountOptions(mountFlags, []string{"vers=4,minorversion=1,sec=sys"})
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
			mountOptions = appendDefaultMountOptions(cifsMountFlags)
			if ephemeralVol {
				mountOptions = util.JoinMountOptions(mountOptions, strings.Split(ephemeralVolMountOptions, ","))
			}
		}
	}

	klog.V(2).Infof("cifsMountPath(%v) fstype(%v) volumeID(%v) context(%v) mountflags(%v) mountOptions(%v) volumeMountGroup(%s)", cifsMountPath, fsType, volumeID, context, mountFlags, mountOptions, volumeMountGroup)

	isDirMounted, err := d.ensureMountPoint(cifsMountPath, os.FileMode(mountPermissions))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not mount target %q: %v", cifsMountPath, err)
	}
	if isDirMounted {
		klog.V(2).Infof("NodeStageVolume: volume %s is already mounted on %s", volumeID, targetPath)
	} else {
		mountFsType := cifs
		if protocol == nfs {
			mountFsType = nfs
		}
		if err := prepareStagePath(cifsMountPath, d.mounter); err != nil {
			return nil, fmt.Errorf("prepare stage path failed for %s with error: %v", cifsMountPath, err)
		}
		if err := wait.PollImmediate(1*time.Second, 2*time.Minute, func() (bool, error) {
			return true, SMBMount(d.mounter, source, cifsMountPath, mountFsType, mountOptions, sensitiveMountOptions)
		}); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("volume(%s) mount %q on %q failed with %v", volumeID, source, cifsMountPath, err))
		}
		if protocol == nfs {
			klog.V(2).Infof("volumeID(%v): mount targetPath(%s) with permissions(0%o)", volumeID, targetPath, mountPermissions)
			if err := os.Chmod(targetPath, os.FileMode(mountPermissions)); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			if volumeMountGroup != "" {
				klog.V(2).Infof("set gid of volume(%s) as %s", volumeID, volumeMountGroup)
				if err := SetVolumeOwnership(cifsMountPath, volumeMountGroup); err != nil {
					return nil, status.Error(codes.Internal, fmt.Sprintf("SetVolumeOwnership with volume(%s) on %q failed with %v", volumeID, cifsMountPath, err))
				}
			}
		}
		klog.V(2).Infof("volume(%s) mount %q on %q succeeded", volumeID, source, cifsMountPath)
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
			return nil, status.Error(codes.Internal, fmt.Sprintf("could not format %q and mount it at %q", targetPath, diskPath))
		}
		klog.V(2).Infof("NodeStageVolume: volume %s format %s and mounting at %s successfully", volumeID, targetPath, diskPath)
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume unmount the volume from the staging path
func (d *Driver) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target not provided")
	}

	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	klog.V(2).Infof("NodeUnstageVolume: CleanupMountPoint volume %s on %s", volumeID, stagingTargetPath)
	if err := CleanupSMBMountPoint(d.mounter, stagingTargetPath, true /*extensiveMountPointCheck*/); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount staging target %q: %v", stagingTargetPath, err)
	}

	targetPath := filepath.Join(filepath.Dir(stagingTargetPath), proxyMount)
	klog.V(2).Infof("NodeUnstageVolume: CleanupMountPoint volume %s on %s", volumeID, targetPath)
	if err := CleanupMountPoint(d.mounter, targetPath, false); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount staging target %q: %v", targetPath, err)
	}
	klog.V(2).Infof("NodeUnstageVolume: unmount volume %s on %s successfully", volumeID, stagingTargetPath)

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodeGetCapabilities return the capabilities of the Node plugin
func (d *Driver) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: d.NSCap,
	}, nil
}

// NodeGetInfo return info of the node on which this plugin is running
func (d *Driver) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
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

	if _, err := os.Lstat(req.VolumePath); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "path %s does not exist", req.VolumePath)
		}
		return nil, status.Errorf(codes.Internal, "failed to stat file %s: %v", req.VolumePath, err)
	}

	volumeMetrics, err := volume.NewMetricsStatFS(req.VolumePath).GetMetrics()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get metrics: %v", err)
	}

	available, ok := volumeMetrics.Available.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume available size(%v)", volumeMetrics.Available)
	}
	capacity, ok := volumeMetrics.Capacity.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume capacity size(%v)", volumeMetrics.Capacity)
	}
	used, ok := volumeMetrics.Used.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume used size(%v)", volumeMetrics.Used)
	}

	inodesFree, ok := volumeMetrics.InodesFree.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes free(%v)", volumeMetrics.InodesFree)
	}
	inodes, ok := volumeMetrics.Inodes.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes(%v)", volumeMetrics.Inodes)
	}
	inodesUsed, ok := volumeMetrics.InodesUsed.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes used(%v)", volumeMetrics.InodesUsed)
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: available,
				Total:     capacity,
				Used:      used,
			},
			{
				Unit:      csi.VolumeUsage_INODES,
				Available: inodesFree,
				Total:     inodes,
				Used:      inodesUsed,
			},
		},
	}, nil
}

// NodeExpandVolume node expand volume
// N/A for azure file
func (d *Driver) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
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
		_, err := ioutil.ReadDir(target)
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
