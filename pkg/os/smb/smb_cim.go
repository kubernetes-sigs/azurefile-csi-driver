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
	"strings"

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

var _ SMBAPI = &cimSMBAPI{}

type cimSMBAPI struct{}

func NewCimSMBAPI() *cimSMBAPI {
	return &cimSMBAPI{}
}

func (*cimSMBAPI) IsSmbMapped(remotePath string) (bool, error) {
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

func (*cimSMBAPI) NewSmbGlobalMapping(remotePath, username, password string) error {
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

func (*cimSMBAPI) RemoveSmbGlobalMapping(remotePath string) error {
	err := cim.RemoveSmbGlobalMappingByRemotePath(remotePathForQuery(remotePath))
	if err != nil {
		return fmt.Errorf("error remove smb mapping '%s'. err: %v", remotePath, err)
	}
	return nil
}
