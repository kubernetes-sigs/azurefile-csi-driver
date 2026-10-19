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

	"k8s.io/klog/v2"
	wmi "sigs.k8s.io/azurefile-csi-driver/pkg/os/wmi"
)

var _ SMBAPI = &cimSMBAPI{}

type cimSMBAPI struct{}

func NewCimSMBAPI() *cimSMBAPI {
	return &cimSMBAPI{}
}

func getSMBGlobalMappingStatusFromWMI(status uint32) SMBGlobalMappingStatus {
	switch status {
	case wmi.SmbMappingStatusOK:
		return SMBGlobalMappingStatusOK
	case wmi.SmbMappingStatusDisconnected:
		return SMBGlobalMappingStatusDisconnected
	default:
		return SMBGlobalMappingStatusOther
	}
}

func (*cimSMBAPI) GetSmbGlobalMappingStatus(remotePath string) (SMBGlobalMappingStatus, error) {
	mappingStatus := SMBGlobalMappingStatusNotFound
	err := wmi.WithCOMThread(func() error {
		return wmi.WithScope(func(scope *wmi.Scope) error {
			inst, err := wmi.QuerySmbGlobalMappingByRemotePath(scope, remotePath)
			if err != nil {
				if wmi.IgnoreNotFound(err) == nil {
					mappingStatus = SMBGlobalMappingStatusNotFound
					return nil
				}
				klog.V(6).Infof("error querying smb mapping for remote path %s. err: %v", remotePath, err)
				return err
			}

			status, err := wmi.GetSmbGlobalMappingStatus(inst)
			if err != nil {
				klog.V(6).Infof("error getting smb mapping status for remote path %s. err: %v", remotePath, err)
				return err
			}

			mappingStatus = getSMBGlobalMappingStatusFromWMI(status)
			return nil
		})
	})
	if err != nil {
		return SMBGlobalMappingStatusNotFound, err
	}
	return mappingStatus, nil
}

func (*cimSMBAPI) NewSmbGlobalMapping(remotePath, username, password string) error {
	requirePrivacy := true
	return wmi.WithCOMThread(func() error {
		err := wmi.NewSmbGlobalMapping(remotePath, username, password, requirePrivacy)
		if err != nil {
			klog.V(6).Infof("error creating smb mapping for remote path %s. err: %v", remotePath, err)
			return fmt.Errorf("create SMB mapping failed for %s: %w", remotePath, err)
		}
		return nil
	})
}

func (*cimSMBAPI) RemoveSmbGlobalMapping(remotePath string) error {
	return wmi.WithCOMThread(func() error {
		return wmi.WithScope(func(scope *wmi.Scope) error {
			err := wmi.RemoveSmbGlobalMappingByRemotePath(scope, remotePath)
			if err != nil {
				klog.V(6).Infof("error removing smb mapping for remote path %s. err: %v", remotePath, err)
				return fmt.Errorf("error remove smb mapping '%s'. err: %w", remotePath, err)
			}
			return nil
		})
	})
}
