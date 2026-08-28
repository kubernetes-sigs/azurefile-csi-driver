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
	"testing"

	"github.com/prometheus/procfs"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile/stats/volume"
)

func TestNewStatsCollection(t *testing.T) {
	cifs := CIFSStats{
		Device:       `\\account.file.core.windows.net\share`,
		BytesRead:    100,
		BytesWritten: 200,
		ReadsTotal:   10,
		ReadsFailed:  1,
		WritesTotal:  20,
		WritesFailed: 2,
	}
	nfs := NFSStats{
		Device:         "account.file.core.windows.net:/account/share",
		MountPoint:     "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount",
		FilesystemType: "nfs4",
		MountStats: procfs.MountStatsNFS{
			Bytes: procfs.NFSBytesStats{
				ReadTotal:  300,
				WriteTotal: 400,
			},
			Operations: []procfs.NFSOperationStats{
				{Operation: "READ", Requests: 30, Errors: 3},
				{Operation: "WRITE", Requests: 40, Errors: 4},
			},
		},
	}

	volumes := volume.MetadataList{{
		StorageAccountName: "account",
		ShareName:          "share",
	}}
	collection := NewStatsCollection(volumes, []CIFSStats{cifs}, []NFSStats{nfs})
	if len(collection.Filesystems) != 2 {
		t.Fatalf("filesystem count = %d, want 2", len(collection.Filesystems))
	}

	smb := collection.Filesystems[0]
	if smb.Protocol != ProtocolSMB || smb.StorageAccount != "account" || smb.FileShare != "share" {
		t.Errorf("unexpected SMB identity: %+v", smb)
	}
	if smb.BytesRead != 100 || smb.BytesWritten != 200 {
		t.Errorf("unexpected SMB common fields: %+v", smb)
	}
	if smb.Operations["read"] != (OperationStats{Requests: 10, Errors: 1}) {
		t.Errorf("unexpected SMB read operation: %+v", smb.Operations["read"])
	}

	nfsSample := collection.Filesystems[1]
	if nfsSample.Protocol != ProtocolNFS ||
		nfsSample.StorageAccount != "account" ||
		nfsSample.FileShare != "share" {
		t.Errorf("unexpected NFS identity: %+v", nfsSample)
	}
	if nfsSample.BytesRead != 300 || nfsSample.BytesWritten != 400 {
		t.Errorf("unexpected NFS bytes: %+v", nfsSample)
	}
	if nfsSample.Operations["write"] != (OperationStats{Requests: 40, Errors: 4}) {
		t.Errorf("unexpected NFS write operation: %+v", nfsSample.Operations["write"])
	}
}

func TestNewStatsCollectionFiltersNonAzureFileVolumes(t *testing.T) {
	volumes := volume.MetadataList{{
		StorageAccountName: "account",
		ShareName:          "owned-share",
	}}
	cifsStats := []CIFSStats{
		{Device: `\\account.file.core.windows.net\owned-share`},
		{Device: `\\account.file.core.windows.net\other-share`},
		{Device: `\\other.file.core.windows.net\owned-share`},
	}
	nfsStats := []NFSStats{
		{
			Device:     "account.file.core.windows.net:/account/owned-share",
			MountPoint: "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount",
		},
		{
			Device:     "account.file.core.windows.net:/account/owned-share",
			MountPoint: "/var/lib/kubelet/pods/id/volumes/kubernetes.io~csi/pvc/mount",
		},
		{
			Device:     "account.file.core.windows.net:/account/other-share",
			MountPoint: "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id2/globalmount",
		},
	}

	collection := NewStatsCollection(volumes, cifsStats, nfsStats)
	if len(collection.Filesystems) != 2 {
		t.Fatalf("filesystem count = %d, want 2: %+v", len(collection.Filesystems), collection.Filesystems)
	}
	if collection.Filesystems[0].Protocol != ProtocolSMB ||
		collection.Filesystems[1].Protocol != ProtocolNFS {
		t.Errorf("unexpected protocols: %+v", collection.Filesystems)
	}
}

func TestNewStatsCollectionRequiresVolumeIdentity(t *testing.T) {
	collection := NewStatsCollection(
		volume.MetadataList{{PVName: "pv-without-account-or-share"}},
		[]CIFSStats{{Device: `\\account.file.core.windows.net\share`}},
		[]NFSStats{{
			Device:     "account.file.core.windows.net:/account/share",
			MountPoint: "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount",
		}},
	)
	if len(collection.Filesystems) != 0 {
		t.Fatalf("filesystem count = %d, want 0", len(collection.Filesystems))
	}
}

func TestNewStatsCollectionAggregatesDuplicateTargets(t *testing.T) {
	collection := NewStatsCollection(
		volume.MetadataList{{
			StorageAccountName: "account",
			ShareName:          "share",
		}},
		[]CIFSStats{
			{
				Device:      `\\account.file.core.windows.net\share`,
				BytesRead:   100,
				ReadsTotal:  10,
				ReadsFailed: 1,
			},
			{
				Device:      `\\account.file.core.windows.net\share`,
				BytesRead:   200,
				ReadsTotal:  20,
				ReadsFailed: 2,
			},
		},
		nil,
	)

	if len(collection.Filesystems) != 1 {
		t.Fatalf("filesystem count = %d, want 1", len(collection.Filesystems))
	}
	filesystem := collection.Filesystems[0]
	if filesystem.BytesRead != 300 {
		t.Errorf("read bytes = %d, want 300", filesystem.BytesRead)
	}
	if got := filesystem.Operations["read"]; got != (OperationStats{Requests: 30, Errors: 3}) {
		t.Errorf("read operation = %+v, want 30 requests and 3 errors", got)
	}
}
