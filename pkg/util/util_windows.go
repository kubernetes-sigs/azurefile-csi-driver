//go:build windows
// +build windows

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

package util

import (
	"os"
	"os/exec"

	"k8s.io/klog/v2"
)

// control the number of concurrent powershell commands running on Windows node
var powershellCmdSem = make(chan struct{}, 3)

func RunPowershellCmd(command string, envs ...string) ([]byte, error) {
	// acquire a semaphore to limit the number of concurrent operations
	powershellCmdSem <- struct{}{}
	defer func() { <-powershellCmdSem }()

	cmd := exec.Command("powershell", "-Mta", "-NoProfile", "-Command", command)
	cmd.Env = append(os.Environ(), envs...)
	klog.V(6).Infof("Executing command: %q", cmd.String())
	return cmd.CombinedOutput()
}
