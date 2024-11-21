/*
Copyright 2020 The Kubernetes Authors.

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

package azurefile

import (
	"context"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGetPluginInfo(t *testing.T) {
	// Check with correct arguments
	d := NewFakeDriver()
	req := csi.GetPluginInfoRequest{}
	resp, err := d.GetPluginInfo(context.Background(), &req)
	assert.NoError(t, err)
	assert.Equal(t, resp.Name, fakeDriverName)
	assert.Equal(t, resp.GetVendorVersion(), vendorVersion)

	//Check error when driver name is empty
	d = NewFakeDriver()
	d.Name = ""
	req = csi.GetPluginInfoRequest{}
	resp, err = d.GetPluginInfo(context.Background(), &req)
	assert.Error(t, err)
	assert.Nil(t, resp)

	//Check error when version is empty
	d = NewFakeDriver()
	d.Version = ""
	req = csi.GetPluginInfoRequest{}
	resp, err = d.GetPluginInfo(context.Background(), &req)
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestProbe(t *testing.T) {
	d := NewFakeDriver()
	req := csi.ProbeRequest{}
	resp, err := d.Probe(context.Background(), &req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, resp.Ready.Value, true)
}

func TestGetPluginCapabilities(t *testing.T) {
	d := NewFakeDriver()
	req := csi.GetPluginCapabilitiesRequest{}
	resp, err := d.GetPluginCapabilities(context.Background(), &req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}
