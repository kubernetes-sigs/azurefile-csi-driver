/*
Copyright 2026 The Kubernetes Authors.

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

package volume

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prometheus/procfs"
	"k8s.io/klog/v2"
)

const (
	volumeDataFileName = "vol_data.json"
)

type volumeData struct {
	DriverName   string `json:"driverName"`
	VolumeHandle string `json:"volumeHandle"`
}

type volumeDataResult struct {
	data  volumeData
	valid bool
}

// LocalVolume discovers Azure File CSI volumes from the node mount table by using helper functions.
type LocalVolume struct {
	driverName string
	getMounts  func() ([]*procfs.MountInfo, error)
	readFile   func(string) ([]byte, error)
}

// NewLocalVolume creates a node-local Azure File CSI volume discoverer.
func NewLocalVolume(driverName string) *LocalVolume {
	return &LocalVolume{
		driverName: driverName,
		getMounts: func() ([]*procfs.MountInfo, error) {
			return procfs.GetMounts()
		},
		readFile: os.ReadFile,
	}
}

// List returns Azure File CSI filesystem targets mounted on this node.
// Metadata reads are cached only for this call so later scrapes see mount changes.
// Cancellation is checked between synchronous filesystem operations.
func (l *LocalVolume) List(ctx context.Context) (MetadataList, error) {
	if l == nil || l.getMounts == nil || l.readFile == nil {
		return nil, fmt.Errorf("local volume discoverer is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	mounts, err := l.getMounts()
	if err != nil {
		return nil, fmt.Errorf("failed to read mount information: %w", err)
	}

	metadataCache := make(map[string]volumeDataResult)
	globalFilesystems := make(map[string]struct{})
	volumes := make(map[string]Metadata)
	for _, mount := range mounts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if mount == nil || !l.isGlobalMount(mount.MountPoint) {
			continue
		}
		metadata, ok := l.metadataForMount(mount, metadataCache)
		if !ok {
			continue
		}
		globalFilesystems[mount.MajorMinorVer] = struct{}{}
		volumes[metadata.key()] = metadata
	}

	for _, mount := range mounts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if mount == nil || !l.isPodMount(mount.MountPoint) {
			continue
		}
		if _, staged := globalFilesystems[mount.MajorMinorVer]; staged {
			continue
		}
		metadata, ok := l.metadataForMount(mount, metadataCache)
		if !ok {
			continue
		}
		if _, ok := l.readVolumeData(mount.MountPoint, metadataCache); !ok {
			continue
		}
		volumes[metadata.key()] = metadata
	}

	result := make(MetadataList, 0, len(volumes))
	for _, metadata := range volumes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result = append(result, metadata)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (l *LocalVolume) metadataForMount(mount *procfs.MountInfo, cache map[string]volumeDataResult) (Metadata, bool) {
	if mount == nil {
		return Metadata{}, false
	}

	var protocol, account, share, server string
	var ok bool
	switch strings.ToLower(mount.FSType) {
	case "cifs":
		protocol = ProtocolSMB
		server, share, ok = ParseCIFSTarget(mount.Source)
		if !ok {
			return Metadata{}, false
		}
		account = storageAccountFromServer(server)
		if account == "" {
			data, valid := l.readVolumeData(mount.MountPoint, cache)
			if !valid {
				return Metadata{}, false
			}
			// Kubelet persists the CSI volume handle alongside the mount.
			// Azure File handles use resource-group#account#share#... .
			parts := strings.Split(data.VolumeHandle, "#")
			if len(parts) < 3 {
				klog.Warningf("Cannot resolve storage account from CSI metadata for SMB mount %s", mount.MountPoint)
				return Metadata{}, false
			}
			var metadataShare string
			account, metadataShare, ok = normalizeAccountAndShare(parts[1], strings.SplitN(parts[2], "/", 2)[0])
			if !ok || metadataShare != share {
				klog.Warningf("CSI metadata does not identify SMB mount %s", mount.MountPoint)
				return Metadata{}, false
			}
		}
	case "nfs", "nfs4":
		protocol = ProtocolNFS
		account, share, ok = ParseNFSTarget(mount.Source)
	default:
		return Metadata{}, false
	}
	if !ok {
		return Metadata{}, false
	}

	return Metadata{
		Protocol:           protocol,
		StorageAccountName: account,
		ShareName:          share,
		Server:             server,
		MountPoint:         filepath.Clean(mount.MountPoint),
		FilesystemID:       mount.MajorMinorVer,
	}, true
}

func (l *LocalVolume) isGlobalMount(mountPoint string) bool {
	parts := mountParts(mountPoint)
	if len(parts) < 6 {
		return false
	}
	tail := parts[len(parts)-6:]
	return tail[0] == "plugins" &&
		tail[1] == "kubernetes.io" &&
		tail[2] == "csi" &&
		tail[3] == l.driverName &&
		(tail[5] == "globalmount" || tail[5] == "proxy-mount")
}

func (l *LocalVolume) isPodMount(mountPoint string) bool {
	parts := mountParts(mountPoint)
	if len(parts) < 6 {
		return false
	}
	tail := parts[len(parts)-6:]
	return tail[0] == "pods" &&
		tail[2] == "volumes" &&
		tail[3] == "kubernetes.io~csi" &&
		tail[5] == "mount"
}

func mountParts(mountPoint string) []string {
	clean := strings.Trim(filepath.ToSlash(filepath.Clean(mountPoint)), "/")
	if clean == "" || clean == "." {
		return nil
	}
	return strings.Split(clean, "/")
}

func (l *LocalVolume) readVolumeData(mountPoint string, cache map[string]volumeDataResult) (volumeData, bool) {
	dataPath := filepath.Join(filepath.Dir(filepath.Clean(mountPoint)), volumeDataFileName)
	if result, ok := cache[dataPath]; ok {
		return result.data, result.valid
	}
	data, err := l.readFile(dataPath)
	if err != nil {
		klog.V(4).InfoS("Failed to read CSI volume metadata", "path", dataPath, "err", err)
		return volumeData{}, false
	}
	cache[dataPath] = volumeDataResult{}

	var metadata volumeData
	if err := json.Unmarshal(data, &metadata); err != nil {
		klog.V(4).InfoS("Failed to parse CSI volume metadata", "path", dataPath, "err", err)
		return volumeData{}, false
	}
	valid := metadata.DriverName == l.driverName
	cache[dataPath] = volumeDataResult{data: metadata, valid: valid}
	return metadata, valid
}
