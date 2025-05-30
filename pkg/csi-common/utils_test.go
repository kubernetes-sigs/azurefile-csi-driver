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
	"os"
	"runtime"
	"testing"

	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
)

func TestParseEndpoint(t *testing.T) {
	//Valid unix domain socket endpoint
	sockType, addr, err := ParseEndpoint("unix://fake.sock")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "unix")
	assert.Equal(t, addr, "fake.sock")

	sockType, addr, err = ParseEndpoint("unix:///fakedir/fakedir/fake.sock")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "unix")
	assert.Equal(t, addr, "/fakedir/fakedir/fake.sock")

	//Valid unix domain socket with uppercase
	sockType, addr, err = ParseEndpoint("UNIX://fake.sock")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "UNIX")
	assert.Equal(t, addr, "fake.sock")

	//Valid TCP endpoint with ip
	sockType, addr, err = ParseEndpoint("tcp://127.0.0.1:80")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "tcp")
	assert.Equal(t, addr, "127.0.0.1:80")

	//Valid TCP endpoint with uppercase
	sockType, addr, err = ParseEndpoint("TCP://127.0.0.1:80")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "TCP")
	assert.Equal(t, addr, "127.0.0.1:80")

	//Valid TCP endpoint with hostname
	sockType, addr, err = ParseEndpoint("tcp://fakehost:80")
	assert.NoError(t, err)
	assert.Equal(t, sockType, "tcp")
	assert.Equal(t, addr, "fakehost:80")

	_, _, err = ParseEndpoint("unix:/fake.sock/")
	assert.NotNil(t, err)

	_, _, err = ParseEndpoint("fake.sock")
	assert.NotNil(t, err)

	_, _, err = ParseEndpoint("unix://")
	assert.NotNil(t, err)

	_, _, err = ParseEndpoint("://")
	assert.NotNil(t, err)

	_, _, err = ParseEndpoint("")
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

	handler := func(_ context.Context, _ interface{}) (interface{}, error) { return nil, nil }
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
		c csi.ControllerServiceCapability_RPC_Type
	}{
		{
			c: csi.ControllerServiceCapability_RPC_UNKNOWN,
		},
		{
			c: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		},
		{
			c: csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		},
		{
			c: csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		},
		{
			c: csi.ControllerServiceCapability_RPC_GET_CAPACITY,
		},
		{
			c: csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		},
	}
	for _, test := range tests {
		resp := NewControllerServiceCapability(test.c)
		assert.NotNil(t, resp)
	}
}

func TestNewNodeServiceCapability(t *testing.T) {
	tests := []struct {
		c csi.NodeServiceCapability_RPC_Type
	}{
		{
			c: csi.NodeServiceCapability_RPC_UNKNOWN,
		},
		{
			c: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		},
		{
			c: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		},
		{
			c: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
		},
	}
	for _, test := range tests {
		resp := NewNodeServiceCapability(test.c)
		assert.NotNil(t, resp)
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

func TestListenEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skip test on Windows")
	}

	tests := []struct {
		name     string
		endpoint string
		filePath string
		wantErr  bool
	}{
		{
			name:     "unix socket",
			endpoint: "unix:///tmp/csi.sock",
			filePath: "/tmp/csi.sock",
			wantErr:  false,
		},
		{
			name:     "tcp socket",
			endpoint: "tcp://127.0.0.1:0",
			wantErr:  false,
		},
		{
			name:     "invalid endpoint",
			endpoint: "invalid://",
			wantErr:  true,
		},
		{
			name:     "invalid unix socket",
			endpoint: "unix://does/not/exist",
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ListenEndpoint(context.Background(), tt.endpoint)
			if (err != nil) != tt.wantErr {
				t.Errorf("Listen() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				got.Close()
				if tt.filePath != "" {
					os.Remove(tt.filePath)
				}
			}
		})
	}
}
