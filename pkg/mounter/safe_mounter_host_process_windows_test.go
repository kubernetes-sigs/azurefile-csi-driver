//go:build windows
// +build windows

/*
Copyright 2025 The Kubernetes Authors.

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

package mounter

import (
	"errors"
	"reflect"
	"testing"

	"k8s.io/utils/keymutex"

	"sigs.k8s.io/azurefile-csi-driver/pkg/os/smb"
)

func TestGetRemotePathLockKey(t *testing.T) {
	tests := []struct {
		name       string
		remotePath string
		want       string
	}{
		{name: "canonical unc path", remotePath: `\\server\share`, want: `\\server\share`},
		{name: "single trailing slash is normalized", remotePath: `\\server\share\`, want: `\\server\share`},
		{name: "multiple trailing slashes are normalized", remotePath: `\\server\share\\`, want: `\\server\share`},
		{name: "path is lowercased", remotePath: `\\Server\Share\`, want: `\\server\share`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getRemotePathLockKey(tt.remotePath); got != tt.want {
				t.Fatalf("getRemotePathLockKey(%q) = %q, want %q", tt.remotePath, got, tt.want)
			}
		})
	}
}

type fakeSMBAPI struct {
	status          smb.SMBGlobalMappingStatus
	statusErr       error
	removeErr       error
	calls           []string
	newMappingPaths []string
}

func (f *fakeSMBAPI) GetSmbGlobalMappingStatus(remotePath string) (smb.SMBGlobalMappingStatus, error) {
	f.calls = append(f.calls, "status:"+remotePath)
	return f.status, f.statusErr
}

func (f *fakeSMBAPI) NewSmbGlobalMapping(remotePath, username, password string) error {
	f.calls = append(f.calls, "new:"+remotePath)
	f.newMappingPaths = append(f.newMappingPaths, remotePath)
	return nil
}

func (f *fakeSMBAPI) RemoveSmbGlobalMapping(remotePath string) error {
	f.calls = append(f.calls, "remove:"+remotePath)
	return f.removeErr
}

func TestEnsureSMBGlobalMapping_RecreatesDisconnectedMapping(t *testing.T) {
	api := &fakeSMBAPI{status: smb.SMBGlobalMappingStatusDisconnected}
	mounter := &winMounter{smbAPI: api, remotePathLocks: keymutex.NewHashed(0)}
	pathValidCalled := false

	err := mounter.ensureSMBGlobalMappingLocked(`\\server\share`, "user", "pass", func(string) (bool, error) {
		pathValidCalled = true
		return true, nil
	})
	if err != nil {
		t.Fatalf("ensureSMBGlobalMapping returned error: %v", err)
	}
	if pathValidCalled {
		t.Fatal("pathValidFn should not be called for disconnected mappings")
	}
	wantCalls := []string{`status:\\server\share`, `remove:\\server\share`, `new:\\server\share`}
	if !reflect.DeepEqual(api.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", api.calls, wantCalls)
	}
}

func TestEnsureSMBGlobalMapping_DisconnectedRemoveFailureStopsRecreate(t *testing.T) {
	removeErr := errors.New("remove failed")
	api := &fakeSMBAPI{status: smb.SMBGlobalMappingStatusDisconnected, removeErr: removeErr}
	mounter := &winMounter{smbAPI: api, remotePathLocks: keymutex.NewHashed(0)}

	err := mounter.ensureSMBGlobalMappingLocked(`\\server\share`, "user", "pass", func(string) (bool, error) {
		return true, nil
	})
	if !errors.Is(err, removeErr) {
		t.Fatalf("ensureSMBGlobalMapping error = %v, want %v", err, removeErr)
	}
	wantCalls := []string{`status:\\server\share`, `remove:\\server\share`}
	if !reflect.DeepEqual(api.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", api.calls, wantCalls)
	}
}

func TestEnsureSMBGlobalMapping_OtherStatusFallsBackToRemap(t *testing.T) {
	api := &fakeSMBAPI{status: smb.SMBGlobalMappingStatusOther}
	mounter := &winMounter{smbAPI: api, remotePathLocks: keymutex.NewHashed(0)}
	pathValidCalled := false

	err := mounter.ensureSMBGlobalMappingLocked(`\\server\share`, "user", "pass", func(string) (bool, error) {
		pathValidCalled = true
		return true, nil
	})
	if err != nil {
		t.Fatalf("ensureSMBGlobalMapping returned error: %v", err)
	}
	if pathValidCalled {
		t.Fatal("pathValidFn should not be called for non-OK SMB mapping states")
	}
	wantCalls := []string{`status:\\server\share`, `new:\\server\share`}
	if !reflect.DeepEqual(api.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", api.calls, wantCalls)
	}
}
