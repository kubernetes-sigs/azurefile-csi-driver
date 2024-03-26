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
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
)

func parseEndpoint(ep string) (string, string, error) {
	if strings.HasPrefix(strings.ToLower(ep), "unix://") || strings.HasPrefix(strings.ToLower(ep), "tcp://") {
		s := strings.SplitN(ep, "://", 2)
		if s[1] != "" {
			return s[0], s[1], nil
		}
	}
	return "", "", fmt.Errorf("Invalid endpoint: %v", ep)
}
func ListenEndpoint(endpoint string) (net.Listener, error) {
	proto, addr, err := parseEndpoint(endpoint)
	if err != nil {
		klog.Fatal(err.Error())
	}

	if proto == "unix" {
		if runtime.GOOS != "windows" {
			addr = "/" + addr
		}
		if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
			klog.Fatalf("Failed to remove %s, error: %s", addr, err.Error())
		}
	}

	listener, err := net.Listen(proto, addr)
	if err != nil {
		klog.Fatalf("Failed to listen: %v", err)
	}
	return listener, err
}

func NewVolumeCapabilityAccessMode(mode csi.VolumeCapability_AccessMode_Mode) *csi.VolumeCapability_AccessMode {
	return &csi.VolumeCapability_AccessMode{Mode: mode}
}

func NewControllerServiceCapability(cap csi.ControllerServiceCapability_RPC_Type) *csi.ControllerServiceCapability {
	return &csi.ControllerServiceCapability{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: cap,
			},
		},
	}
}

func NewNodeServiceCapability(cap csi.NodeServiceCapability_RPC_Type) *csi.NodeServiceCapability {
	return &csi.NodeServiceCapability{
		Type: &csi.NodeServiceCapability_Rpc{
			Rpc: &csi.NodeServiceCapability_RPC{
				Type: cap,
			},
		},
	}
}

func getLogLevel(method string) int32 {
	if method == "/csi.v1.Identity/Probe" ||
		method == "/csi.v1.Node/NodeGetCapabilities" ||
		method == "/csi.v1.Node/NodeGetVolumeStats" {
		return 6
	}
	return 2
}

func LogGRPC(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	level := klog.Level(getLogLevel(info.FullMethod))
	klog.V(level).Infof("GRPC call: %s", info.FullMethod)
	klog.V(level).Infof("GRPC request: %s", StripSensitiveValue(protosanitizer.StripSecrets(req), "csi.storage.k8s.io/serviceAccount.tokens"))

	resp, err := handler(ctx, req)
	if err != nil {
		klog.Errorf("GRPC error: %v", err)
	} else {
		klog.V(level).Infof("GRPC response: %s", protosanitizer.StripSecrets(resp))
	}
	return resp, err
}

type stripSensitiveValue struct {
	// volume_context[key] is the value to be stripped.
	key string
	// req is the csi grpc request stripped by `protosanitizer.StripSecrets`
	req fmt.Stringer
}

func StripSensitiveValue(req fmt.Stringer, key string) fmt.Stringer {
	return &stripSensitiveValue{
		key: key,
		req: req,
	}
}

func (s *stripSensitiveValue) String() string {
	return stripSensitiveValueByKey(s.req, s.key)
}

func stripSensitiveValueByKey(req fmt.Stringer, key string) string {
	var parsed map[string]interface{}

	err := json.Unmarshal([]byte(req.String()), &parsed)
	if err != nil || parsed == nil {
		return req.String()
	}

	volumeContext, ok := parsed["volume_context"].(map[string]interface{})
	if !ok {
		return req.String()
	}

	if _, ok := volumeContext[key]; !ok {
		return req.String()
	}

	volumeContext[key] = "***stripped***"

	b, err := json.Marshal(parsed)
	if err != nil {
		return req.String()
	}

	return string(b)
}
