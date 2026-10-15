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

import "context"

const (
	// ProtocolSMB identifies a CIFS/SMB mount.
	ProtocolSMB = "smb"
	// ProtocolNFS identifies an NFS mount.
	ProtocolNFS = "nfs"
)

// Metadata identifies a locally mounted Azure File target.
type Metadata struct {
	Protocol           string
	StorageAccountName string
	ShareName          string
	MountPoint         string
	// FilesystemID is the mountinfo major:minor identity used to deduplicate
	// bind mounts and mount-propagation replicas.
	FilesystemID string
}

func (m Metadata) key() string {
	return m.Protocol + "\x00" + m.StorageAccountName + "\x00" + m.ShareName + "\x00" + m.MountPoint
}

// MetadataList is a list of locally mounted Azure File targets.
type MetadataList []Metadata

// MetadataLister discovers locally mounted Azure File targets.
type MetadataLister interface {
	List(ctx context.Context) (MetadataList, error)
}
