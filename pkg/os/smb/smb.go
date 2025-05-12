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

package smb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/klog/v2"
	"sigs.k8s.io/azurefile-csi-driver/pkg/os/cim"
)

const (
	credentialDelimiter = ":"
)

func remotePathForQuery(remotePath string) string {
	return strings.ReplaceAll(remotePath, "\\", "\\\\")
}

func escapeUserName(userName string) string {
	// refer to https://github.com/PowerShell/PowerShell/blob/9303de597da55963a6e26a8fe164d0b256ca3d4d/src/Microsoft.PowerShell.Commands.Management/cimSupport/cmdletization/cim/cimConverter.cs#L169-L170
	escaped := strings.ReplaceAll(userName, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, credentialDelimiter, "\\"+credentialDelimiter)
	return escaped
}

func IsSmbMapped(remotePath string) (bool, error) {
	inst, err := cim.QuerySmbGlobalMappingByRemotePath(remotePathForQuery(remotePath))
	if err != nil {
		return false, cim.IgnoreNotFound(err)
	}

	status, err := inst.GetProperty("Status")
	if err != nil {
		return false, err
	}

	return status.(int32) == cim.SmbMappingStatusOK, nil
}

func NewSmbGlobalMapping(remotePath, username, password string) error {
	params := map[string]interface{}{
		"RemotePath":     remotePath,
		"RequirePrivacy": true,
	}
	if username != "" {
		// refer to https://github.com/PowerShell/PowerShell/blob/9303de597da55963a6e26a8fe164d0b256ca3d4d/src/Microsoft.PowerShell.Commands.Management/cimSupport/cmdletization/cim/cimConverter.cs#L166-L178
		// on how SMB credential is handled in PowerShell
		params["Credential"] = escapeUserName(username) + credentialDelimiter + password
	}

	result, _, err := cim.InvokeCimMethod(cim.WMINamespaceSmb, "MSFT_SmbGlobalMapping", "Create", params)
	if err != nil {
		return fmt.Errorf("NewSmbGlobalMapping failed. result: %d, err: %v", result, err)
	}

	return nil
}

func RemoveSmbGlobalMapping(remotePath string) error {
	err := cim.RemoveSmbGlobalMappingByRemotePath(remotePathForQuery(remotePath))
	if err != nil {
		return fmt.Errorf("error remove smb mapping '%s'. err: %v", remotePath, err)
	}
	return nil
}

// GetRemoteServerFromTarget- gets the remote server path given a mount point, the function is recursive until it find the remote server or errors out
func GetRemoteServerFromTarget(mount string) (string, error) {
	target, err := os.Readlink(mount)
	klog.V(2).Infof("read link for mount %s, target: %s", mount, target)
	if err != nil || len(target) == 0 {
		return "", fmt.Errorf("error reading link for mount %s. target %s err: %v", mount, target, err)
	}
	return strings.TrimSpace(target), nil
}

// CheckForDuplicateSMBMounts checks if there is any other SMB mount exists on the same remote server
func CheckForDuplicateSMBMounts(dir, mount, remoteServer string) (bool, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	for _, file := range files {
		klog.V(6).Infof("checking file %s", file.Name())
		if file.IsDir() {
			globalMountPath := filepath.Join(dir, file.Name(), "globalmount")
			if strings.EqualFold(filepath.Clean(globalMountPath), filepath.Clean(mount)) {
				klog.V(2).Infof("skip current mount path %s", mount)
			} else {
				fileInfo, err := os.Lstat(globalMountPath)
				// check if the file is a symlink, if yes, check if it is pointing to the same remote server
				if err == nil && fileInfo.Mode()&os.ModeSymlink != 0 {
					remoteServerPath, err := GetRemoteServerFromTarget(globalMountPath)
					klog.V(2).Infof("checking remote server path %s on local path %s", remoteServerPath, globalMountPath)
					if err == nil {
						if remoteServerPath == remoteServer {
							return true, nil
						}
					} else {
						klog.Errorf("GetRemoteServerFromTarget(%s) failed with %v", globalMountPath, err)
					}
				}
			}
		}
	}
	return false, err
}
