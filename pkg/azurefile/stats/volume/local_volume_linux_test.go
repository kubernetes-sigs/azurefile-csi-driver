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
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/prometheus/procfs"
)

func TestLocalVolumeList(t *testing.T) {
	const (
		nfsGlobal = "/mnt/custom-kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/nfs-id/globalmount"
		smbGlobal = "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/smb-id/globalmount"
		smbPod    = "/var/lib/kubelet/pods/pod-1/volumes/kubernetes.io~csi/pvc/mount"
		inlinePod = "/var/lib/kubelet/pods/pod-2/volumes/kubernetes.io~csi/inline/mount"
		otherPod  = "/var/lib/kubelet/pods/pod-3/volumes/kubernetes.io~csi/other/mount"
		inlineNFS = "/var/lib/kubelet/pods/pod-4/volumes/kubernetes.io~csi/inline-nfs/mount"
	)

	mounts := []*procfs.MountInfo{
		{
			MountID:       100,
			MajorMinorVer: "0:100",
			MountPoint:    nfsGlobal,
			FSType:        "nfs4",
			Source:        "nfsaccount.file.core.windows.net:/nfsaccount/nfsshare",
		},
		// A propagated replica has a different mount ID but the same filesystem
		// and mountpoint. It must result in one discovered volume.
		{
			MountID:       101,
			MajorMinorVer: "0:100",
			MountPoint:    nfsGlobal,
			FSType:        "nfs4",
			Source:        "nfsaccount.file.core.windows.net:/nfsaccount/nfsshare",
		},
		{
			MountID:       200,
			MajorMinorVer: "0:200",
			MountPoint:    smbGlobal,
			FSType:        "cifs",
			Source:        `\\smbaccount.file.core.windows.net\smbshare`,
		},
		// This is the pod bind view of smbGlobal and must not be added.
		{
			MountID:       201,
			MajorMinorVer: "0:200",
			MountPoint:    smbPod,
			FSType:        "cifs",
			Source:        `\\smbaccount.file.core.windows.net\smbshare`,
		},
		{
			MountID:       300,
			MajorMinorVer: "0:300",
			MountPoint:    inlinePod,
			FSType:        "cifs",
			Source:        `\\inlineaccount.file.core.windows.net\inlineshare`,
		},
		{
			MountID:       400,
			MajorMinorVer: "0:400",
			MountPoint:    otherPod,
			FSType:        "cifs",
			Source:        `\\otheraccount.file.core.windows.net\othershare`,
		},
		{
			MountID:       500,
			MajorMinorVer: "0:500",
			MountPoint:    inlineNFS,
			FSType:        "nfs4",
			Source:        "10.0.0.4:/inlineaccount/inlinenfs/subdirectory",
		},
		{
			MountID:       600,
			MajorMinorVer: "0:600",
			MountPoint:    "/mnt/unrelated",
			FSType:        "cifs",
			Source:        `\\unrelated.file.core.windows.net\share`,
		},
	}
	files := map[string][]byte{
		"/var/lib/kubelet/pods/pod-2/volumes/kubernetes.io~csi/inline/vol_data.json": []byte(
			`{"driverName":"file.csi.azure.com","volumeLifecycleMode":"ephemeral"}`,
		),
		"/var/lib/kubelet/pods/pod-3/volumes/kubernetes.io~csi/other/vol_data.json": []byte(
			`{"driverName":"other.csi.example.com"}`,
		),
		"/var/lib/kubelet/pods/pod-4/volumes/kubernetes.io~csi/inline-nfs/vol_data.json": []byte(
			`{"driverName":"file.csi.azure.com","volumeLifecycleMode":"ephemeral"}`,
		),
	}
	lister := &LocalVolume{
		driverName: "file.csi.azure.com",
		getMounts: func() ([]*procfs.MountInfo, error) {
			return mounts, nil
		},
		readFile: func(path string) ([]byte, error) {
			data, ok := files[path]
			if !ok {
				return nil, fmt.Errorf("file %q not found", path)
			}
			return data, nil
		},
	}

	volumes, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(volumes) != 4 {
		t.Fatalf("List() returned %d volumes, want 4: %+v", len(volumes), volumes)
	}

	got := make(map[string]Metadata, len(volumes))
	for _, metadata := range volumes {
		got[metadata.Protocol+"/"+metadata.StorageAccountName+"/"+metadata.ShareName] = metadata
	}
	if metadata := got["nfs/nfsaccount/nfsshare"]; metadata.MountPoint != nfsGlobal {
		t.Errorf("unexpected NFS metadata: %+v", metadata)
	}
	if metadata := got["smb/smbaccount/smbshare"]; metadata.MountPoint != smbGlobal {
		t.Errorf("unexpected staged SMB metadata: %+v", metadata)
	}
	if metadata := got["smb/inlineaccount/inlineshare"]; metadata.MountPoint != inlinePod {
		t.Errorf("unexpected inline SMB metadata: %+v", metadata)
	}
	if metadata := got["nfs/inlineaccount/inlinenfs"]; metadata.MountPoint != inlineNFS {
		t.Errorf("unexpected inline NFS metadata: %+v", metadata)
	}
}

