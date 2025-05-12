//go:build windows
// +build windows

package cim

import (
	"github.com/microsoft/wmi/pkg/base/query"
	cim "github.com/microsoft/wmi/pkg/wmiinstance"
)

// Refer to https://learn.microsoft.com/en-us/previous-versions/windows/desktop/smb/msft-smbmapping
const (
	SmbMappingStatusOK int32 = iota
	SmbMappingStatusPaused
	SmbMappingStatusDisconnected
	SmbMappingStatusNetworkError
	SmbMappingStatusConnecting
	SmbMappingStatusReconnecting
	SmbMappingStatusUnavailable
)

// QuerySmbGlobalMappingByRemotePath retrieves the SMB global mapping from its remote path.
//
// The equivalent WMI query is:
//
//	SELECT [selectors] FROM MSFT_SmbGlobalMapping
//
// Refer to https://pkg.go.dev/github.com/microsoft/wmi/server2019/root/microsoft/windows/smb#MSFT_SmbGlobalMapping
// for the WMI class definition.
func QuerySmbGlobalMappingByRemotePath(remotePath string) (*cim.WmiInstance, error) {
	smbQuery := query.NewWmiQuery("MSFT_SmbGlobalMapping", "RemotePath", remotePath)
	instances, err := QueryInstances(WMINamespaceSmb, smbQuery)
	if err != nil {
		return nil, err
	}

	return instances[0], err
}

// RemoveSmbGlobalMappingByRemotePath removes a SMB global mapping matching to the remote path.
//
// Refer to https://pkg.go.dev/github.com/microsoft/wmi/server2019/root/microsoft/windows/smb#MSFT_SmbGlobalMapping
// for the WMI class definition.
func RemoveSmbGlobalMappingByRemotePath(remotePath string) error {
	smbQuery := query.NewWmiQuery("MSFT_SmbGlobalMapping", "RemotePath", remotePath)
	instances, err := QueryInstances(WMINamespaceSmb, smbQuery)
	if err != nil {
		return err
	}

	_, err = instances[0].InvokeMethod("Remove", true)
	return err
}
