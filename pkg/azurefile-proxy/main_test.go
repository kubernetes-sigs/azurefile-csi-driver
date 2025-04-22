/*
Copyright 2025 The Kubernetes Authors.

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
	"net"
	"os"
	"runtime"
	"testing"

	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile-proxy/pb"
)

func mockRunGRPCServer(_ pb.MountServiceServer, _ bool, _ net.Listener) error {
	return nil
}

func TestMain(t *testing.T) {
	// Skip test on windows
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on ", runtime.GOOS)
	}

	// mock the grpcServerRunner
	originalGRPCServerRunner := grpcServerRunner
	grpcServerRunner = mockRunGRPCServer
	defer func() { grpcServerRunner = originalGRPCServerRunner }()

	// Set the azurefile-proxy-endpoint
	os.Args = []string{"cmd", "-azurefile-proxy-endpoint=unix://tmp/test.sock"}

	// Run main
	main()
}
