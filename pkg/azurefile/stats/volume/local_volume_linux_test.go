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
