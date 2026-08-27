//go:build !windows
// +build !windows

/*
Copyright 2020 The Kubernetes Authors.

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

package azurefile

import (
	"os"
	"syscall"
	"testing"
)

// TestClassifyMountErrorTypedErrno covers the typed syscall.Errno fallback in
// classifyMountError. These cases rely on Unix errno values and
// mount.IsCorruptedMnt's Unix semantics, so they are constrained to non-Windows
// platforms; Windows uses a different numeric error list and errno text.
func TestClassifyMountErrorTypedErrno(t *testing.T) {
	tests := []struct {
		desc     string
		err      error
		expected string
	}{
		{
			desc:     "stale/corrupted mount (typed ESTALE)",
			err:      &os.PathError{Op: "stat", Path: "/mnt/x", Err: syscall.ESTALE},
			expected: mountErrorStale,
		},
		{
			desc:     "typed EACCES classified precisely, not stale",
			err:      &os.PathError{Op: "open", Path: "/mnt/x", Err: syscall.EACCES},
			expected: mountErrorAccessDenied,
		},
		{
			desc:     "typed EHOSTDOWN classified as network, not stale",
			err:      &os.PathError{Op: "stat", Path: "/mnt/x", Err: syscall.EHOSTDOWN},
			expected: mountErrorNetwork,
		},
	}

	for _, test := range tests {
		if result := classifyMountError(test.err); result != test.expected {
			t.Errorf("desc: (%s), input: err(%v), classifyMountError returned (%q), expected (%q)",
				test.desc, test.err, result, test.expected)
		}
	}
}
