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

	"sigs.k8s.io/azurefile-csi-driver/pkg/util"
)

func IsSmbMapped(remotePath string) (bool, error) {
	cmdLine := `$(Get-SmbGlobalMapping -RemotePath $Env:smbremotepath -ErrorAction Stop).Status `
	cmdEnv := fmt.Sprintf("smbremotepath=%s", remotePath)
	out, err := util.RunPowershellCmd(cmdLine, cmdEnv)
	if err != nil {
		return false, fmt.Errorf("error checking smb mapping. cmd %s, output: %s, err: %v", remotePath, string(out), err)
	}

	if len(out) == 0 || !strings.EqualFold(strings.TrimSpace(string(out)), "OK") {
		return false, nil
	}
	return true, nil
}

// NewSmbLink - creates a directory symbolic link to the remote share.
// The os.Symlink was having issue for cases where the destination was an SMB share - the container
// runtime would complain stating "Access Denied". Because of this, we had to perform
// this operation with powershell commandlet creating an directory softlink.
// Since os.Symlink is currently being used in working code paths, no attempt is made in
// alpha to merge the paths.
// TODO (for beta release): Merge the link paths - os.Symlink and Powershell link path.
func NewSmbLink(remotePath, localPath string) error {
	if !strings.HasSuffix(remotePath, "\\") {
		// Golang has issues resolving paths mapped to file shares if they do not end in a trailing \
		// so add one if needed.
		remotePath = remotePath + "\\"
	}

	cmdLine := `New-Item -ItemType SymbolicLink $Env:smblocalPath -Target $Env:smbremotepath`
	output, err := util.RunPowershellCmd(cmdLine, fmt.Sprintf("smbremotepath=%s", remotePath), fmt.Sprintf("smblocalpath=%s", localPath))
	if err != nil {
		return fmt.Errorf("error linking %s to %s. output: %s, err: %v", remotePath, localPath, string(output), err)
	}

	return nil
}

func NewSmbGlobalMapping(remotePath, username, password string) error {
	// use PowerShell Environment Variables to store user input string to prevent command line injection
	// https://docs.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_environment_variables?view=powershell-5.1
	cmdLine := fmt.Sprintf(`$PWord = ConvertTo-SecureString -String $Env:smbpassword -AsPlainText -Force` +
		`;$Credential = New-Object -TypeName System.Management.Automation.PSCredential -ArgumentList $Env:smbuser, $PWord` +
		`;New-SmbGlobalMapping -RemotePath $Env:smbremotepath -Credential $Credential -RequirePrivacy $true`)

	if output, err := util.RunPowershellCmd(cmdLine, fmt.Sprintf("smbuser=%s", username),
		fmt.Sprintf("smbpassword=%s", password),
		fmt.Sprintf("smbremotepath=%s", remotePath)); err != nil {
		return fmt.Errorf("NewSmbGlobalMapping failed. output: %q, err: %v", string(output), err)
	}
	return nil
}

func RemoveSmbGlobalMapping(remotePath string) error {
	cmd := `Remove-SmbGlobalMapping -RemotePath $Env:smbremotepath -Force`
	if output, err := util.RunPowershellCmd(cmd, fmt.Sprintf("smbremotepath=%s", remotePath)); err != nil {
		return fmt.Errorf("UnmountSmbShare failed. output: %q, err: %v", string(output), err)
	}
	return nil
}
