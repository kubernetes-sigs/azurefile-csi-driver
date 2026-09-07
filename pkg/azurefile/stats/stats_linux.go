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

package stats

import (
	"path/filepath"
	"strings"

	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile/stats/volume"
)

// Protocol identifies the network filesystem protocol that produced a sample.
type Protocol string

const (
	// ProtocolSMB identifies statistics from the Linux CIFS/SMB client.
	ProtocolSMB Protocol = "smb"
	// ProtocolNFS identifies statistics from the Linux NFS client.
	ProtocolNFS Protocol = "nfs"
)

// OperationStats contains the common counters available for a filesystem
// protocol operation.
type OperationStats struct {
	// Requests is the total number of requests observed for the operation.
	Requests uint64
	// Errors is the number of requests reported as failed or errored.
	//
	// The kernel sources have protocol-specific semantics: CIFS counts failed
	// SMB commands, while NFS counts operations completed with tk_status < 0.
	Errors uint64
}

// FilesystemStats is the protocol-neutral projection of one remote CIFS share
// or NFS export. Protocol-specific details remain in CIFSStats and NFSStats.
type FilesystemStats struct {
	// Protocol identifies whether the counters came from SMB or NFS.
	Protocol Protocol
	// StorageAccount identifies the Azure storage account hosting the share.
	StorageAccount string
	// FileShare identifies the Azure file share.
	FileShare string
	// BytesRead is the number of bytes received from the remote filesystem.
	BytesRead uint64
	// BytesWritten is the number of bytes sent to the remote filesystem.
	BytesWritten uint64
	// Operations contains request and error counters keyed by a normalized,
	// lower-case protocol operation name.
	Operations map[string]OperationStats
}

// Collection accumulates protocol-neutral statistics from CIFS and NFS.
type Collection struct {
	Filesystems []FilesystemStats
}

// NewCollection projects only kernel statistics that match a locally
// mounted volume owned by file.csi.azure.com.
func NewCollection(volumes volume.MetadataList, cifsStats []CIFSStats, nfsStats []NFSStats) *Collection {
	collection := &Collection{
		Filesystems: make([]FilesystemStats, 0, len(cifsStats)+len(nfsStats)),
	}
	filesystemIndexes := make(map[string]int)

	volumeTargets := newVolumeTargetSet(volumes)
	for _, stat := range cifsStats {
		account, share, ok := volume.ParseCIFSTarget(stat.Device)
		if ok && volumeTargets.containsCIFS(account, share) {
			collection.add(projectCIFSStats(stat, account, share), filesystemIndexes)
		}
	}
	seenNFSMounts := make(map[string]struct{})
	for _, stat := range nfsStats {
		account, share, ok := volume.ParseNFSTarget(stat.Device)
		filesystemID, matched := volumeTargets.nfsFilesystem(account, share, stat.MountPoint)
		if !ok || !matched {
			continue
		}
		mountKey := filesystemID
		if mountKey == "" {
			mountKey = strings.ToLower(strings.TrimSpace(stat.Device))
		}
		if _, seen := seenNFSMounts[mountKey]; seen {
			continue
		}
		seenNFSMounts[mountKey] = struct{}{}
		collection.add(projectNFSStats(stat, account, share), filesystemIndexes)
	}
	return collection
}

func (c *Collection) add(filesystem FilesystemStats, indexes map[string]int) {
	key := string(filesystem.Protocol) + "\x00" + filesystem.StorageAccount + "\x00" + filesystem.FileShare
	if index, ok := indexes[key]; ok {
		existing := &c.Filesystems[index]
		existing.BytesRead += filesystem.BytesRead
		existing.BytesWritten += filesystem.BytesWritten
		for operation, operationStats := range filesystem.Operations {
			current := existing.Operations[operation]
			current.Requests += operationStats.Requests
			current.Errors += operationStats.Errors
			existing.Operations[operation] = current
		}
		return
	}
	indexes[key] = len(c.Filesystems)
	c.Filesystems = append(c.Filesystems, filesystem)
}