func TestLocalVolumeSMBEndpointMetadata(t *testing.T) {
	const globalMount = "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount"
	const podMount = "/var/lib/kubelet/pods/pod/volumes/kubernetes.io~csi/pvc/mount"
	tests := []struct {
		name       string
		server     string
		mountPoint string
		data       string
		account    string
	}{
		{
			name: "first IP", server: "10.10.24.244", mountPoint: globalMount,
			data: `{"driverName":"file.csi.azure.com","volumeHandle":"rg#firstaccount#share###"}`, account: "firstaccount",
		},
		{
			name: "second IP", server: "10.20.30.40", mountPoint: globalMount,
			data: `{"driverName":"file.csi.azure.com","volumeHandle":"rg#secondaccount#share###"}`, account: "secondaccount",
		},
		{
			name: "custom hostname", server: "files.example.com", mountPoint: globalMount,
			data: `{"driverName":"file.csi.azure.com","volumeHandle":"rg#actualaccount#share/subdir###"}`, account: "actualaccount",
		},
		{
			name: "pod mount", server: "10.10.24.244", mountPoint: podMount,
			data: `{"driverName":"file.csi.azure.com","volumeHandle":"rg#firstaccount#share###"}`, account: "firstaccount",
		},
		{
			name: "canonical without metadata", server: "account.file.core.windows.net", mountPoint: globalMount,
			account: "account",
		},
		{name: "missing metadata", server: "10.10.24.244", mountPoint: globalMount},
		{
			name: "unknown handle", server: "10.10.24.244", mountPoint: globalMount,
			data: `{"driverName":"file.csi.azure.com","volumeHandle":"opaque-id"}`,
		},
		{
			name: "other driver", server: "10.10.24.244", mountPoint: globalMount,
			data: `{"driverName":"other.csi.example.com","volumeHandle":"rg#account#share###"}`,
		},
		{
			name: "wrong share", server: "10.10.24.244", mountPoint: globalMount,
			data: `{"driverName":"file.csi.azure.com","volumeHandle":"rg#account#other-share###"}`,
		},
		{
			name: "empty account", server: "10.10.24.244", mountPoint: globalMount,
			data: `{"driverName":"file.csi.azure.com","volumeHandle":"rg##share###"}`,
		},
		{name: "malformed metadata", server: "10.10.24.244", mountPoint: globalMount, data: "{"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reads := 0
			lister := &LocalVolume{
				driverName: "file.csi.azure.com",
				getMounts: func() ([]*procfs.MountInfo, error) {
					return []*procfs.MountInfo{{
						MountPoint: test.mountPoint,
						FSType:     "cifs",
						Source:     "//" + test.server + "/share",
					}}, nil
				},
				readFile: func(path string) ([]byte, error) {
					reads++
					if want := filepath.Join(filepath.Dir(test.mountPoint), volumeDataFileName); path != want {
						t.Errorf("metadata path = %q, want %q", path, want)
					}
					if test.data == "" {
						return nil, errors.New("metadata unavailable")
					}
					return []byte(test.data), nil
				},
			}
			volumes, err := lister.List(context.Background())
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			wantReads := 1
			if test.name == "canonical without metadata" {
				wantReads = 0
			}
			if reads != wantReads {
				t.Errorf("metadata reads = %d, want %d", reads, wantReads)
			}
			if test.account == "" {
				if len(volumes) != 0 {
					t.Fatalf("List() returned unresolved volumes: %+v", volumes)
				}
				return
			}
			if len(volumes) != 1 {
				t.Fatalf("List() returned %d volumes, want 1", len(volumes))
			}
			if got := volumes[0]; got.Server != test.server || got.StorageAccountName != test.account || got.ShareName != "share" {
				t.Errorf("unexpected SMB metadata: %+v", got)
			}
		})
	}
}

