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
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestGetVersion(t *testing.T) {
	version := GetVersion()

	expected := VersionInfo{
		DriverName:    "azurefile-csi-driver",
		DriverVersion: "N/A",
		GitCommit:     "N/A",
		BuildDate:     "N/A",
		GoVersion:     runtime.Version(),
		Compiler:      runtime.Compiler,
		Platform:      fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}

	if !reflect.DeepEqual(version, expected) {
		t.Fatalf("structs are not same\ngot:\n%+v\nexpected:\n%+v", version, expected)
	}

}

func TestGetVersionYAML(t *testing.T) {
	output, err := GetVersionYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	versioninfo := GetVersion()
	marshalled, _ := yaml.Marshal(&versioninfo)

	expected := strings.TrimSpace(string(marshalled))

	if output != expected {
		t.Fatalf("yaml not same\ngot:\n%s\nexpected:\n%s", output, expected)
	}
}