type volumeTargetSet struct {
	cifs map[string]struct{}
	nfs  map[string]string
}

func newVolumeTargetSet(volumes volume.MetadataList) volumeTargetSet {
	targets := volumeTargetSet{
		cifs: make(map[string]struct{}),
		nfs:  make(map[string]string),
	}
	for _, metadata := range volumes {
		account := strings.ToLower(strings.TrimSpace(metadata.StorageAccountName))
		share := strings.ToLower(strings.Trim(strings.TrimSpace(metadata.ShareName), `/\`))
		if account == "" || share == "" {
			continue
		}
		target := account + "/" + share
		switch metadata.Protocol {
		case volume.ProtocolSMB:
			targets.cifs[target] = struct{}{}
		case volume.ProtocolNFS:
			targets.nfs[target+"\x00"+filepath.Clean(metadata.MountPoint)] = metadata.FilesystemID
		}
	}
	return targets
}

func (s volumeTargetSet) containsCIFS(account, share string) bool {
	_, ok := s.cifs[account+"/"+share]
	return ok
}

func (s volumeTargetSet) nfsFilesystem(account, share, mountPoint string) (string, bool) {
	filesystemID, ok := s.nfs[account+"/"+share+"\x00"+filepath.Clean(mountPoint)]
	return filesystemID, ok
}

func projectCIFSStats(stat CIFSStats, account, share string) FilesystemStats {
	return FilesystemStats{
		Protocol:       ProtocolSMB,
		StorageAccount: account,
		FileShare:      share,
		BytesRead:      stat.BytesRead,
		BytesWritten:   stat.BytesWritten,
		Operations: map[string]OperationStats{
			"tree_connect":    {Requests: stat.TreeConnectTotal, Errors: stat.TreeConnectFailed},
			"tree_disconnect": {Requests: stat.TreeDisconnectTotal, Errors: stat.TreeDisconnectFailed},
			"create":          {Requests: stat.CreatesTotal, Errors: stat.CreatesFailed},
			"close":           {Requests: stat.ClosesTotal, Errors: stat.ClosesFailed},
			"flush":           {Requests: stat.FlushesTotal, Errors: stat.FlushesFailed},
			"read":            {Requests: stat.ReadsTotal, Errors: stat.ReadsFailed},
			"write":           {Requests: stat.WritesTotal, Errors: stat.WritesFailed},
			"lock":            {Requests: stat.LocksTotal, Errors: stat.LocksFailed},
			"ioctl":           {Requests: stat.IOCTLSTotal, Errors: stat.IOCTLSFailed},
			"query_directory": {Requests: stat.QueryDirectoriesTotal, Errors: stat.QueryDirectoriesFailed},
			"change_notify":   {Requests: stat.ChangeNotifiesTotal, Errors: stat.ChangeNotifiesFailed},
			"query_info":      {Requests: stat.QueryInfosTotal, Errors: stat.QueryInfosFailed},
			"set_info":        {Requests: stat.SetInfosTotal, Errors: stat.SetInfosFailed},
			"oplock_break":    {Requests: stat.OplockBreaksSent, Errors: stat.OplockBreaksFailed},
		},
	}
}

func projectNFSStats(stat NFSStats, account, share string) FilesystemStats {
	operations := make(map[string]OperationStats, len(stat.MountStats.Operations))
	for _, operation := range stat.MountStats.Operations {
		name := strings.ToLower(operation.Operation)
		operations[name] = OperationStats{
			Requests: operation.Requests,
			Errors:   operation.Errors,
		}
	}

	return FilesystemStats{
		Protocol:       ProtocolNFS,
		StorageAccount: account,
		FileShare:      share,
		BytesRead:      stat.MountStats.Bytes.ReadTotal,
		BytesWritten:   stat.MountStats.Bytes.WriteTotal,
		Operations:     operations,
	}
}
