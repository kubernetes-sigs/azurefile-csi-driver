//go:build windows
// +build windows

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
	"sigs.k8s.io/azurefile-csi-driver/pkg/mounter"
)

// fakeMounterWrapper wraps the real Windows csi-proxy / host-process mounter
// and intercepts IsLikelyNotMountPoint/IsMountPoint for the well-known test
// path markers used across the unit tests ("error_is_likely" and
// "false_is_likely"), matching the semantics of fakeMounter on Linux. It
// embeds mounter.CSIProxyMounter so the type assertions in
// azure_common_windows.go continue to succeed and every other method passes
// through to the wrapped implementation.
type fakeMounterWrapper struct {
	mounter.CSIProxyMounter
}

// IsLikelyNotMountPoint mirrors fakeMounter.IsLikelyNotMountPoint on Linux so
// tests that rely on these path markers behave the same on Windows.
func (w *fakeMounterWrapper) IsLikelyNotMountPoint(file string) (bool, error) {
	if strings.Contains(file, "error_is_likely") {
		return false, fmt.Errorf("fake IsLikelyNotMountPoint: fake error")
	}
	if strings.Contains(file, "false_is_likely") {
		return false, nil
	}
	return w.CSIProxyMounter.IsLikelyNotMountPoint(file)
}

// IsMountPoint mirrors fakeMounter.IsMountPoint so it stays consistent with
// the overridden IsLikelyNotMountPoint above (the real Windows mounter's
// IsMountPoint calls its own IsLikelyNotMountPoint, bypassing this wrapper).
func (w *fakeMounterWrapper) IsMountPoint(file string) (bool, error) {
	notMnt, err := w.IsLikelyNotMountPoint(file)
	if err != nil {
		return false, err
	}
	return !notMnt, nil
}

func newFakeMounter() (*mount.SafeFormatAndMount, error) {
	m, err := mounter.NewSafeMounter(true, true)
	if err != nil {
		return nil, err
	}
	proxy, ok := m.Interface.(mounter.CSIProxyMounter)
	if !ok {
		return nil, fmt.Errorf("could not cast to csi proxy class")
	}
	// Wrap the real Windows mounter so tests can use the "false_is_likely"
	// / "error_is_likely" path markers the same way they do on Linux.
	m.Interface = &fakeMounterWrapper{CSIProxyMounter: proxy}
	return m, nil
}
