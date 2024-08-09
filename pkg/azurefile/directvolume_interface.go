package azurefile

import (
	volume "github.com/kata-containers/kata-containers/src/runtime/pkg/direct-volume"
)

type DirectVolume interface {
	Add(volumePath string, mountInfo string) error
	Remove(volumePath string) error
	VolumeMountInfo(volumePath string) (*volume.MountInfo, error)
	RecordSandboxId(sandboxId string, volumePath string) error
	GetSandboxIdForVolume(volumePath string) (string, error)
}

// Ensure the existing functions implement the interface
var _ DirectVolume = &directVolume{}

type directVolume struct{}

func (dv *directVolume) Add(volumePath string, mountInfo string) error {
	return volume.Add(volumePath, mountInfo)
}

func (dv *directVolume) Remove(volumePath string) error {
	return volume.Remove(volumePath)
}

func (dv *directVolume) VolumeMountInfo(volumePath string) (*volume.MountInfo, error) {
	return volume.VolumeMountInfo(volumePath)
}

func (dv *directVolume) RecordSandboxId(sandboxId string, volumePath string) error {
	return volume.RecordSandboxId(sandboxId, volumePath)
}

func (dv *directVolume) GetSandboxIdForVolume(volumePath string) (string, error) {
	return volume.GetSandboxIdForVolume(volumePath)
}
