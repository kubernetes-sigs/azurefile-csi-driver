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
	azureFileCSIDriverName = "file.csi.azure.com"
	volumeDataFileName     = "vol_data.json"
)

type volumeData struct {
	DriverName string `json:"driverName"`
}

// Volume discovers Azure File CSI volumes from the node mount table.
type LocalVolume struct {
	getMounts func() ([]*procfs.MountInfo, error)
	readFile  func(string) ([]byte, error)
}

// NewLocalVolume creates a node-local Azure File CSI volume discoverer.
func NewLocalVolume() *LocalVolume {
	return &LocalVolume{
		getMounts: func() ([]*procfs.MountInfo, error) {
			proc, err := procfs.Self()
			if err != nil {
				return nil, fmt.Errorf("failed to open self procfs entry: %w", err)
			}
			return proc.MountInfo()
		},
		readFile: os.ReadFile,
	}
}

// List returns Azure File CSI filesystem targets mounted on this node.
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

	globalFilesystems := make(map[string]struct{})
	volumes := make(map[string]Metadata)
	for _, mount := range mounts {
		metadata, ok := l.metadataForMount(mount)
		if !ok || !l.isGlobalMount(mount.MountPoint) {
			continue
		}
		globalFilesystems[mount.MajorMinorVer] = struct{}{}
		volumes[metadata.key()] = metadata
	}

	for _, mount := range mounts {
		if !l.isPodMount(mount.MountPoint) {
			continue
		}
		metadata, ok := l.metadataForMount(mount)
		if !ok {
			continue
		}
		if _, staged := globalFilesystems[mount.MajorMinorVer]; staged {
			continue
		}
		if !l.isAzureFilePodVolume(mount.MountPoint) {
			continue
		}
		volumes[metadata.key()] = metadata
	}

	result := make(MetadataList, 0, len(volumes))
	for _, metadata := range volumes {
		result = append(result, metadata)
	}
	return result, nil
}

func (l *LocalVolume) metadataForMount(mount *procfs.MountInfo) (Metadata, bool) {
	if mount == nil {
		return Metadata{}, false
	}

	var protocol, account, share string
	var ok bool
	switch strings.ToLower(mount.FSType) {
	case "cifs":
		protocol = ProtocolSMB
		account, share, ok = ParseCIFSTarget(mount.Source)
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
		tail[3] == azureFileCSIDriverName &&
		tail[5] == "globalmount"
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

func (l *LocalVolume) isAzureFilePodVolume(mountPoint string) bool {
	dataPath := filepath.Join(filepath.Dir(filepath.Clean(mountPoint)), volumeDataFileName)
	data, err := l.readFile(dataPath)
	if err != nil {
		klog.V(4).InfoS("Failed to read CSI volume metadata", "path", dataPath, "err", err)
		return false
	}

	var metadata volumeData
	if err := json.Unmarshal(data, &metadata); err != nil {
		klog.V(4).InfoS("Failed to parse CSI volume metadata", "path", dataPath, "err", err)
		return false
	}
	return metadata.DriverName == azureFileCSIDriverName
}
