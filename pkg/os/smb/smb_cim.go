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
	"k8s.io/klog/v2"
	"sigs.k8s.io/azurefile-csi-driver/pkg/os/cim"
)

var _ SMBAPI = &cimSMBAPI{}

type cimSMBAPI struct{}

func NewCimSMBAPI() *cimSMBAPI {
	return &cimSMBAPI{}
}

func (*cimSMBAPI) IsSmbMapped(remotePath string) (bool, error) {
	var isMapped bool
	err := cim.WithCOMThread(func() error {
		inst, err := cim.QuerySmbGlobalMappingByRemotePath(remotePath)
		if err != nil {
			klog.V(6).Infof("error querying smb mapping for remote path %s. err: %v", remotePath, err)
			return err
		}

		status, err := cim.GetSmbGlobalMappingStatus(inst)
		if err != nil {
			klog.V(6).Infof("error getting smb mapping status for remote path %s. err: %v", remotePath, err)
			return err
		}

		isMapped = status == cim.SmbMappingStatusOK
		return nil
	})
	return isMapped, cim.IgnoreNotFound(err)
}

func (*cimSMBAPI) NewSmbGlobalMapping(remotePath, username, password string) error {
	return cim.WithCOMThread(func() error {
		result, err := cim.NewSmbGlobalMapping(remotePath, username, password, true)
		if err != nil {
			klog.V(6).Infof("error creating smb mapping for remote path %s. result %d, err: %v", remotePath, result, err)
			return err
		}
		return nil
	})
}

func (*cimSMBAPI) RemoveSmbGlobalMapping(remotePath string) error {
	return cim.WithCOMThread(func() error {
		err := cim.RemoveSmbGlobalMappingByRemotePath(remotePath)
		if err != nil {
			klog.V(6).Infof("error removing smb mapping for remote path %s. err: %v", remotePath, err)
			return err
		}
		return nil
	})
}
