//go:build windows
// +build windows

/*
Copyright 2023 The Kubernetes Authors.

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

package filesystem

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
	"golang.org/x/sys/windows"
	"k8s.io/klog/v2"
	"sigs.k8s.io/azurefile-csi-driver/pkg/util"
)

// pathValidDefaultTimeout bounds the GetFileAttributes probe in PathValid. A UNC path
// backed by a dead SMB session can make GetFileAttributes block; without a bound a
// single wedged connection could stall NodeStage/NodePublish indefinitely.
const pathValidDefaultTimeout = 30 * time.Second

// pathValidGroup deduplicates concurrent PathValid probes for the same path. When the
// underlying GetFileAttributes call against a wedged UNC path blocks past the timeout,
// the probing goroutine cannot be cancelled (the Windows syscall is not interruptible).
// singleflight ensures at most one such goroutine is in flight per path, so repeated
// kubelet retries against the same dead session reuse the in-flight probe instead of
// accumulating unbounded blocked goroutines.
var pathValidGroup singleflight.Group

var invalidPathCharsRegexWindows = regexp.MustCompile(`["/\:\?\*|]`)
var absPathRegexWindows = regexp.MustCompile(`^[a-zA-Z]:\\`)

func containsInvalidCharactersWindows(path string) bool {
	if isAbsWindows(path) {
		path = path[3:]
	}
	if invalidPathCharsRegexWindows.MatchString(path) {
		return true
	}
	if strings.Contains(path, `..`) {
		return true
	}
	return false
}

func isUNCPathWindows(path string) bool {
	// check for UNC/pipe prefixes like "\\"
	if len(path) < 2 {
		return false
	}
	if path[0] == '\\' && path[1] == '\\' {
		return true
	}
	return false
}

func isAbsWindows(path string) bool {
	// for Windows check for C:\\.. prefix only
	// UNC prefixes of the form \\ are not considered
	return absPathRegexWindows.MatchString(path)
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// PathExists checks if the given path exists on the host.
func PathExists(path string) (bool, error) {
	if err := ValidatePathWindows(path); err != nil {
		klog.Errorf("failed validatePathWindows %v", err)
		return false, err
	}
	return pathExists(path)
}

func PathValid(ctx context.Context, path string) (bool, error) {
	klog.V(6).Infof("PathValid called with path: %s", path)

	// Bound the probe: GetFileAttributes against a UNC path whose SMB session is dead
	// can block, so run it on a goroutine and honor the context deadline. If the caller
	// did not supply a deadline, apply a default one.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pathValidDefaultTimeout)
		defer cancel()
	}

	type result struct {
		valid bool
		err   error
	}
	// Deduplicate the actual probe per path via singleflight so concurrent callers
	// (and rapid kubelet retries) share a single in-flight GetFileAttributes goroutine.
	// This bounds the number of goroutines that can be left blocked on a wedged UNC path
	// to at most one per path. resultCh is buffered so the worker can always send and
	// exit once the (possibly delayed) syscall finally returns, even after we time out.
	resultCh := make(chan result, 1)
	go func() {
		v, _, _ := pathValidGroup.Do(path, func() (interface{}, error) {
			valid, err := getPathAttributesValid(path)
			return result{valid: valid, err: err}, nil
		})
		resultCh <- v.(result)
	}()

	select {
	case <-ctx.Done():
		klog.Warningf("PathValid(%s) timed out or was cancelled: %v", path, ctx.Err())
		return false, fmt.Errorf("PathValid(%s) timed out: %w", path, ctx.Err())
	case r := <-resultCh:
		return r.valid, r.err
	}
}

func getPathAttributesValid(path string) (bool, error) {
	pathString, err := windows.UTF16PtrFromString(path)
	if err != nil {
		klog.V(6).Infof("failed to convert path %s to UTF16: %v", path, err)
		return false, fmt.Errorf("invalid path: %w", err)
	}

	attrs, err := windows.GetFileAttributes(pathString)
	if err != nil {
		klog.V(6).Infof("failed to get file attributes for path %s: %v", path, err)
		if errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_INVALID_NAME) {
			klog.Warningf("path %s does not exist or is invalid, error: %v", path, err)
			return false, nil
		}

		// GetFileAttribute returns user or password incorrect for a disconnected SMB connection after the password is changed
		return false, fmt.Errorf("failed to get path %s attribute: %w", path, err)
	}

	klog.V(6).Infof("GetFileAttributes for path %s returned attributes: %d", path, attrs)
	return attrs != windows.INVALID_FILE_ATTRIBUTES, nil
}

func ValidatePathWindows(path string) error {
	pathlen := len(path)

	if pathlen > util.MaxPathLengthWindows {
		return fmt.Errorf("path length %d exceeds maximum characters: %d", pathlen, util.MaxPathLengthWindows)
	}

	if pathlen > 0 && (path[0] == '\\') {
		return fmt.Errorf("invalid character \\ at beginning of path: %s", path)
	}

	if isUNCPathWindows(path) {
		return fmt.Errorf("unsupported UNC path prefix: %s", path)
	}

	if containsInvalidCharactersWindows(path) {
		return fmt.Errorf("path contains invalid characters: %s", path)
	}

	if !isAbsWindows(path) {
		return fmt.Errorf("not an absolute Windows path: %s", path)
	}

	return nil
}

func Rmdir(path string, force bool) error {
	if err := ValidatePathWindows(path); err != nil {
		return err
	}

	if force {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func IsMountPoint(path string) (bool, error) {
	return IsSymlink(path)
}

// IsSymlink - returns true if tgt is a mount point.
// A path is considered a mount point if:
// - directory exists and
// - it is a soft link and
// - the target path of the link exists.
// If tgt path does not exist, it returns an error
// if tgt path exists, but the source path tgt points to does not exist, it returns false without error.
func IsSymlink(tgt string) (bool, error) {
	// This code is similar to k8s.io/kubernetes/pkg/util/mount except the pathExists usage.
	stat, err := os.Lstat(tgt)
	if err != nil {
		return false, err
	}

	// If its a link and it points to an existing file then its a mount point.
	if stat.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(tgt)
		if err != nil {
			return false, fmt.Errorf("readlink error: %v", err)
		}
		exists, err := pathExists(target)
		if err != nil {
			return false, err
		}
		return exists, nil
	}

	return false, nil
}
