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

import "testing"

func TestParseCIFSTarget(t *testing.T) {
	tests := []struct {
		name   string
		device string
		server string
		share  string
		ok     bool
	}{
		{
			name:   "UNC path",
			device: `\\account.file.core.windows.net\share`,
			server: "account.file.core.windows.net",
			share:  "share",
			ok:     true,
		},
		{
			name:   "subdirectory",
			device: "//account.file.core.windows.net/share/folder",
			server: "account.file.core.windows.net",
			share:  "share",
			ok:     true,
		},
		{name: "IP endpoint", device: `\\10.10.24.244\share`, server: "10.10.24.244", share: "share", ok: true},
		{name: "different IP endpoint", device: `\\10.20.30.40\share`, server: "10.20.30.40", share: "share", ok: true},
		{name: "custom hostname", device: `\\Files.Example.com\Share`, server: "files.example.com", share: "share", ok: true},
		{name: "secondary endpoint", device: `\\account-secondary.file.core.windows.net\share`, server: "account-secondary.file.core.windows.net", share: "share", ok: true},
		{name: "missing share", device: "//account.file.core.windows.net", ok: false},
		{name: "empty", device: "", ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, share, ok := ParseCIFSTarget(test.device)
			if server != test.server || share != test.share || ok != test.ok {
				t.Errorf("ParseCIFSTarget(%q) = %q, %q, %t; want %q, %q, %t",
					test.device, server, share, ok, test.server, test.share, test.ok)
			}
		})
	}
}

func TestStorageAccountFromServer(t *testing.T) {
	tests := []struct {
		server  string
		account string
	}{
		{server: "account.file.core.windows.net", account: "account"},
		{server: "account.file.core.windows.net.", account: "account"},
		{server: "account-secondary.file.core.windows.net", account: "account"},
		{server: "account.privatelink.file.core.windows.net", account: "account"},
		{server: "account.file.core.chinacloudapi.cn", account: "account"},
		{server: "account.file.core.usgovcloudapi.net", account: "account"},
		{server: "account.file.core.cloudapi.de", account: "account"},
		{server: "account.z32.file.storage.azure.net", account: "account"},
		{server: "account.z32.privatelink.file.storage.azure.net", account: "account"},
		{server: "account.z32.afs.storage.azure.net", account: "account"},
		{server: "10.10.24.244"},
		{server: "10.20.30.40"},
		{server: "files.example.com"},
		{server: "account.file.core.windows.net.example.com"},
	}
	for _, test := range tests {
		t.Run(test.server, func(t *testing.T) {
			if account := storageAccountFromServer(test.server); account != test.account {
				t.Errorf("storageAccountFromServer(%q) = %q, want %q", test.server, account, test.account)
			}
		})
	}
}

func TestParseNFSTarget(t *testing.T) {
	tests := []struct {
		name    string
		device  string
		account string
		share   string
		ok      bool
	}{
		{
			name:    "Azure Files export",
			device:  "account.file.core.windows.net:/account/share",
			account: "account",
			share:   "share",
			ok:      true,
		},
		{
			name:    "subdirectory",
			device:  "10.0.0.4:/account/share/folder/nested",
			account: "account",
			share:   "share",
			ok:      true,
		},
		{name: "missing share", device: "server:/account", ok: false},
		{name: "missing server", device: ":/account/share", ok: false},
		{name: "empty", device: "", ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account, share, ok := ParseNFSTarget(test.device)
			if account != test.account || share != test.share || ok != test.ok {
				t.Errorf("ParseNFSTarget(%q) = %q, %q, %t; want %q, %q, %t",
					test.device, account, share, ok, test.account, test.share, test.ok)
			}
		})
	}
}
