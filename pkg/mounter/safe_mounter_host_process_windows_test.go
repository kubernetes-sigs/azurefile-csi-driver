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

import "testing"

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
