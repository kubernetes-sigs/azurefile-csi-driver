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
	"bytes"
	"context"
	"flag"
	"testing"

	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
)

func TestParseEndpoint(t *testing.T) {
	//Valid unix domain socket endpoint
	sockType, addr, err := parseEndpoint("unix://fake.sock")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "unix")
	assert.Equal(t, addr, "fake.sock")

	sockType, addr, err = parseEndpoint("unix:///fakedir/fakedir/fake.sock")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "unix")
	assert.Equal(t, addr, "/fakedir/fakedir/fake.sock")

	//Valid unix domain socket with uppercase
	sockType, addr, err = parseEndpoint("UNIX://fake.sock")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "UNIX")
	assert.Equal(t, addr, "fake.sock")

	//Valid TCP endpoint with ip
	sockType, addr, err = parseEndpoint("tcp://127.0.0.1:80")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "tcp")
	assert.Equal(t, addr, "127.0.0.1:80")

	//Valid TCP endpoint with uppercase
	sockType, addr, err = parseEndpoint("TCP://127.0.0.1:80")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "TCP")
	assert.Equal(t, addr, "127.0.0.1:80")

	//Valid TCP endpoint with hostname
	sockType, addr, err = parseEndpoint("tcp://fakehost:80")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "tcp")
	assert.Equal(t, addr, "fakehost:80")

	_, _, err = parseEndpoint("unix:/fake.sock/")
	assert.NotNil(t, err)

	_, _, err = parseEndpoint("fake.sock")
	assert.NotNil(t, err)

	_, _, err = parseEndpoint("unix://")
	assert.NotNil(t, err)

	_, _, err = parseEndpoint("://")
	assert.NotNil(t, err)

	_, _, err = parseEndpoint("")
	assert.NotNil(t, err)
}

func TestLogGRPC(t *testing.T) {
	// SET UP
	klog.InitFlags(nil)
	if e := flag.Set("logtostderr", "false"); e != nil {
		t.Error(e)
	}
	if e := flag.Set("alsologtostderr", "false"); e != nil {
		t.Error(e)
	}
	if e := flag.Set("v", "100"); e != nil {
		t.Error(e)
	}
	flag.Parse()

	buf := new(bytes.Buffer)
	klog.SetOutput(buf)

	handler := func(ctx context.Context, req interface{}) (interface{}, error) { return nil, nil }
	info := grpc.UnaryServerInfo{
		FullMethod: "fake",
	}

	tests := []struct {
		name   string
		req    interface{}
		expStr string
	}{
		{
			"with secrets",
			&csi.NodeStageVolumeRequest{
				VolumeId: "vol_1",
				Secrets: map[string]string{
					"account_name": "k8s",
					"account_key":  "testkey",
				},
				XXX_sizecache: 100,
			},
			`GRPC request: {"secrets":"***stripped***","volume_id":"vol_1"}`,
		},
		{
			"without secrets",
			&csi.ListSnapshotsRequest{
				StartingToken: "testtoken",
			},
			`GRPC request: {"starting_token":"testtoken"}`,
		},
		{
			"NodeStageVolumeRequest with service account token",
			&csi.NodeStageVolumeRequest{
				VolumeContext: map[string]string{
					"csi.storage.k8s.io/serviceAccount.tokens": "testtoken",
					"csi.storage.k8s.io/testfield":             "testvalue",
				},
				XXX_sizecache: 100,
			},
			`GRPC request: {"volume_context":{"csi.storage.k8s.io/serviceAccount.tokens":"***stripped***","csi.storage.k8s.io/testfield":"testvalue"}}`,
		},
		{
			"NodePublishVolumeRequest with service account token",
			&csi.NodePublishVolumeRequest{
				VolumeContext: map[string]string{
					"csi.storage.k8s.io/serviceAccount.tokens": "testtoken",
					"csi.storage.k8s.io/testfield":             "testvalue",
				},
				XXX_sizecache: 100,
			},
			`GRPC request: {"volume_context":{"csi.storage.k8s.io/serviceAccount.tokens":"***stripped***","csi.storage.k8s.io/testfield":"testvalue"}}`,
		},
		{
			"with secrets and service account token",
			&csi.NodeStageVolumeRequest{
				VolumeId: "vol_1",
				Secrets: map[string]string{
					"account_name": "k8s",
					"account_key":  "testkey",
				},
				VolumeContext: map[string]string{
					"csi.storage.k8s.io/serviceAccount.tokens": "testtoken",
					"csi.storage.k8s.io/testfield":             "testvalue",
				},
				XXX_sizecache: 100,
			},
			`GRPC request: {"secrets":"***stripped***","volume_context":{"csi.storage.k8s.io/serviceAccount.tokens":"***stripped***","csi.storage.k8s.io/testfield":"testvalue"},"volume_id":"vol_1"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// EXECUTE
			_, _ = LogGRPC(context.Background(), test.req, &info, handler)
			klog.Flush()

			// ASSERT
			assert.Contains(t, buf.String(), "GRPC call: fake")
			assert.Contains(t, buf.String(), test.expStr)
			assert.Contains(t, buf.String(), "GRPC response: null")

			// CLEANUP
			buf.Reset()
		})
	}
}

func TestNewVolumeCapabilityAccessMode(t *testing.T) {
	tests := []struct {
		mode csi.VolumeCapability_AccessMode_Mode
	}{
		{
			mode: csi.VolumeCapability_AccessMode_UNKNOWN,
		},
		{
			mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
		{
			mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		},
		{
			mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		},
		{
			mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		},
		{
			mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		},
		{
			mode: csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
		},
		{
			mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		},
	}
	for _, test := range tests {
		resp := NewVolumeCapabilityAccessMode(test.mode)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.Mode, test.mode)
	}
}

func TestNewControllerServiceCapability(t *testing.T) {
	tests := []struct {
		cap csi.ControllerServiceCapability_RPC_Type
	}{
		{
			cap: csi.ControllerServiceCapability_RPC_UNKNOWN,
		},
		{
			cap: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		},
		{
			cap: csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		},
		{
			cap: csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		},
		{
			cap: csi.ControllerServiceCapability_RPC_GET_CAPACITY,
		},
		{
			cap: csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		},
	}
	for _, test := range tests {
		resp := NewControllerServiceCapability(test.cap)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.XXX_sizecache, int32(0))
	}
}

func TestNewNodeServiceCapability(t *testing.T) {
	tests := []struct {
		cap csi.NodeServiceCapability_RPC_Type
	}{
		{
			cap: csi.NodeServiceCapability_RPC_UNKNOWN,
		},
		{
			cap: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		},
		{
			cap: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		},
		{
			cap: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
		},
	}
	for _, test := range tests {
		resp := NewNodeServiceCapability(test.cap)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.XXX_sizecache, int32(0))
	}
}

func TestGetLogLevel(t *testing.T) {
	tests := []struct {
		method string
		level  int32
	}{
		{
			method: "/csi.v1.Identity/Probe",
			level:  6,
		},
		{
			method: "/csi.v1.Node/NodeGetCapabilities",
			level:  6,
		},
		{
			method: "/csi.v1.Node/NodeGetVolumeStats",
			level:  6,
		},
		{
			method: "",
			level:  2,
		},
		{
			method: "unknown",
			level:  2,
		},
	}

	for _, test := range tests {
		level := getLogLevel(test.method)
		if level != test.level {
			t.Errorf("returned level: (%v), expected level: (%v)", level, test.level)
		}
	}
}
