//go:build windows
// +build windows

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
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/windows"
	"k8s.io/klog/v2"
	mount "k8s.io/mount-utils"
	"sigs.k8s.io/azurefile-csi-driver/pkg/mounter"
)

func SMBMount(m *mount.SafeFormatAndMount, source, target, fsType string, mountOptions, sensitiveMountOptions []string) error {
	if proxy, ok := m.Interface.(mounter.CSIProxyMounter); ok {
		return proxy.SMBMount(source, target, fsType, mountOptions, sensitiveMountOptions)
	}
	return fmt.Errorf("could not cast to csi proxy class")
}

func SMBUnmount(m *mount.SafeFormatAndMount, target string) error {
	if proxy, ok := m.Interface.(mounter.CSIProxyMounter); ok {
		return proxy.SMBUnmount(target)
	}
	return fmt.Errorf("could not cast to csi proxy class")
}

func RemoveStageTarget(m *mount.SafeFormatAndMount, target string) error {
	if proxy, ok := m.Interface.(mounter.CSIProxyMounter); ok {
		return proxy.Rmdir(target)
	}
	return fmt.Errorf("could not cast to csi proxy class")
}

// CleanupSMBMountPoint - In windows CSI proxy call to umount is used to unmount the SMB.
// The clean up mount point point calls is supposed for fix the corrupted directories as well.
// For alpha CSI proxy integration, we only do an unmount.
func CleanupSMBMountPoint(m *mount.SafeFormatAndMount, target string, extensiveMountCheck bool) error {
	return SMBUnmount(m, target)
}

func CleanupMountPoint(m *mount.SafeFormatAndMount, target string, extensiveMountCheck bool) error {
	if proxy, ok := m.Interface.(mounter.CSIProxyMounter); ok {
		return proxy.Rmdir(target)
	}
	return fmt.Errorf("could not cast to csi proxy class")
}

func GetVolumeStats(ctx context.Context, m *mount.SafeFormatAndMount, target string) ([]*csi.VolumeUsage, error) {
	var (
		total     uint64
		totalFree uint64
	)
	klog.V(4).Infof("call GetVolumeStats on %s", target)
	dirName, err := windows.UTF16PtrFromString(target)
	if err != nil {
		klog.Errorf("UTF16PtrFromString(%s) failed with %v", target, err)
		return []*csi.VolumeUsage{}, err
	}
	if err := windows.GetDiskFreeSpaceEx(dirName, nil, &total, &totalFree); err != nil {
		klog.Errorf("GetDiskFreeSpaceEx(%s) failed with %v", target, err)
		return []*csi.VolumeUsage{}, err
	}

	return []*csi.VolumeUsage{
		{
			Unit:      csi.VolumeUsage_BYTES,
			Available: int64(totalFree),
			Total:     int64(total),
			Used:      int64(total - totalFree),
		},
	}, nil
}

func removeDir(path string, m *mount.SafeFormatAndMount) error {
	if proxy, ok := m.Interface.(mounter.CSIProxyMounter); ok {
		isExists, err := proxy.ExistsPath(path)
		if err != nil {
			return err
		}

		if isExists {
			klog.V(4).Infof("Removing path: %s", path)
			if err = proxy.Rmdir(path); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("could not cast to csi proxy class")
}

// preparePublishPath - In case of windows, the publish code path creates a soft link
// from global stage path to the publish path. But kubelet creates the directory in advance.
// We work around this issue by deleting the publish path then recreating the link.
func preparePublishPath(path string, m *mount.SafeFormatAndMount) error {
	return removeDir(path, m)
}

func prepareStagePath(path string, m *mount.SafeFormatAndMount) error {
	return removeDir(path, m)
}
