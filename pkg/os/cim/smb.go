//go:build windows
// +build windows

package cim

import (
	"github.com/microsoft/wmi/pkg/base/query"
	cim "github.com/microsoft/wmi/pkg/wmiinstance"
)

func QuerySmbGlobalMappingByRemotePath(remotePath string) (*cim.WmiInstance, error) {
	smbQuery := query.NewWmiQuery("MSFT_SmbGlobalMapping", "RemotePath", remotePath)
	instances, err := QueryInstances(WMINamespaceSmb, smbQuery)
	if err != nil {
		return nil, err
	}

	return instances[0], err
}

func RemoveSmbGlobalMappingByRemotePath(remotePath string) error {
	smbQuery := query.NewWmiQuery("MSFT_SmbGlobalMapping", "RemotePath", remotePath)
	instances, err := QueryInstances(WMINamespaceSmb, smbQuery)
	if err != nil {
		return err
	}

	_, err = instances[0].InvokeMethod("Remove", true)
	return err
}