func TestReadVolumeDataRetriesReadErrors(t *testing.T) {
	reads := 0
	lister := &LocalVolume{
		driverName: "file.csi.azure.com",
		readFile: func(string) ([]byte, error) {
			reads++
			if reads == 1 {
				return nil, errors.New("metadata temporarily unavailable")
			}
			return []byte(`{"driverName":"file.csi.azure.com","volumeHandle":"rg#account#share###"}`), nil
		},
	}
	cache := make(map[string]volumeDataResult)
	if _, ok := lister.readVolumeData("/volume/mount", cache); ok {
		t.Fatal("readVolumeData() succeeded despite read error")
	}
	for range 2 {
		data, ok := lister.readVolumeData("/volume/mount", cache)
		if !ok || data.VolumeHandle != "rg#account#share###" {
			t.Fatalf("readVolumeData() = (%+v, %t), want valid metadata", data, ok)
		}
	}
	if reads != 2 {
		t.Errorf("metadata reads = %d, want 2", reads)
	}
}

func TestLocalVolumeMetadataCacheRefreshes(t *testing.T) {
	const globalMount = "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount"
	mount := &procfs.MountInfo{
		MountPoint: globalMount, FSType: "cifs",
		Source: "//10.10.24.244/share", MajorMinorVer: "0:100",
	}
	podMount := *mount
	podMount.MountPoint = "/var/lib/kubelet/pods/pod/volumes/kubernetes.io~csi/pvc/mount"
	reads := 0
	account := "firstaccount"
	lister := &LocalVolume{
		driverName: "file.csi.azure.com",
		getMounts: func() ([]*procfs.MountInfo, error) {
			return []*procfs.MountInfo{mount, mount, &podMount}, nil
		},
		readFile: func(path string) ([]byte, error) {
			reads++
			if want := filepath.Join(filepath.Dir(globalMount), volumeDataFileName); path != want {
				t.Errorf("metadata path = %q, want %q", path, want)
			}
			return []byte(fmt.Sprintf(`{"driverName":"file.csi.azure.com","volumeHandle":"rg#%s#share###"}`, account)), nil
		},
	}
	for i, name := range []string{"firstaccount", "secondaccount"} {
		account = name
		volumes, err := lister.List(context.Background())
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(volumes) != 1 || volumes[0].StorageAccountName != account {
			t.Fatalf("List() = %+v, want one volume for %s", volumes, account)
		}
		if reads != i+1 {
			t.Errorf("metadata reads = %d, want %d", reads, i+1)
		}
	}
}

func TestLocalVolumeListCancellation(t *testing.T) {
	for _, phase := range []string{"before discovery", "mountinfo", "global metadata", "pod metadata"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := 0
			lister := &LocalVolume{
				driverName: "file.csi.azure.com",
				getMounts: func() ([]*procfs.MountInfo, error) {
					if phase == "before discovery" {
						t.Error("mountinfo read after cancellation")
					}
					if phase == "mountinfo" {
						cancel()
					}
					mountPoint := "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount"
					if phase == "pod metadata" {
						mountPoint = "/var/lib/kubelet/pods/pod/volumes/kubernetes.io~csi/pvc/mount"
					}
					mount := &procfs.MountInfo{MountPoint: mountPoint, FSType: "cifs", Source: "//10.10.24.244/share"}
					return []*procfs.MountInfo{mount, mount}, nil
				},
				readFile: func(string) ([]byte, error) {
					reads++
					cancel()
					return []byte(`{"driverName":"file.csi.azure.com","volumeHandle":"rg#account#share###"}`), nil
				},
			}
			if phase == "before discovery" {
				cancel()
			}
			volumes, err := lister.List(ctx)
			if !errors.Is(err, context.Canceled) || volumes != nil {
				t.Fatalf("List() = (%+v, %v), want (nil, context.Canceled)", volumes, err)
			}
			wantReads := 0
			if phase == "global metadata" || phase == "pod metadata" {
				wantReads = 1
			}
			if reads != wantReads {
				t.Errorf("metadata reads = %d, want %d", reads, wantReads)
			}
		})
	}
}

func TestLocalVolumeListMountInfoError(t *testing.T) {
	expected := errors.New("mountinfo unavailable")
	lister := &LocalVolume{
		getMounts: func() ([]*procfs.MountInfo, error) {
			return nil, expected
		},
		readFile: func(string) ([]byte, error) {
			return nil, nil
		},
	}

	if _, err := lister.List(context.Background()); !errors.Is(err, expected) {
		t.Fatalf("List() error = %v, want %v", err, expected)
	}
}
