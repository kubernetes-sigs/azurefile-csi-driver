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
	version := GetVersion(DefaultDriverName)

	expected := VersionInfo{
		DriverName:    DefaultDriverName,
		DriverVersion: "N/A",
		GitCommit:     "N/A",
		BuildDate:     "N/A",
		GoVersion:     runtime.Version(),
		Compiler:      runtime.Compiler,
		Platform:      fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}

	if !reflect.DeepEqual(version, expected) {
		t.Errorf("Unexpected error. \n Expected: %v \n Found: %v", expected, version)
	}

}

func TestGetVersionYAML(t *testing.T) {
	resp, err := GetVersionYAML("")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	versionInfo := GetVersion("")
	marshalled, _ := yaml.Marshal(&versionInfo)

	expected := strings.TrimSpace(string(marshalled))

	if resp != expected {
		t.Fatalf("Unexpected error. \n Expected:%v\nFound:%v", expected, resp)
	}
}

func TestGetUserAgent(t *testing.T) {
	tests := []struct {
		driverName      string
		customUserAgent string
		userAgentSuffix string
		expectedResult  string
	}{
		{
			driverName:      "",
			customUserAgent: "",
			userAgentSuffix: "",
			expectedResult:  fmt.Sprintf("%s/%s", "", driverVersion),
		},
		{
			driverName:      "",
			customUserAgent: "customUserAgent",
			userAgentSuffix: "",
			expectedResult:  "customUserAgent",
		},
		{
			driverName:      "drivername",
			customUserAgent: "",
			userAgentSuffix: "suffix",
			expectedResult:  fmt.Sprintf("%s/%s suffix", "drivername", driverVersion),
		},
	}

	for _, test := range tests {
		result := GetUserAgent(test.driverName, test.customUserAgent, test.userAgentSuffix)
		if result != test.expectedResult {
			t.Errorf("Unexpected result: %v, expected result: %v", result, test.expectedResult)
		}
	}
}
