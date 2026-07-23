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
	"fmt"
	"strings"

	mount "k8s.io/mount-utils"
)

type fakeMounter struct {
	mount.FakeMounter
	unmountCount uint
}

// Mount overrides mount.FakeMounter.Mount.
func (f *fakeMounter) Mount(source string, target string, _ string, _ []string) error {
	if strings.Contains(source, "error_mount") {
		return fmt.Errorf("fake Mount: source error")
	} else if strings.Contains(target, "error_mount") {
		return fmt.Errorf("fake Mount: target error")
	}

	return nil
}

// MountSensitive overrides mount.FakeMounter.MountSensitive.
func (f *fakeMounter) MountSensitive(source string, target string, _ string, _ []string, _ []string) error {
	if strings.Contains(source, "error_mount_sens") {
		return fmt.Errorf("fake MountSensitive: source error")
	} else if strings.Contains(target, "error_mount_sens") {
		return fmt.Errorf("fake MountSensitive: target error")
	}

	return nil
}

// IsLikelyNotMountPoint overrides mount.FakeMounter.IsLikelyNotMountPoint.
func (f *fakeMounter) IsLikelyNotMountPoint(file string) (bool, error) {
	if strings.Contains(file, "error_is_likely") {
		return false, fmt.Errorf("fake IsLikelyNotMountPoint: fake error")
	}
	if strings.Contains(file, "false_is_likely") {
		return false, nil
	}
	return true, nil
}

// IsMountPoint overrides mount.FakeMounter.IsMountPoint.
func (f *fakeMounter) IsMountPoint(file string) (bool, error) {
	notMnt, err := f.IsLikelyNotMountPoint(file)
	if err != nil {
		return false, err
	}
	return !notMnt, nil
}

// NewFakeMounter fake mounter. The platform-specific implementation lives in
// fake_mounter_windows.go (which wraps the real Windows mounter so the
// "false_is_likely" / "error_is_likely" path markers used by the shared unit
// tests behave the same as on Linux) and fake_mounter_others.go.
func NewFakeMounter() (*mount.SafeFormatAndMount, error) {
	return newFakeMounter()
}

// Unmount overrides mount.FakeMounter.Unmount.
func (f *fakeMounter) Unmount(target string) error {
	f.unmountCount++
	if strings.Contains(target, "error") {
		return fmt.Errorf("fake Unmount: target error")
	}
	return f.FakeMounter.Unmount(target)
}
