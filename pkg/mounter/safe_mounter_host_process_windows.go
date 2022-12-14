//go:build windows
// +build windows

/*
Copyright 2022 The Kubernetes Authors.

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

package mounter

import (
	"context"
	"fmt"
	"os"
	filepath "path/filepath"
	"strings"

	"k8s.io/klog/v2"
	mount "k8s.io/mount-utils"

	"sigs.k8s.io/azurefile-csi-driver/pkg/os/filesystem"
	ossmb "sigs.k8s.io/azurefile-csi-driver/pkg/os/smb"
)

var _ CSIProxyMounter = &winNativeCallMounter{}

type winNativeCallMounter struct{}

func NewWinNativeCallMounter() *winNativeCallMounter {
	return &winNativeCallMounter{}
}

func (mounter *winNativeCallMounter) SMBMount(source, target, fsType string, mountOptions, sensitiveMountOptions []string) error {
	klog.V(2).Infof("SMBMount: remote path: %s local path: %s", source, target)

	if len(mountOptions) == 0 || len(sensitiveMountOptions) == 0 {
		return fmt.Errorf("empty mountOptions(len: %d) or sensitiveMountOptions(len: %d) is not allowed", len(mountOptions), len(sensitiveMountOptions))
	}

	parentDir := filepath.Dir(target)
	parentExists, err := mounter.ExistsPath(parentDir)
	if err != nil {
		return fmt.Errorf("parent dir: %s exist check failed with err: %v", parentDir, err)
	}

	if !parentExists {
		klog.V(2).Infof("Parent directory %s does not exists. Creating the directory", parentDir)
		if err := mounter.MakeDir(parentDir); err != nil {
			return fmt.Errorf("create of parent dir: %s dailed with error: %v", parentDir, err)
		}
	}

	source = strings.Replace(source, "/", "\\", -1)
	normalizedTarget := normalizeWindowsPath(target)

	klog.V(2).Infof("begin to mount %s on %s", source, normalizedTarget)

	remotePath := source
	localPath := normalizedTarget

	if remotePath == "" {
		klog.Errorf("remote path is empty")
		return fmt.Errorf("remote path is empty")
	}

	isMapped, err := ossmb.IsSmbMapped(remotePath)
	if err != nil {
		isMapped = false
	}

	if isMapped {
		valid, err := filesystem.PathValid(context.Background(), remotePath)
		if err != nil {
			klog.Warningf("PathValid(%s) failed with %v, ignore error", remotePath, err)
		}

		if !valid {
			klog.V(4).Infof("RemotePath %s is not valid, removing now", remotePath)
			err := ossmb.RemoveSmbGlobalMapping(remotePath)
			if err != nil {
				klog.Errorf("RemoveSmbGlobalMapping(%s) failed with %v", remotePath, err)
				return err
			}
			isMapped = false
		}
	}

	if !isMapped {
		klog.V(4).Infof("Remote %s not mapped. Mapping now!", remotePath)
		username := mountOptions[0]
		password := sensitiveMountOptions[0]
		err := ossmb.NewSmbGlobalMapping(remotePath, username, password)
		if err != nil {
			klog.Errorf("failed NewSmbGlobalMapping %v", err)
			return err
		}
	}

	if len(localPath) != 0 {
		err = filesystem.ValidatePathWindows(localPath)
		if err != nil {
			klog.Errorf("failed validate plugin path %v", err)
			return err
		}
		err = ossmb.NewSmbLink(remotePath, localPath)
		if err != nil {
			klog.Errorf("failed NewSmbLink %v", err)
			return fmt.Errorf("creating link %s to %s failed with error: %v", localPath, remotePath, err)
		}
	}

	klog.V(2).Infof("mount %s on %s successfully", source, normalizedTarget)

	return nil
}

func (mounter *winNativeCallMounter) SMBUnmount(target string) error {
	klog.V(4).Infof("SMBUnmount: local path: %s", target)
	return mounter.Rmdir(target)
}

// Mount just creates a soft link at target pointing to source.
func (mounter *winNativeCallMounter) Mount(source string, target string, fstype string, options []string) error {
	klog.V(4).Infof("Mount: old name: %s. new name: %s", source, target)
	// Mount is called after the format is done.
	// TODO: Confirm that fstype is empty.
	linkRequest := &filesystem.LinkPathRequest{
		SourcePath: normalizeWindowsPath(source),
		TargetPath: normalizeWindowsPath(target),
	}
	if err := filesystem.LinkPath(context.Background(), linkRequest); err != nil {
		return err
	}
	return nil
}

// Rmdir - delete the given directory
func (mounter *winNativeCallMounter) Rmdir(path string) error {
	klog.V(4).Infof("Remove directory: %s", path)
	rmdirRequest := &filesystem.RmdirRequest{
		Path:  normalizeWindowsPath(path),
		Force: true,
	}
	if err := filesystem.Rmdir(context.Background(), rmdirRequest); err != nil {
		return err
	}
	return nil
}

// Unmount - Removes the directory - equivalent to unmount on Linux.
func (mounter *winNativeCallMounter) Unmount(target string) error {
	klog.V(4).Infof("Unmount: %s", target)
	return mounter.Rmdir(target)
}

func (mounter *winNativeCallMounter) List() ([]mount.MountPoint, error) {
	return []mount.MountPoint{}, fmt.Errorf("List not implemented for CSIProxyMounter")
}

func (mounter *winNativeCallMounter) IsMountPoint(file string) (bool, error) {
	isNotMnt, err := mounter.IsLikelyNotMountPoint(file)
	if err != nil {
		return false, err
	}
	return !isNotMnt, nil
}

func (mounter *winNativeCallMounter) IsMountPointMatch(mp mount.MountPoint, dir string) bool {
	return mp.Path == dir
}

// IsLikelyMountPoint - If the directory does not exists, the function will return os.ErrNotExist error.
// If the path exists, will check if its a link, if its a link then existence of target path is checked.
func (mounter *winNativeCallMounter) IsLikelyNotMountPoint(path string) (bool, error) {
	klog.V(4).Infof("IsLikelyNotMountPoint: %s", path)
	isExists, err := mounter.ExistsPath(path)
	if err != nil {
		return false, err
	}
	if !isExists {
		return true, os.ErrNotExist
	}

	response, err := filesystem.IsMountPoint(context.Background(),
		&filesystem.IsMountPointRequest{
			Path: normalizeWindowsPath(path),
		})
	if err != nil {
		return false, err
	}
	return !response, nil
}

// MakeDir - Creates a directory.
// Currently the make dir is only used from the staging code path, hence we call it
// with Plugin context..
func (mounter *winNativeCallMounter) MakeDir(path string) error {
	klog.V(4).Infof("Make directory: %s", path)
	mkdirReq := &filesystem.MkdirRequest{
		Path: normalizeWindowsPath(path),
	}
	if err := filesystem.Mkdir(context.Background(), mkdirReq); err != nil {
		return err
	}

	return nil
}

// ExistsPath - Checks if a path exists. Unlike util ExistsPath, this call does not perform follow link.
func (mounter *winNativeCallMounter) ExistsPath(path string) (bool, error) {
	klog.V(4).Infof("Exists path: %s", path)
	return filesystem.PathExists(context.Background(),
		&filesystem.PathExistsRequest{
			Path: normalizeWindowsPath(path),
		})
}

func (mounter *winNativeCallMounter) MountSensitive(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	return fmt.Errorf("MountSensitive not implemented for winNativeCallMounter")
}

func (mounter *winNativeCallMounter) MountSensitiveWithoutSystemd(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	return fmt.Errorf("MountSensitiveWithoutSystemd not implemented for winNativeCallMounter")
}

func (mounter *winNativeCallMounter) MountSensitiveWithoutSystemdWithMountFlags(source string, target string, fstype string, options []string, sensitiveOptions []string, mountFlags []string) error {
	return mounter.MountSensitive(source, target, fstype, options, sensitiveOptions /* sensitiveOptions */)
}

func (mounter *winNativeCallMounter) GetMountRefs(pathname string) ([]string, error) {
	return []string{}, fmt.Errorf("GetMountRefs not implemented for winNativeCallMounter")
}

func (mounter *winNativeCallMounter) EvalHostSymlinks(pathname string) (string, error) {
	return "", fmt.Errorf("EvalHostSymlinks not implemented for winNativeCallMounter")
}

func (mounter *winNativeCallMounter) GetFSGroup(pathname string) (int64, error) {
	return -1, fmt.Errorf("GetFSGroup not implemented for winNativeCallMounter")
}

func (mounter *winNativeCallMounter) GetSELinuxSupport(pathname string) (bool, error) {
	return false, fmt.Errorf("GetSELinuxSupport not implemented for winNativeCallMounter")
}

func (mounter *winNativeCallMounter) GetMode(pathname string) (os.FileMode, error) {
	return 0, fmt.Errorf("GetMode not implemented for winNativeCallMounter")
}

// GetAPIVersions returns the versions of the client APIs this mounter is using.
func (mounter *winNativeCallMounter) GetAPIVersions() string {
	return ""
}

func (mounter *winNativeCallMounter) CanSafelySkipMountPointCheck() bool {
	return false
}
