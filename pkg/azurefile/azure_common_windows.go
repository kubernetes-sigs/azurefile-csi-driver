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
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/windows"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/volume"
	mount "k8s.io/mount-utils"
	"sigs.k8s.io/azurefile-csi-driver/pkg/mounter"
)

func SMBMount(m *mount.SafeFormatAndMount, source, target, fsType string, mountOptions, sensitiveMountOptions []string) error {
	if proxy, ok := m.Interface.(mounter.CSIProxyMounter); ok {
		return proxy.SMBMount(source, target, fsType, mountOptions, sensitiveMountOptions)
	}
	return fmt.Errorf("could not cast to csi proxy class")
}

func SMBUnmount(m *mount.SafeFormatAndMount, target string, extensiveMountCheck, removeSMBMountOnWindows bool) error {
	if proxy, ok := m.Interface.(mounter.CSIProxyMounter); ok {
		if removeSMBMountOnWindows {
			return proxy.Unmount(target)
		}
		return proxy.Rmdir(target)
	}
	return fmt.Errorf("could not cast to csi proxy class")
}

func CleanupMountPoint(m *mount.SafeFormatAndMount, target string, extensiveMountCheck bool) error {
	if proxy, ok := m.Interface.(mounter.CSIProxyMounter); ok {
		return proxy.Rmdir(target)
	}
	return fmt.Errorf("could not cast to csi proxy class")
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

// GetFreeSpace returns the free space of the volume in bytes, total size of the volume in bytes and the used space of the volume in bytes
func GetFreeSpace(path string) (int64, int64, int64, error) {
	var totalNumberOfBytes, totalNumberOfFreeBytes uint64
	dirName := windows.StringToUTF16Ptr(path)
	if err := windows.GetDiskFreeSpaceEx(dirName, nil, &totalNumberOfBytes, &totalNumberOfFreeBytes); err != nil {
		return 0, 0, 0, err
	}
	return int64(totalNumberOfFreeBytes), int64(totalNumberOfBytes), int64(totalNumberOfBytes - totalNumberOfFreeBytes), nil
}

// GetVolumeStats returns volume stats based on the given path.
func GetVolumeStats(path string, enableWindowsHostProcess bool) (*csi.NodeGetVolumeStatsResponse, error) {
	if enableWindowsHostProcess {
		// only in host process mode, we can get the free space of the volume, otherwise, we will get permission denied error
		freeBytesAvailable, totalBytes, totalBytesUsed, err := GetFreeSpace(path)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get free space on path %s: %v", path, err)
		}
		return &csi.NodeGetVolumeStatsResponse{
			Usage: []*csi.VolumeUsage{
				{
					Unit:      csi.VolumeUsage_BYTES,
					Available: freeBytesAvailable,
					Total:     totalBytes,
					Used:      totalBytesUsed,
				},
			},
		}, nil
	}

	volumeMetrics, err := volume.NewMetricsStatFS(path).GetMetrics()
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
	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: available,
				Total:     capacity,
				Used:      used,
			},
		},
	}, nil
}
