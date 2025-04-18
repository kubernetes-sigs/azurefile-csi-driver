/*
Copyright 2021 The Kubernetes Authors.

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

package main

import (
	"flag"
	"net"
	"os"

	"k8s.io/klog/v2"

	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile-proxy/server"
	csicommon "sigs.k8s.io/azurefile-csi-driver/pkg/csi-common"
)

var (
	azurefileProxyEndpoint = flag.String("azurefile-proxy-endpoint", "unix://tmp/azurefile-proxy.sock", "azurefile-proxy endpoint")
	grpcServerRunner       = server.RunGRPCServer
)

func main() {
	klog.InitFlags(nil)
	_ = flag.Set("logtostderr", "true")
	flag.Parse()
	proto, addr, err := csicommon.ParseEndpoint(*azurefileProxyEndpoint)
	if err != nil {
		klog.Fatalf("failed to parse endpoint %v", err.Error())
	}

	if proto == "unix" {
		addr = "/" + addr
		if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
			klog.Fatalf("Failed to remove %s, error: %s", addr, err.Error())
		}
	}

	listener, err := net.Listen(proto, addr)
	if err != nil {
		klog.Fatal("cannot start server:", err)
	}

	mountServer := server.NewMountServiceServer()

	klog.V(2).Infof("Listening for connections on address: %v\n", listener.Addr())
	if err = grpcServerRunner(mountServer, false, listener); err != nil {
		klog.Fatalf("Error running grpc server %v. Error: %v", listener.Addr(), err)
	}
}
