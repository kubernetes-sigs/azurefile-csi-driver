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

	"k8s.io/klog/v2"
	"sigs.k8s.io/azurefile-csi-driver/pkg/util"
)

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
func PathExists(ctx context.Context, request *PathExistsRequest) (bool, error) {
	klog.V(2).Infof("Request: PathExists with path=%q", request.Path)
	err := ValidatePathWindows(request.Path)
	if err != nil {
		klog.Errorf("failed validatePathWindows %v", err)
		return false, err
	}
	exists, err := pathExists(request.Path)
	if err != nil {
		klog.Errorf("failed check PathExists %v", err)
	}
	return exists, err
}

func PathValid(ctx context.Context, path string) (bool, error) {
	cmd := `Test-Path $Env:remotepath`
	cmdEnv := fmt.Sprintf("remotepath=%s", path)
	output, err := util.RunPowershellCmd(cmd, cmdEnv)
	if err != nil {
		return false, fmt.Errorf("returned output: %s, error: %v", string(output), err)
	}

	return strings.HasPrefix(strings.ToLower(string(output)), "true"), nil
}

func ValidatePathWindows(path string) error {
	prefix := `C:\var\lib\kubelet`

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

	if !strings.HasPrefix(strings.ToLower(path), strings.ToLower(prefix)) {
		return fmt.Errorf("path: %s is not within context path: %s", path, prefix)
	}

	return nil
}

func Mkdir(ctx context.Context, request *MkdirRequest) error {
	klog.V(2).Infof("Request: Mkdir with path=%q", request.Path)
	err := ValidatePathWindows(request.Path)
	if err != nil {
		klog.Errorf("failed validatePathWindows %v", err)
		return err
	}
	err = os.MkdirAll(request.Path, 0755)
	if err != nil {
		klog.Errorf("failed Mkdir %v", err)
		return err
	}

	return err
}

func Rmdir(ctx context.Context, request *RmdirRequest) error {
	klog.V(2).Infof("Request: Rmdir with path=%q", request.Path)
	err := ValidatePathWindows(request.Path)
	if err != nil {
		klog.Errorf("failed validatePathWindows %v", err)
		return err
	}

	if request.Force {
		err = os.RemoveAll(request.Path)
	} else {
		err = os.Remove(request.Path)
	}
	if err != nil {
		klog.Errorf("failed Rmdir %v", err)
		return err
	}

	return err
}
func LinkPath(ctx context.Context, request *LinkPathRequest) error {
	klog.V(2).Infof("Request: LinkPath with targetPath=%q sourcePath=%q", request.TargetPath, request.SourcePath)
	createSymlinkRequest := &CreateSymlinkRequest{
		SourcePath: request.SourcePath,
		TargetPath: request.TargetPath,
	}
	if err := CreateSymlink(ctx, createSymlinkRequest); err != nil {
		klog.Errorf("Failed to forward to CreateSymlink: %v", err)
		return err
	}
	return nil
}

func CreateSymlink(ctx context.Context, request *CreateSymlinkRequest) error {
	klog.V(2).Infof("Request: CreateSymlink with targetPath=%q sourcePath=%q", request.TargetPath, request.SourcePath)
	err := ValidatePathWindows(request.TargetPath)
	if err != nil {
		klog.Errorf("failed validatePathWindows for target path %v", err)
		return err
	}
	err = ValidatePathWindows(request.SourcePath)
	if err != nil {
		klog.Errorf("failed validatePathWindows for source path %v", err)
		return err
	}
	err = os.Symlink(request.SourcePath, request.TargetPath)
	if err != nil {
		klog.Errorf("failed CreateSymlink: %v", err)
		return err
	}
	return nil
}

func IsMountPoint(ctx context.Context, request *IsMountPointRequest) (bool, error) {
	klog.V(2).Infof("Request: IsMountPoint with path=%q", request.Path)
	isSymlinkRequest := &IsSymlinkRequest{
		Path: request.Path,
	}
	isSymlink, err := IsSymlink(ctx, isSymlinkRequest)
	if err != nil {
		klog.Errorf("Failed to forward to IsSymlink: %v", err)
	}
	return isSymlink, err
}

func IsSymlink(ctx context.Context, request *IsSymlinkRequest) (bool, error) {
	klog.V(2).Infof("Request: IsSymlink with path=%q", request.Path)
	isSymlink, err := isSymlink(request.Path)
	if err != nil {
		klog.Errorf("failed IsSymlink %v", err)
	}
	return isSymlink, err
}

// IsSymlink - returns true if tgt is a mount point.
// A path is considered a mount point if:
// - directory exists and
// - it is a soft link and
// - the target path of the link exists.
// If tgt path does not exist, it returns an error
// if tgt path exists, but the source path tgt points to does not exist, it returns false without error.
func isSymlink(tgt string) (bool, error) {
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
