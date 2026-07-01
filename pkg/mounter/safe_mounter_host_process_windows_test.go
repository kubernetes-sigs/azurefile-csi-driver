//go:build windows
// +build windows

/*
Copyright 2024 The Kubernetes Authors.

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
	"fmt"
	"testing"
)

// fakeSMBAPI is a programmable smb.SMBAPI used to exercise the host-process
// mounter self-heal logic without touching real PowerShell/SMB.
type fakeSMBAPI struct {
	mapped        bool
	isMappedErr   error
	newCalled     int
	newErr        error
	removeCalled  int
	removeErr     error
	lastNewRemote string
	lastNewUser   string
	lastNewPass   string
}

func (f *fakeSMBAPI) IsSmbMapped(_ string) (bool, error) {
	return f.mapped, f.isMappedErr
}

func (f *fakeSMBAPI) NewSmbGlobalMapping(remotePath, username, password string) error {
	f.newCalled++
	f.lastNewRemote = remotePath
	f.lastNewUser = username
	f.lastNewPass = password
	return f.newErr
}

func (f *fakeSMBAPI) RemoveSmbGlobalMapping(_ string) error {
	f.removeCalled++
	return f.removeErr
}

func okCreds() func() (string, string, error) {
	return func() (string, string, error) { return "AZURE\\acc", "key==", nil }
}

func TestRevalidateSMBMount_NotMappedRemaps(t *testing.T) {
	api := &fakeSMBAPI{mapped: false}
	m := &winMounter{smbAPI: api}

	// remotePath with forward slashes should be normalized to backslashes.
	err := m.RevalidateSMBMount("//acc.file.core.windows.net/share", okCreds())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if api.newCalled != 1 {
		t.Fatalf("expected NewSmbGlobalMapping to be called once, got %d", api.newCalled)
	}
	if api.removeCalled != 0 {
		t.Fatalf("expected RemoveSmbGlobalMapping not to be called, got %d", api.removeCalled)
	}
	if api.lastNewRemote != `\\acc.file.core.windows.net\share` {
		t.Fatalf("remotePath not normalized, got %q", api.lastNewRemote)
	}
	if api.lastNewUser != "AZURE\\acc" || api.lastNewPass != "key==" {
		t.Fatalf("unexpected creds: user=%q pass=%q", api.lastNewUser, api.lastNewPass)
	}
}

func TestRevalidateSMBMount_NewMappingError(t *testing.T) {
	api := &fakeSMBAPI{mapped: false, newErr: fmt.Errorf("boom")}
	m := &winMounter{smbAPI: api}
	if err := m.RevalidateSMBMount(`\\acc.file.core.windows.net\share`, okCreds()); err == nil {
		t.Fatalf("expected error from NewSmbGlobalMapping, got nil")
	}
}

func TestRevalidateSMBMount_CredentialError(t *testing.T) {
	api := &fakeSMBAPI{mapped: false}
	m := &winMounter{smbAPI: api}
	creds := func() (string, string, error) { return "", "", fmt.Errorf("no creds") }
	if err := m.RevalidateSMBMount(`\\acc.file.core.windows.net\share`, creds); err == nil {
		t.Fatalf("expected credential error to propagate, got nil")
	}
	if api.newCalled != 0 {
		t.Fatalf("expected no remap when credentials fail, got %d", api.newCalled)
	}
}

func TestRevalidateSMBMapping_EmptyPath(t *testing.T) {
	m := &winMounter{smbAPI: &fakeSMBAPI{}}
	if err := m.revalidateSMBMapping("", okCreds()); err == nil {
		t.Fatalf("expected error for empty remote path, got nil")
	}
}
