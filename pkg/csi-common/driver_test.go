/*
Copyright 2017 The Kubernetes Authors.

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

package csicommon

import (
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	fakeDriverName = "fake"
	fakeNodeID     = "fakeNodeID"
)

var (
	vendorVersion = "0.3.0"
)

func NewFakeDriver() *CSIDriver {
	driver := NewCSIDriver(fakeDriverName, vendorVersion, fakeNodeID)

	return driver
}

func TestNewFakeDriver(t *testing.T) {
	// Test New fake driver with invalid arguments.
	// Invalid driver name
	d := NewCSIDriver("", vendorVersion, fakeNodeID)
	assert.Nil(t, d)

	// Invalid nodeID
	d = NewCSIDriver(fakeDriverName, vendorVersion, "")
	assert.Nil(t, d)

	// Invalid Version
	d = NewCSIDriver(fakeDriverName, "", fakeNodeID)
	assert.NotNil(t, d)
}

func TestGetVolumeCapabilityAccessModes(t *testing.T) {
	d := NewFakeDriver()

	// Test no volume access modes.
	// REVISIT: Do we need to support any default access modes.
	c := d.GetVolumeCapabilityAccessModes()
	assert.Zero(t, len(c))

	// Test driver with access modes.
	d.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER})
	modes := d.GetVolumeCapabilityAccessModes()
	assert.Equal(t, 1, len(modes))
	assert.Equal(t, modes[0].GetMode(), csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER)
}

func TestValidateControllerServiceRequest(t *testing.T) {
	d := NewFakeDriver()

	// Valid requests which require no capabilities
	err := d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_UNKNOWN)
	assert.NoError(t, err)

	// Test controller service publish/unpublish not supported
	err = d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME)
	s, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, s.Code(), codes.InvalidArgument)

	// Add controller service publish & unpublish request
	d.AddControllerServiceCapabilities(
		[]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
			csi.ControllerServiceCapability_RPC_GET_CAPACITY,
			csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
			csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		})

	// Test controller service publish/unpublish is supported
	err = d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME)
	assert.NoError(t, err)

	// Test controller service create/delete is supported
	err = d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME)
	assert.NoError(t, err)

	// Test controller service list volumes is supported
	err = d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_VOLUMES)
	assert.NoError(t, err)

	// Test controller service get capacity is supported
	err = d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_GET_CAPACITY)
	assert.NoError(t, err)

	// Test controller service clone volumes is supported
	err = d.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CLONE_VOLUME)
	assert.NoError(t, err)
}

func TestValidateNodeServiceRequest(t *testing.T) {
	d := NewFakeDriver()

	// Valid requests which require no capabilities
	err := d.ValidateNodeServiceRequest(csi.NodeServiceCapability_RPC_UNKNOWN)
	assert.NoError(t, err)

	// Test node with invalid capability validation
	err = d.ValidateNodeServiceRequest(csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME)
	s, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, s.Code(), codes.InvalidArgument)

	// Add node service capabilities
	d.AddNodeServiceCapabilities(
		[]csi.NodeServiceCapability_RPC_Type{
			csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
			csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
			csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
		})

	// Test node service stage unstaged volume is supported
	err = d.ValidateNodeServiceRequest(csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME)
	assert.NoError(t, err)

	// Test node service get volume stats is supported
	err = d.ValidateNodeServiceRequest(csi.NodeServiceCapability_RPC_GET_VOLUME_STATS)
	assert.NoError(t, err)

	// Test node expand volume is supported
	err = d.ValidateNodeServiceRequest(csi.NodeServiceCapability_RPC_EXPAND_VOLUME)
	assert.NoError(t, err)
}
