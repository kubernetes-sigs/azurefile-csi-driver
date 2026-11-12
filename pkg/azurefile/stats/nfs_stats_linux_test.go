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
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/procfs"
)

func TestProcessNFSStats(t *testing.T) {
	const mountStats = `device tmpfs mounted on /run with fstype tmpfs
device account.file.core.windows.net:/account/share mounted on /var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount with fstype nfs4 statvers=1.1
	opts:	rw,vers=4.1,nconnect=2
	age:	140
	events:	1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27
	bytes:	100 200 0 0 300 400 5 6
	xprt:	tcp 0 0 2 0 0 10 9 0 20 3 4 5 6
	xprt:	tcp 0 0 2 0 0 11 10 0 21 4 5 6 7
	per-op statistics
		READ: 8 9 1 1000 2000 3 4 5 1
		WRITE: 6 7 0 3000 4000 8 9 10 0

device account.file.core.windows.net:/account/share mounted on /var/lib/kubelet/pods/id/volumes/kubernetes.io~csi/pvc/mount with fstype nfs4 statvers=1.1
	opts:	rw,vers=4.1,nconnect=2
	age:	140
	events:	1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27
	bytes:	100 200 0 0 300 400 5 6
	per-op statistics
		READ: 8 9 1 1000 2000 3 4 5 1

`

	proc := newTestProc(t, mountStats)
	stats, err := processNFSStats(proc)
	if err != nil {
		t.Fatalf("processNFSStats() error = %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("processNFSStats() returned %d records, want 2", len(stats))
	}

	first := stats[0]
	if first.Device != "account.file.core.windows.net:/account/share" {
		t.Errorf("Device = %q", first.Device)
	}
	if first.FilesystemType != "nfs4" {
		t.Errorf("FilesystemType = %q, want nfs4", first.FilesystemType)
	}
	if first.MountStats.Bytes.ReadTotal != 300 || first.MountStats.Bytes.WriteTotal != 400 {
		t.Errorf("server bytes = (%d, %d), want (300, 400)",
			first.MountStats.Bytes.ReadTotal, first.MountStats.Bytes.WriteTotal)
	}
	if len(first.MountStats.Transport) != 2 {
		t.Errorf("transport count = %d, want 2", len(first.MountStats.Transport))
	}
	if len(first.MountStats.Operations) != 2 {
		t.Fatalf("operation count = %d, want 2", len(first.MountStats.Operations))
	}
	if first.MountStats.Operations[0].Operation != "READ" ||
		first.MountStats.Operations[0].Requests != 8 ||
		first.MountStats.Operations[0].Errors != 1 {
		t.Errorf("READ operation was not parsed: %+v", first.MountStats.Operations[0])
	}

	if first.MountPoint == stats[1].MountPoint {
		t.Errorf("bind mount and staging mount should have distinct mount points")
	}
}

func TestProcessNFSStatsReadError(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "123"), 0o700); err != nil {
		t.Fatalf("failed to create fake proc entry: %v", err)
	}

	fs, err := procfs.NewFS(root)
	if err != nil {
		t.Fatalf("procfs.NewFS() error = %v", err)
	}
	proc, err := fs.Proc(123)
	if err != nil {
		t.Fatalf("fs.Proc() error = %v", err)
	}

	if _, err := processNFSStats(proc); err == nil {
		t.Fatal("processNFSStats() error = nil, want read error")
	}
}

func newTestProc(t *testing.T, mountStats string) procfs.Proc {
	t.Helper()

	root := t.TempDir()
	procDir := filepath.Join(root, "123")
	if err := os.Mkdir(procDir, 0o700); err != nil {
		t.Fatalf("failed to create fake proc entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "mountstats"), []byte(mountStats), 0o600); err != nil {
		t.Fatalf("failed to write mountstats: %v", err)
	}

	fs, err := procfs.NewFS(root)
	if err != nil {
		t.Fatalf("procfs.NewFS() error = %v", err)
	}
	proc, err := fs.Proc(123)
	if err != nil {
		t.Fatalf("fs.Proc() error = %v", err)
	}
	return proc
}
