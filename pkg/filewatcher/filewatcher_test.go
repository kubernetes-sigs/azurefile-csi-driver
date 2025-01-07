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

package filewatcher

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestWatchFileForChanges(t *testing.T) {
	// Set mock exit once.
	exit = func(_ int) {
		// Mock, do nothing
	}

	t.Run("ExistingFile", func(t *testing.T) {
		// Create a temporary file to watch
		tmpfile, err := os.CreateTemp("", "testfile")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpfile.Name())

		// Now call the watcher
		if err = WatchFileForChanges(tmpfile.Name()); err != nil {
			t.Errorf("Failed to watch file: %v", err)
		}

		// Trigger the watcher
		if err = os.WriteFile(tmpfile.Name(), []byte("new content"), 0644); err != nil {
			t.Fatal(err)
		}

		// Let the watcher see the change
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		// Reset the watcher once before the test
		resetWatchCertificateFileOnce()

		err := WatchFileForChanges("nonexistentfile")
		if err == nil || (!strings.Contains(err.Error(), "no such file or directory") &&
			!strings.Contains(err.Error(), "The system cannot find the file specified")) {
			t.Errorf("expected error to contain 'no such file or directory' or 'The system cannot find the file specified', got %v", err)
		}
	})
}
