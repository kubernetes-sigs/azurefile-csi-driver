/*
Copyright 2019 The Kubernetes Authors.

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
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	v1api "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/accountclient/mock_accountclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/fileshareclient/mock_fileshareclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/mock_azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/cache"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/storage"
)

const (
	fakeNodeID     = "fakeNodeID"
	fakeDriverName = "fake"
)

var (
	vendorVersion = "0.3.0"
)

func NewFakeDriver() *Driver {
	driverOptions := DriverOptions{
		NodeID:                      fakeNodeID,
		DriverName:                  DefaultDriverName,
		KubeConfig:                  "",
		Endpoint:                    "tcp://127.0.0.1:0",
		WaitForAzCopyTimeoutMinutes: 1,
		EnableKataCCMount:           true,
	}
	driver := NewDriver(&driverOptions)
	driver.Name = fakeDriverName
	driver.Version = vendorVersion
	driver.AddControllerServiceCapabilities(
		[]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		})
	driver.cloud = &storage.AccountRepo{}
	return driver
}

func NewFakeDriverCustomOptions(opts DriverOptions) *Driver {
	var err error
	driverOptions := opts
	driver := NewDriver(&driverOptions)
	driver.Name = fakeDriverName
	driver.Version = vendorVersion
	if err != nil {
		panic(err)
	}
	driver.AddControllerServiceCapabilities(
		[]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		})
	return driver
}

func TestNewFakeDriver(t *testing.T) {
	driverOptions := DriverOptions{
		NodeID:     fakeNodeID,
		DriverName: DefaultDriverName,
		Endpoint:   "tcp://127.0.0.1:0",
		KubeConfig: "",
	}
	d := NewDriver(&driverOptions)
	assert.NotNil(t, d)
}

func TestAppendDefaultCifsMountOptions(t *testing.T) {
	tests := []struct {
		options                 []string
		appendClosetimeoOption  bool
		appendNoShareSockOption bool
		expected                []string
	}{
		{
			options: []string{"dir_mode=0777"},
			expected: []string{"dir_mode=0777",
				fmt.Sprintf("%s=%s", fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
				mfsymlinks,
			},
		},
		{
			options: []string{"file_mode=0777"},
			expected: []string{"file_mode=0777",
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
				mfsymlinks,
			},
		},
		{
			options: []string{"vers=2.1"},
			expected: []string{"vers=2.1",
				fmt.Sprintf("%s=%s", fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
				mfsymlinks,
			},
		},
		{
			options: []string{"file_mode=0777", "dir_mode=0777"},
			expected: []string{
				"file_mode=0777", "dir_mode=0777",
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
				mfsymlinks,
			},
		},
		{
			options: []string{"actimeo=3"},
			expected: []string{
				"actimeo=3",
				fmt.Sprintf("%s=%s", fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				mfsymlinks,
			},
		},
		{
			options: []string{"acregmax=1"},
			expected: []string{
				"acregmax=1",
				fmt.Sprintf("%s=%s", fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				mfsymlinks,
			},
		},
		{
			options: []string{"acdirmax=2"},
			expected: []string{
				"acdirmax=2",
				fmt.Sprintf("%s=%s", fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				mfsymlinks,
			},
		},
		{
			options: []string{mfsymlinks},
			expected: []string{
				mfsymlinks,
				fmt.Sprintf("%s=%s", fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
			},
		},
		{
			options: []string{"vers=3.1.1"},
			expected: []string{"dir_mode=0777",
				fmt.Sprintf("%s=%s", fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", "vers", "3.1.1"),
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
				mfsymlinks,
			},
		},
		{
			options: []string{""},
			expected: []string{"", fmt.Sprintf("%s=%s",
				fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
				mfsymlinks,
			},
		},
		{
			options:                []string{""},
			appendClosetimeoOption: true,
			expected: []string{"", fmt.Sprintf("%s=%s",
				fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
				mfsymlinks,
				"sloppy,closetimeo=0",
			},
		},
		{
			options:                 []string{""},
			appendNoShareSockOption: true,
			expected: []string{"", fmt.Sprintf("%s=%s",
				fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
				mfsymlinks,
				"nosharesock",
			},
		},
		{
			options:                 []string{"nosharesock"},
			appendNoShareSockOption: true,
			expected: []string{fmt.Sprintf("%s=%s",
				fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				fmt.Sprintf("%s=%s", actimeo, defaultActimeo),
				mfsymlinks,
				"nosharesock",
			},
		},
	}

	for _, test := range tests {
		result := appendDefaultCifsMountOptions(test.options, test.appendNoShareSockOption, test.appendClosetimeoOption)
		sort.Strings(result)
		sort.Strings(test.expected)

		if !reflect.DeepEqual(result, test.expected) {
			t.Errorf("input: %q, appendDefaultCifsMountOptions result: %q, expected: %q", test.options, result, test.expected)
		}
	}
}

func TestAppendDefaultNfsMountOptions(t *testing.T) {
	tests := []struct {
		options                []string
		appendNoResvPortOption bool
		appendActimeoOption    bool
		expected               []string
	}{
		{
			options:                []string{""},
			appendNoResvPortOption: false,
			appendActimeoOption:    false,
			expected:               []string{""},
		},
		{
			options:                []string{},
			appendNoResvPortOption: true,
			appendActimeoOption:    true,
			expected:               []string{fmt.Sprintf("%s=%s", actimeo, defaultActimeo), noResvPort},
		},
		{
			options:                []string{noResvPort},
			appendNoResvPortOption: true,
			appendActimeoOption:    true,
			expected:               []string{fmt.Sprintf("%s=%s", actimeo, defaultActimeo), noResvPort},
		},
		{
			options:                []string{fmt.Sprintf("%s=%s", actimeo, "60")},
			appendNoResvPortOption: true,
			appendActimeoOption:    true,
			expected:               []string{fmt.Sprintf("%s=%s", actimeo, "60"), noResvPort},
		},
	}

	for _, test := range tests {
		result := appendDefaultNfsMountOptions(test.options, test.appendNoResvPortOption, test.appendActimeoOption)
		sort.Strings(result)
		sort.Strings(test.expected)

		if !reflect.DeepEqual(result, test.expected) {
			t.Errorf("input: %q, appendDefaultNfsMountOptions result: %q, expected: %q", test.options, result, test.expected)
		}
	}
}

func TestGetFileShareInfo(t *testing.T) {
	tests := []struct {
		id                string
		resourceGroupName string
		accountName       string
		fileShareName     string
		diskName          string
		namespace         string
		subsID            string
		expectedError     error
	}{
		{
			id:                "rg#f5713de20cde511e8ba4900#pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41#diskname1.vhd#1620118846",
			resourceGroupName: "rg",
			accountName:       "f5713de20cde511e8ba4900",
			fileShareName:     "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:          "diskname1.vhd",
			namespace:         "",
			expectedError:     nil,
		},
		{
			id:                "rg#f5713de20cde511e8ba4900#pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41#diskname1.vhd#1620118846#namespace",
			resourceGroupName: "rg",
			accountName:       "f5713de20cde511e8ba4900",
			fileShareName:     "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:          "diskname1.vhd",
			namespace:         "namespace",
			expectedError:     nil,
		},
		{
			id:                "#f5713de20cde511e8ba4900#pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41#diskname1.vhd#namespace",
			resourceGroupName: "",
			accountName:       "f5713de20cde511e8ba4900",
			fileShareName:     "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:          "diskname1.vhd",
			namespace:         "namespace",
			expectedError:     nil,
		},
		{
			id:                "rg#f5713de20cde511e8ba4900#pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41#diskname2.vhd#",
			resourceGroupName: "rg",
			accountName:       "f5713de20cde511e8ba4900",
			fileShareName:     "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:          "diskname2.vhd",
			expectedError:     nil,
		},
		{
			id:                "rg#f5713de20cde511e8ba4900#pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41#diskname3.vhd",
			resourceGroupName: "rg",
			accountName:       "f5713de20cde511e8ba4900",
			fileShareName:     "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:          "diskname3.vhd",
			expectedError:     nil,
		},
		{
			id:                "rg#f5713de20cde511e8ba4900#pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			resourceGroupName: "rg",
			accountName:       "f5713de20cde511e8ba4900",
			fileShareName:     "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:          "",
			expectedError:     nil,
		},
		{
			id:                "rg#f5713de20cde511e8ba4900",
			resourceGroupName: "",
			accountName:       "",
			fileShareName:     "",
			diskName:          "",
			expectedError:     fmt.Errorf("error parsing volume id: \"rg#f5713de20cde511e8ba4900\", should at least contain two #"),
		},
		{
			id:                "rg",
			resourceGroupName: "",
			accountName:       "",
			fileShareName:     "",
			diskName:          "",
			expectedError:     fmt.Errorf("error parsing volume id: \"rg\", should at least contain two #"),
		},
		{
			id:                "",
			resourceGroupName: "",
			accountName:       "",
			fileShareName:     "",
			diskName:          "",
			expectedError:     fmt.Errorf("error parsing volume id: \"\", should at least contain two #"),
		},
		{
			id:                "rg#f5713de20cde511e8ba4900#fileShareName#diskname.vhd#uuid#namespace#subsID",
			resourceGroupName: "rg",
			accountName:       "f5713de20cde511e8ba4900",
			fileShareName:     "fileShareName",
			diskName:          "diskname.vhd",
			namespace:         "namespace",
			subsID:            "subsID",
			expectedError:     nil,
		},
		{
			id:                "rg#f5713de20cde511e8ba4900#fileShareName#diskname.vhd#uuid#namespace",
			resourceGroupName: "rg",
			accountName:       "f5713de20cde511e8ba4900",
			fileShareName:     "fileShareName",
			diskName:          "diskname.vhd",
			namespace:         "namespace",
			subsID:            "",
			expectedError:     nil,
		},
	}

	for _, test := range tests {
		resourceGroupName, accountName, fileShareName, diskName, namespace, subsID, expectedError := GetFileShareInfo(test.id)
		if resourceGroupName != test.resourceGroupName {
			t.Errorf("GetFileShareInfo(%q) returned with: %q, expected: %q", test.id, resourceGroupName, test.resourceGroupName)
		}
		if accountName != test.accountName {
			t.Errorf("GetFileShareInfo(%q) returned with: %q, expected: %q", test.id, accountName, test.accountName)
		}
		if fileShareName != test.fileShareName {
			t.Errorf("GetFileShareInfo(%q) returned with: %q, expected: %q", test.id, fileShareName, test.fileShareName)
		}
		if diskName != test.diskName {
			t.Errorf("GetFileShareInfo(%q) returned with: %q, expected: %q", test.id, diskName, test.diskName)
		}
		if namespace != test.namespace {
			t.Errorf("GetFileShareInfo(%q) returned with: %q, expected: %q", test.id, namespace, test.namespace)
		}
		if subsID != test.subsID {
			t.Errorf("GetFileShareInfo(%q) returned with: %q, expected: %q", test.id, subsID, test.subsID)
		}
		if !reflect.DeepEqual(expectedError, test.expectedError) {
			t.Errorf("GetFileShareInfo(%q) returned with: %v, expected: %v", test.id, expectedError, test.expectedError)
		}
	}
}

func TestGetStorageAccount(t *testing.T) {
	emptyAccountKeyMap := map[string]string{
		"accountname": "testaccount",
		"accountkey":  "",
	}

	emptyAccountNameMap := map[string]string{
		defaultSecretAccountName: "",
		defaultSecretAccountKey:  "testkey",
	}

	emptyAzureAccountKeyMap := map[string]string{
		defaultSecretAccountName: "testaccount",
		defaultSecretAccountKey:  "",
	}

	emptyAzureAccountNameMap := map[string]string{
		defaultSecretAccountName: "",
		defaultSecretAccountKey:  "testkey",
	}

	tests := []struct {
		options   map[string]string
		expected1 string
		expected2 string
		expected3 error
	}{
		{
			options: map[string]string{
				"accountname": "testaccount",
				"accountkey":  "testkey",
			},
			expected1: "testaccount",
			expected2: "testkey",
			expected3: nil,
		},
		{
			options: map[string]string{
				defaultSecretAccountName: "testaccount",
				defaultSecretAccountKey:  "testkey",
			},
			expected1: "testaccount",
			expected2: "testkey",
			expected3: nil,
		},
		{
			options: map[string]string{
				"accountname": "",
				"accountkey":  "",
			},
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("could not find accountname or azurestorageaccountname field in secrets"),
		},
		{
			options:   emptyAccountKeyMap,
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("could not find accountkey or azurestorageaccountkey field in secrets"),
		},
		{
			options:   emptyAccountNameMap,
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("could not find accountname or azurestorageaccountname field secrets"),
		},
		{
			options:   emptyAzureAccountKeyMap,
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("could not find accountkey or azurestorageaccountkey field in secrets"),
		},
		{
			options:   emptyAzureAccountNameMap,
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("could not find accountname or azurestorageaccountname field in secrets"),
		},
		{
			options:   nil,
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("unexpected: getStorageAccount secrets is nil"),
		},
	}

	for _, test := range tests {
		result1, result2, result3 := getStorageAccount(test.options)
		if !reflect.DeepEqual(result1, test.expected1) || !reflect.DeepEqual(result2, test.expected2) {
			t.Errorf("input: %q, getStorageAccount result1: %q, expected1: %q, result2: %q, expected2: %q, result3: %q, expected3: %q", test.options, result1, test.expected1, result2, test.expected2,
				result3, test.expected3)
		} else {
			if result1 == "" || result2 == "" {
				assert.Error(t, result3)
			}
		}
	}
}

func TestGetValidFileShareName(t *testing.T) {
	tests := []struct {
		volumeName string
		expected   string
	}{
		{
			volumeName: "aqz",
			expected:   "aqz",
		},
		{
			volumeName: "029",
			expected:   "029",
		},
		{
			volumeName: "a--z",
			expected:   "a-z",
		},
		{
			volumeName: "A2Z",
			expected:   "a2z",
		},
		{
			volumeName: "1234567891234567891234567891234567891234567891234567891234567891",
			expected:   "123456789123456789123456789123456789123456789123456789123456789",
		},
		{
			volumeName: "aq",
			expected:   "pvc-file-dynamic",
		},
	}

	for _, test := range tests {
		result := getValidFileShareName(test.volumeName)
		if test.volumeName == "aq" {
			assert.Contains(t, result, test.expected)
		} else if !reflect.DeepEqual(result, test.expected) {
			t.Errorf("input: %q, getValidFileShareName result: %q, expected: %q", test.volumeName, result, test.expected)
		}
	}
}

func TestCheckShareNameBeginAndEnd(t *testing.T) {
	tests := []struct {
		fileShareName string
		expected      bool
	}{
		{
			fileShareName: "aqz",
			expected:      true,
		},
		{
			fileShareName: "029",
			expected:      true,
		},
		{
			fileShareName: "a-9",
			expected:      true,
		},
		{
			fileShareName: "0-z",
			expected:      true,
		},
		{
			fileShareName: "-1-",
			expected:      false,
		},
		{
			fileShareName: ":1p",
			expected:      false,
		},
	}

	for _, test := range tests {
		result := checkShareNameBeginAndEnd(test.fileShareName)
		if !reflect.DeepEqual(result, test.expected) {
			t.Errorf("input: %q, checkShareNameBeginAndEnd result: %v, expected: %v", test.fileShareName, result, test.expected)
		}
	}
}

func TestGetSnapshot(t *testing.T) {
	tests := []struct {
		options   string
		expected1 string
		expected2 error
	}{
		{
			options:   "rg#f123#csivolumename#diskname#2019-08-22T07:17:53.0000000Z",
			expected1: "2019-08-22T07:17:53.0000000Z",
			expected2: nil,
		},
		{
			options:   "rg#f123#csivolumename#diskname#uuid#2020-08-22T07:17:53.0000000Z",
			expected1: "2020-08-22T07:17:53.0000000Z",
			expected2: nil,
		},
		{
			options:   "rg#f123#csivolumename#diskname#uuid#default#2021-08-22T07:17:53.0000000Z",
			expected1: "2021-08-22T07:17:53.0000000Z",
			expected2: nil,
		},
		{
			options:   "rg#f123#csivolumename",
			expected1: "",
			expected2: fmt.Errorf("error parsing volume id: \"rg#f123#csivolumename\", should at least contain four #"),
		},
		{
			options:   "rg#f123",
			expected1: "",
			expected2: fmt.Errorf("error parsing volume id: \"rg#f123\", should at least contain four #"),
		},
		{
			options:   "rg",
			expected1: "",
			expected2: fmt.Errorf("error parsing volume id: \"rg\", should at least contain four #"),
		},
		{
			options:   "",
			expected1: "",
			expected2: fmt.Errorf("error parsing volume id: \"\", should at least contain four #"),
		},
	}

	for _, test := range tests {
		result1, result2 := getSnapshot(test.options)
		if !reflect.DeepEqual(result1, test.expected1) || !reflect.DeepEqual(result2, test.expected2) {
			t.Errorf("input: %q, getSnapshot result1: %q, expected1: %q, result2: %q, expected2: %q, ", test.options, result1, test.expected1, result2, test.expected2)
		}
	}
}

func TestIsCorruptedDir(t *testing.T) {
	skipIfTestingOnWindows(t)
	existingMountPath, err := os.MkdirTemp(os.TempDir(), "csi-mount-test")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(existingMountPath)

	curruptedPath := filepath.Join(existingMountPath, "curruptedPath")
	if err := os.Symlink(existingMountPath, curruptedPath); err != nil {
		t.Fatalf("failed to create curruptedPath: %v", err)
	}

	tests := []struct {
		desc           string
		dir            string
		expectedResult bool
	}{
		{
			desc:           "NotExist dir",
			dir:            "/tmp/NotExist",
			expectedResult: false,
		},
		{
			desc:           "Existing dir",
			dir:            existingMountPath,
			expectedResult: false,
		},
	}

	for i, test := range tests {
		isCorruptedDir := IsCorruptedDir(test.dir)
		assert.Equal(t, test.expectedResult, isCorruptedDir, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestNewDriver(t *testing.T) {
	tests := []struct {
		nodeID string
	}{
		{
			nodeID: fakeNodeID,
		},
		{
			nodeID: "",
		},
	}

	for _, test := range tests {
		driverOptions := DriverOptions{
			NodeID:     test.nodeID,
			DriverName: DefaultDriverName,
		}
		result := NewDriver(&driverOptions)
		assert.NotNil(t, result)
		assert.Equal(t, result.NodeID, test.nodeID)
	}
}

func TestGetFileURL(t *testing.T) {
	tests := []struct {
		accountName           string
		accountKey            string
		storageEndpointSuffix string
		fileShareName         string
		diskName              string
		expectedError         error
	}{
		{
			accountName:           "f5713de20cde511e8ba4900",
			accountKey:            base64.StdEncoding.EncodeToString([]byte("acc_key")),
			storageEndpointSuffix: "suffix",
			fileShareName:         "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:              "diskname.vhd",
			expectedError:         nil,
		},
		{
			accountName:           "",
			accountKey:            base64.StdEncoding.EncodeToString([]byte("acc_key")),
			storageEndpointSuffix: "suffix",
			fileShareName:         "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:              "diskname.vhd",
			expectedError:         nil,
		},
		{
			accountName:           "",
			accountKey:            "",
			storageEndpointSuffix: "",
			fileShareName:         "",
			diskName:              "",
			expectedError:         nil,
		},
		{
			accountName:           "f5713de20cde511e8ba4900",
			accountKey:            "abc",
			storageEndpointSuffix: "suffix",
			fileShareName:         "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:              "diskname.vhd",
			expectedError:         fmt.Errorf("NewSharedKeyCredential(f5713de20cde511e8ba4900) failed with error: decode account key: illegal base64 data at input byte 0"),
		},
		{
			accountName:           "^f5713de20cde511e8ba4900",
			accountKey:            base64.StdEncoding.EncodeToString([]byte("acc_key")),
			storageEndpointSuffix: "suffix",
			fileShareName:         "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:              "diskname.vhd",
			expectedError:         fmt.Errorf("parse fileURLTemplate error: %v", &url.Error{Op: "parse", URL: "https://^f5713de20cde511e8ba4900.file.suffix/pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41/diskname.vhd", Err: url.InvalidHostError("^")}),
		},
	}
	for _, test := range tests {
		_, err := getDirectoryClient(test.accountName, test.accountKey, test.storageEndpointSuffix, test.fileShareName, test.diskName)
		if !reflect.DeepEqual(err, test.expectedError) {
			t.Errorf("accountName: %v accountKey: %v storageEndpointSuffix: %v fileShareName: %v diskName: %v Error: %v",
				test.accountName, test.accountKey, test.storageEndpointSuffix, test.fileShareName, test.diskName, err)
		}
	}
}

func TestGetAccountInfo(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	validSecret := map[string]string{
		defaultSecretAccountName: "testaccount",
		defaultSecretAccountKey:  "testkey",
	}
	emptySecret := map[string]string{}
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := []*armstorage.AccountKey{
		{Value: &value},
	}

	clientSet := fake.NewSimpleClientset()

	tests := []struct {
		volumeID            string
		rgName              string
		secrets             map[string]string
		reqContext          map[string]string
		expectErr           bool
		err                 error
		expectAccountName   string
		expectFileShareName string
		expectDiskName      string
	}{
		{
			volumeID: "##",
			rgName:   "",
			secrets:  emptySecret,
			reqContext: map[string]string{
				shareNameField: "test_sharename",
				diskNameField:  "test_diskname",
			},
			expectErr:           false,
			err:                 nil,
			expectAccountName:   "",
			expectFileShareName: "test_sharename",
			expectDiskName:      "test_diskname",
		},
		{
			volumeID: "vol_1##",
			rgName:   "vol_1",
			secrets:  validSecret,
			reqContext: map[string]string{
				shareNameField: "test_sharename",
				diskNameField:  "test_diskname",
			},
			expectErr:           false,
			err:                 nil,
			expectAccountName:   "testaccount",
			expectFileShareName: "test_sharename",
			expectDiskName:      "test_diskname",
		},
		{
			volumeID: "vol_2##",
			rgName:   "vol_2",
			secrets:  emptySecret,
			reqContext: map[string]string{
				shareNameField: "test_sharename",
				diskNameField:  "test_diskname",
			},
			expectErr:           false,
			err:                 nil,
			expectAccountName:   "",
			expectFileShareName: "test_sharename",
			expectDiskName:      "test_diskname",
		},
		{
			volumeID: "uniqe-volumeid-nfs",
			rgName:   "vol_nfs",
			secrets:  emptySecret,
			reqContext: map[string]string{
				resourceGroupField:  "vol_nfs",
				storageAccountField: "test_accountname",
				shareNameField:      "test_sharename",
				protocolField:       "nfs",
			},
			expectErr:           false,
			err:                 nil,
			expectAccountName:   "test_accountname",
			expectFileShareName: "test_sharename",
			expectDiskName:      "",
		},
		{
			volumeID: "invalid_getLatestAccountKey_value##",
			rgName:   "vol_2",
			secrets:  emptySecret,
			reqContext: map[string]string{
				shareNameField:           "test_sharename",
				getLatestAccountKeyField: "invalid",
			},
			expectErr:           true,
			err:                 fmt.Errorf("invalid %s: %s in volume context", getLatestAccountKeyField, "invalid"),
			expectAccountName:   "",
			expectFileShareName: "test_sharename",
			expectDiskName:      "test_diskname",
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
		d.cloud.ComputeClientFactory = mock_azclient.NewMockClientFactory(ctrl)
		d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClient().Return(mockStorageAccountsClient).AnyTimes()
		d.kubeClient = clientSet
		d.cloud.Environment = &azclient.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), test.rgName).Return(key, nil).AnyTimes()
		rgName, accountName, _, fileShareName, diskName, _, err := d.GetAccountInfo(context.Background(), test.volumeID, test.secrets, test.reqContext)
		if test.expectErr && err == nil {
			t.Errorf("Unexpected non-error")
			continue
		}
		if !test.expectErr && err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}

		if err == nil {
			assert.Equal(t, test.rgName, rgName, test.volumeID)
			assert.Equal(t, test.expectAccountName, accountName, test.volumeID)
			assert.Equal(t, test.expectFileShareName, fileShareName, test.volumeID)
			assert.Equal(t, test.expectDiskName, diskName, test.volumeID)
		}
	}
}

func TestCreateDisk(t *testing.T) {
	skipIfTestingOnWindows(t)
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}
	tests := []struct {
		accountName           string
		accountKey            string
		storageEndpointSuffix string
		fileShareName         string
		diskName              string
		expectedError         error
	}{
		{
			accountName:           "f5713de20cde511e8ba4900",
			accountKey:            "abc",
			storageEndpointSuffix: "suffix",
			fileShareName:         "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:              "diskname.vhd",
			expectedError:         fmt.Errorf("NewSharedKeyCredential(f5713de20cde511e8ba4900) failed with error: illegal base64 data at input byte 0"),
		},
		{
			accountName:           "f5713de20cde511e8ba4900",
			accountKey:            base64.StdEncoding.EncodeToString([]byte("acc_key")),
			storageEndpointSuffix: "suffix",
			fileShareName:         "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:              "diskname.vhd",
			expectedError:         nil,
		},
	}

	for _, test := range tests {
		_ = createDisk(context.Background(), test.accountName, test.accountKey, test.storageEndpointSuffix,
			test.fileShareName, test.diskName, 20)
	}
}

func TestGetFileShareQuota(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	shareQuota := int32(10)
	resourceGroupName := "rg"
	accountName := "accountname"
	fileShareName := "filesharename"

	tests := []struct {
		desc                string
		secrets             map[string]string
		mockedFileShareResp *armstorage.FileShare
		mockedFileShareErr  error
		expectedQuota       int
		expectedError       error
	}{
		{
			desc:                "Get file share return error",
			secrets:             map[string]string{},
			mockedFileShareResp: &armstorage.FileShare{},
			mockedFileShareErr:  fmt.Errorf("test error"),
			expectedQuota:       -1,
			expectedError:       fmt.Errorf("test error"),
		},
		{
			desc:                "Share not found",
			secrets:             map[string]string{},
			mockedFileShareResp: &armstorage.FileShare{},
			mockedFileShareErr: &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
			},
			expectedQuota: -1,
			expectedError: nil,
		},
		{
			desc:                "Volume already exists",
			secrets:             map[string]string{},
			mockedFileShareResp: &armstorage.FileShare{FileShareProperties: &armstorage.FileShareProperties{ShareQuota: &shareQuota}},
			mockedFileShareErr:  nil,
			expectedQuota:       int(shareQuota),
			expectedError:       nil,
		},
		{
			desc: "Could not find accountname in secrets",
			secrets: map[string]string{
				"secrets": "secrets",
			},
			mockedFileShareResp: &armstorage.FileShare{},
			mockedFileShareErr:  nil,
			expectedQuota:       -1,
			expectedError:       fmt.Errorf("could not find accountname or azurestorageaccountname field in secrets"),
		},
		{
			desc: "Error creating azure client",
			secrets: map[string]string{
				"accountname": "ut",
				"accountkey":  "testkey",
			},
			mockedFileShareResp: &armstorage.FileShare{},
			mockedFileShareErr:  nil,
			expectedQuota:       -1,
			expectedError:       fmt.Errorf("error creating azure client: decode account key: illegal base64 data at input byte 4"),
		},
	}

	for _, test := range tests {
		mockFileClient := mock_fileshareclient.NewMockInterface(ctrl)
		clientFactory := mock_azclient.NewMockClientFactory(ctrl)
		clientFactory.EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, nil).AnyTimes()
		d.cloud.ComputeClientFactory = clientFactory
		mockFileClient.EXPECT().Get(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockedFileShareResp, test.mockedFileShareErr).AnyTimes()
		quota, err := d.getFileShareQuota(context.TODO(), &storage.AccountOptions{
			ResourceGroup:  resourceGroupName,
			Name:           accountName,
			SubscriptionID: "subsID",
		}, fileShareName, test.secrets, "")
		if !reflect.DeepEqual(err, test.expectedError) {
			t.Errorf("test name: %s, Unexpected error: %v, expected error: %v", test.desc, err, test.expectedError)
		}
		if quota != test.expectedQuota {
			t.Errorf("Unexpected return quota: %d, expected: %d", quota, test.expectedQuota)
		}
	}
}

func TestRun(t *testing.T) {
	fakeCredFile := "fake-cred-file.json"
	fakeCredContent := `{
    "tenantId": "1234",
    "subscriptionId": "12345",
    "aadClientId": "123456",
    "aadClientSecret": "1234567",
    "resourceGroup": "rg1",
    "location": "loc"
}`

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "Successful run",
			testFunc: func(t *testing.T) {
				if err := os.WriteFile(fakeCredFile, []byte(fakeCredContent), 0666); err != nil {
					t.Error(err)
				}

				defer func() {
					if err := os.Remove(fakeCredFile); err != nil {
						t.Error(err)
					}
				}()

				originalCredFile, ok := os.LookupEnv(DefaultAzureCredentialFileEnv)
				if ok {
					defer os.Setenv(DefaultAzureCredentialFileEnv, originalCredFile)
				} else {
					defer os.Unsetenv(DefaultAzureCredentialFileEnv)
				}
				os.Setenv(DefaultAzureCredentialFileEnv, fakeCredFile)

				d := NewFakeDriver()
				ctx, cancelFn := context.WithCancel(context.Background())
				go func() {
					time.Sleep(1 * time.Second)
					cancelFn()
				}()
				if err := d.Run(ctx); err != nil {
					t.Error(err.Error())
				}

			},
		},
		{
			name: "Successful run with node ID missing",
			testFunc: func(t *testing.T) {
				if err := os.WriteFile(fakeCredFile, []byte(fakeCredContent), 0666); err != nil {
					t.Error(err)
				}

				defer func() {
					if err := os.Remove(fakeCredFile); err != nil {
						t.Error(err)
					}
				}()

				originalCredFile, ok := os.LookupEnv(DefaultAzureCredentialFileEnv)
				if ok {
					defer os.Setenv(DefaultAzureCredentialFileEnv, originalCredFile)
				} else {
					defer os.Unsetenv(DefaultAzureCredentialFileEnv)
				}
				os.Setenv(DefaultAzureCredentialFileEnv, fakeCredFile)

				d := NewFakeDriver()
				ctx, cancelFn := context.WithCancel(context.Background())
				go func() {
					time.Sleep(1 * time.Second)
					cancelFn()
				}()
				d.cloud = &storage.AccountRepo{}
				d.NodeID = ""
				if err := d.Run(ctx); err != nil {
					t.Error(err.Error())
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestIsSupportedProtocol(t *testing.T) {
	tests := []struct {
		protocol       string
		expectedResult bool
	}{
		{
			protocol:       "",
			expectedResult: true,
		},
		{
			protocol:       "smb",
			expectedResult: true,
		},
		{
			protocol:       "nfs",
			expectedResult: true,
		},
		{
			protocol:       "invalid",
			expectedResult: false,
		},
	}

	for _, test := range tests {
		result := isSupportedProtocol(test.protocol)
		if result != test.expectedResult {
			t.Errorf("isSupportedProtocol(%s) returned with %v, not equal to %v", test.protocol, result, test.expectedResult)
		}
	}
}

func TestIsSupportedShareAccessTier(t *testing.T) {
	tests := []struct {
		accessTier     string
		expectedResult bool
	}{
		{
			accessTier:     "",
			expectedResult: true,
		},
		{
			accessTier:     "TransactionOptimized",
			expectedResult: true,
		},
		{
			accessTier:     "Hot",
			expectedResult: true,
		},
		{
			accessTier:     "Cool",
			expectedResult: true,
		},
		{
			accessTier:     "Premium",
			expectedResult: true,
		},
		{
			accessTier:     "transactionOptimized",
			expectedResult: false,
		},
		{
			accessTier:     "premium",
			expectedResult: false,
		},
		{
			accessTier:     "unknown",
			expectedResult: false,
		},
	}

	for _, test := range tests {
		result := isSupportedShareAccessTier(test.accessTier)
		if result != test.expectedResult {
			t.Errorf("isSupportedTier(%s) returned with %v, not equal to %v", test.accessTier, result, test.expectedResult)
		}
	}
}

func TestIsSupportedAccountAccessTier(t *testing.T) {
	tests := []struct {
		accessTier     string
		expectedResult bool
	}{
		{
			accessTier:     "",
			expectedResult: true,
		},
		{
			accessTier:     "TransactionOptimized",
			expectedResult: false,
		},
		{
			accessTier:     "Hot",
			expectedResult: true,
		},
		{
			accessTier:     "Cool",
			expectedResult: true,
		},
		{
			accessTier:     "Premium",
			expectedResult: true,
		},
		{
			accessTier:     "transactionOptimized",
			expectedResult: false,
		},
		{
			accessTier:     "premium",
			expectedResult: false,
		},
		{
			accessTier:     "unknown",
			expectedResult: false,
		},
	}

	for _, test := range tests {
		result := isSupportedAccountAccessTier(test.accessTier)
		if result != test.expectedResult {
			t.Errorf("isSupportedTier(%s) returned with %v, not equal to %v", test.accessTier, result, test.expectedResult)
		}
	}
}

func TestIsSupportedRootSquashType(t *testing.T) {
	tests := []struct {
		rootSquashType string
		expectedResult bool
	}{
		{
			rootSquashType: "",
			expectedResult: true,
		},
		{
			rootSquashType: "AllSquash",
			expectedResult: true,
		},
		{
			rootSquashType: "NoRootSquash",
			expectedResult: true,
		},
		{
			rootSquashType: "RootSquash",
			expectedResult: true,
		},
		{
			rootSquashType: "unknown",
			expectedResult: false,
		},
	}

	for _, test := range tests {
		result := isSupportedRootSquashType(test.rootSquashType)
		if result != test.expectedResult {
			t.Errorf("isSupportedRootSquashType(%s) returned with %v, not equal to %v", test.rootSquashType, result, test.expectedResult)
		}
	}
}

func TestGetSubnetResourceID(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "NetworkResourceSubscriptionID is Empty",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &storage.AccountRepo{}
				d.cloud.SubscriptionID = "fakeSubID"
				d.cloud.NetworkResourceSubscriptionID = ""
				d.cloud.ResourceGroup = "foo"
				d.cloud.VnetResourceGroup = "foo"
				actualOutput := d.getSubnetResourceID("", "", "")
				expectedOutput := fmt.Sprintf(subnetTemplate, d.cloud.SubscriptionID, "foo", d.cloud.VnetName, d.cloud.SubnetName)
				assert.Equal(t, actualOutput, expectedOutput, "cloud.SubscriptionID should be used as the SubID")
			},
		},
		{
			name: "NetworkResourceSubscriptionID is not Empty",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &storage.AccountRepo{}
				d.cloud.SubscriptionID = "fakeSubID"
				d.cloud.NetworkResourceSubscriptionID = "fakeNetSubID"
				d.cloud.ResourceGroup = "foo"
				d.cloud.VnetResourceGroup = "foo"
				actualOutput := d.getSubnetResourceID("", "", "")
				expectedOutput := fmt.Sprintf(subnetTemplate, d.cloud.NetworkResourceSubscriptionID, "foo", d.cloud.VnetName, d.cloud.SubnetName)
				assert.Equal(t, actualOutput, expectedOutput, "cloud.NetworkResourceSubscriptionID should be used as the SubID")
			},
		},
		{
			name: "VnetResourceGroup is Empty",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &storage.AccountRepo{}
				d.cloud.SubscriptionID = "bar"
				d.cloud.NetworkResourceSubscriptionID = "bar"
				d.cloud.ResourceGroup = "fakeResourceGroup"
				d.cloud.VnetResourceGroup = ""
				actualOutput := d.getSubnetResourceID("", "", "")
				expectedOutput := fmt.Sprintf(subnetTemplate, "bar", d.cloud.ResourceGroup, d.cloud.VnetName, d.cloud.SubnetName)
				assert.Equal(t, actualOutput, expectedOutput, "cloud.Resourcegroup should be used as the rg")
			},
		},
		{
			name: "VnetResourceGroup is not Empty",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &storage.AccountRepo{}
				d.cloud.SubscriptionID = "bar"
				d.cloud.NetworkResourceSubscriptionID = "bar"
				d.cloud.ResourceGroup = "fakeResourceGroup"
				d.cloud.VnetResourceGroup = "fakeVnetResourceGroup"
				actualOutput := d.getSubnetResourceID("", "", "")
				expectedOutput := fmt.Sprintf(subnetTemplate, "bar", d.cloud.VnetResourceGroup, d.cloud.VnetName, d.cloud.SubnetName)
				assert.Equal(t, actualOutput, expectedOutput, "cloud.VnetResourceGroup should be used as the rg")
			},
		},
		{
			name: "VnetResourceGroup, vnetName, subnetName is specified",
			testFunc: func(t *testing.T) {
				d := NewFakeDriver()
				d.cloud = &storage.AccountRepo{}
				d.cloud.SubscriptionID = "bar"
				d.cloud.NetworkResourceSubscriptionID = "bar"
				d.cloud.ResourceGroup = "fakeResourceGroup"
				d.cloud.VnetResourceGroup = "fakeVnetResourceGroup"
				actualOutput := d.getSubnetResourceID("vnetrg", "vnetName", "subnetName")
				expectedOutput := fmt.Sprintf(subnetTemplate, "bar", "vnetrg", "vnetName", "subnetName")
				assert.Equal(t, actualOutput, expectedOutput, "VnetResourceGroup, vnetName, subnetName is specified")
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestGetTotalAccountQuota(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	fileShareItemsWithQuota := []*armstorage.FileShareItem{
		{
			Properties: &armstorage.FileShareProperties{
				ShareQuota: to.Ptr(int32(100)),
			},
		},
		{
			Properties: &armstorage.FileShareProperties{
				ShareQuota: to.Ptr(int32(200)),
			},
		},
	}

	tests := []struct {
		name             string
		subsID           string
		resourceGroup    string
		accountName      string
		fileShareItems   []*armstorage.FileShareItem
		listFileShareErr error
		expectedQuota    int32
		expectedShareNum int32
		expectedErr      error
	}{
		{
			name:             "GetTotalAccountQuota success",
			expectedQuota:    0,
			expectedShareNum: 0,
		},
		{
			name:             "GetTotalAccountQuota success (with 2 file shares)",
			fileShareItems:   fileShareItemsWithQuota,
			expectedQuota:    300,
			expectedShareNum: 2,
		},
		{
			name:             "list file share error",
			listFileShareErr: fmt.Errorf("list file share error"),
			expectedQuota:    -1,
			expectedShareNum: -1,
			expectedErr:      fmt.Errorf("list file share error"),
		},
	}

	for _, test := range tests {
		mockFileClient := mock_fileshareclient.NewMockInterface(ctrl)
		d.cloud.ComputeClientFactory = mock_azclient.NewMockClientFactory(ctrl)
		d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, nil).AnyTimes()
		mockFileClient.EXPECT().List(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any()).Return(test.fileShareItems, test.listFileShareErr).AnyTimes()

		quota, fileShareNum, err := d.GetTotalAccountQuota(context.TODO(), test.subsID, test.resourceGroup, test.accountName)
		assert.Equal(t, test.expectedErr, err, test.name)
		assert.Equal(t, test.expectedQuota, quota, test.name)
		assert.Equal(t, test.expectedShareNum, fileShareNum, test.name)
	}
}

func TestGetStorageEndPointSuffix(t *testing.T) {
	d := NewFakeDriver()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tests := []struct {
		name           string
		cloud          *storage.AccountRepo
		expectedSuffix string
	}{
		{
			name:           "nil cloud",
			cloud:          nil,
			expectedSuffix: "core.windows.net",
		},
		{
			name:           "empty cloud",
			cloud:          &storage.AccountRepo{},
			expectedSuffix: "core.windows.net",
		},
		{
			name: "cloud with storage endpoint suffix",
			cloud: &storage.AccountRepo{
				Environment: &azclient.Environment{
					StorageEndpointSuffix: "suffix",
				},
			},
			expectedSuffix: "suffix",
		},
		{
			name: "public cloud",
			cloud: &storage.AccountRepo{
				Environment: &azclient.Environment{
					StorageEndpointSuffix: "core.windows.net",
				},
			},
			expectedSuffix: "core.windows.net",
		},
		{
			name: "china cloud",
			cloud: &storage.AccountRepo{
				Environment: &azclient.Environment{
					StorageEndpointSuffix: "core.chinacloudapi.cn",
				},
			},
			expectedSuffix: "core.chinacloudapi.cn",
		},
	}

	for _, test := range tests {
		d.cloud = test.cloud
		suffix := d.getStorageEndPointSuffix()
		assert.Equal(t, test.expectedSuffix, suffix, test.name)
	}
}

func TestGetStorageAccesskey(t *testing.T) {
	options := &storage.AccountOptions{
		Name:           "test-sa",
		SubscriptionID: "test-subID",
		ResourceGroup:  "test-rg",
	}
	fakeAccName := options.Name
	fakeAccKey := "test-key"
	secretNamespace := "test-ns"
	testCases := []struct {
		name          string
		secrets       map[string]string
		secretName    string
		expectedError error
	}{
		{
			name:          "error is not nil", // test case should run first to avoid cache hit
			secrets:       make(map[string]string),
			secretName:    "foobar",
			expectedError: errors.New(""),
		},
		{
			name: "Secrets is larger than 0",
			secrets: map[string]string{
				"accountName":              fakeAccName,
				"accountNameField":         fakeAccName,
				"defaultSecretAccountName": fakeAccName,
				"accountKey":               fakeAccKey,
				"accountKeyField":          fakeAccKey,
				"defaultSecretAccountKey":  fakeAccKey,
			},
			expectedError: nil,
		},
		{
			name:          "secretName is Empty",
			secrets:       make(map[string]string),
			secretName:    "",
			expectedError: nil,
		},
		{
			name:          "successful input/error is nil",
			secrets:       make(map[string]string),
			secretName:    fmt.Sprintf(secretNameTemplate, options.Name),
			expectedError: nil,
		},
	}
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}
	d.kubeClient = fake.NewSimpleClientset()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
	d.cloud.ComputeClientFactory = mock_azclient.NewMockClientFactory(gomock.NewController(t))
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClient().Return(mockStorageAccountsClient).AnyTimes()
	accountListKeysResult := []*armstorage.AccountKey{}
	mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(accountListKeysResult, nil).AnyTimes()
	d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClientForSub(gomock.Any()).Return(mockStorageAccountsClient, nil).AnyTimes()
	secret := &v1api.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: secretNamespace,
			Name:      fmt.Sprintf(secretNameTemplate, options.Name),
		},
		Data: map[string][]byte{
			defaultSecretAccountName: []byte(fakeAccName),
			defaultSecretAccountKey:  []byte(fakeAccKey),
		},
		Type: "Opaque",
	}
	secret.Namespace = secretNamespace
	_, secretCreateErr := d.kubeClient.CoreV1().Secrets(secretNamespace).Create(context.TODO(), secret, metav1.CreateOptions{})
	if secretCreateErr != nil {
		t.Error("failed to create secret")
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			accKey, err := d.GetStorageAccesskey(context.TODO(), options, tc.secrets, tc.secretName, secretNamespace)
			if tc.expectedError != nil {
				assert.Error(t, err, "there should be an error")
			} else {
				assert.Equal(t, nil, err, "error should be nil")
				assert.Equal(t, fakeAccKey, accKey, "account keys must match")
			}
		})
	}
}

func TestGetStorageAccesskeyWithSubsID(t *testing.T) {
	testCases := []struct {
		name          string
		expectedError error
	}{
		{
			name:          "Get storage access key error with cloud is nil",
			expectedError: fmt.Errorf("could not get account key: cloud or ComputeClientFactory is nil"),
		},
		{
			name:          "Get storage access key error with ComputeClientFactory is nil",
			expectedError: fmt.Errorf("could not get account key: cloud or ComputeClientFactory is nil"),
		},
		{
			name:          "Get storage access key successfully",
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		d := NewFakeDriver()
		d.cloud = &storage.AccountRepo{}
		if !strings.Contains(tc.name, "is nil") {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockStorageAccountsClient := mock_accountclient.NewMockInterface(ctrl)
			d.cloud.ComputeClientFactory = mock_azclient.NewMockClientFactory(ctrl)
			d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClient().Return(mockStorageAccountsClient).AnyTimes()
			s := "unit-test"
			accountkey := armstorage.AccountKey{Value: &s}
			mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return([]*armstorage.AccountKey{&accountkey}, tc.expectedError).AnyTimes()
			d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetAccountClientForSub(gomock.Any()).Return(mockStorageAccountsClient, nil).AnyTimes()
		}
		_, err := d.GetStorageAccesskeyWithSubsID(context.TODO(), "test-subID", "test-rg", "test-sa", true)
		assert.Equal(t, tc.expectedError, err)
	}
}

func TestGetFileShareClientForSub(t *testing.T) {
	testCases := []struct {
		name          string
		expectedError error
	}{
		{
			name:          "Get file share client error with cloud is nil",
			expectedError: fmt.Errorf("cloud or ComputeClientFactory is nil"),
		},
		{
			name:          "Get file share client error with ComputeClientFactory is nil",
			expectedError: fmt.Errorf("cloud or ComputeClientFactory is nil"),
		},
		{
			name:          "Get file share client successfully",
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		d := NewFakeDriver()
		d.cloud = &storage.AccountRepo{}
		if !strings.Contains(tc.name, "is nil") {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockFileClient := mock_fileshareclient.NewMockInterface(ctrl)
			d.cloud.ComputeClientFactory = mock_azclient.NewMockClientFactory(ctrl)
			d.cloud.ComputeClientFactory.(*mock_azclient.MockClientFactory).EXPECT().GetFileShareClientForSub(gomock.Any()).Return(mockFileClient, tc.expectedError).AnyTimes()
		}
		_, err := d.getFileShareClientForSub("test-subID")
		assert.Equal(t, tc.expectedError, err)
	}
}

func TestGetNodeInfoFromLabels(t *testing.T) {
	testCases := []struct {
		name         string
		nodeName     string
		labels       map[string]string
		setupClient  bool
		expectedVals [3]string
		expectedErr  error
	}{
		{
			name:        "Error when kubeClient is nil",
			nodeName:    "test-node",
			setupClient: false,
			expectedErr: fmt.Errorf("kubeClient is nil"),
		},
		{
			name:        "Error when node does not exist",
			nodeName:    "nonexistent-node",
			setupClient: true,
			expectedErr: fmt.Errorf("get node(nonexistent-node) failed with nodes \"nonexistent-node\" not found"),
		},
		{
			name:        "Error when node has no labels",
			nodeName:    "test-node",
			setupClient: true,
			labels:      map[string]string{}, // Node exists but has no labels
			expectedErr: fmt.Errorf("node(test-node) label is empty"),
		},
		{
			name:        "Success with kata labels",
			nodeName:    "test-node",
			setupClient: true,
			labels: map[string]string{
				"kubernetes.azure.com/kata-cc-isolation":      "true",
				"kubernetes.azure.com/kata-mshv-vm-isolation": "true",
				"katacontainers.io/kata-runtime":              "false",
			},
			expectedVals: [3]string{"true", "true", "false"},
			expectedErr:  nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			var clientset kubernetes.Interface

			if tc.setupClient {
				clientset = fake.NewSimpleClientset()
			}

			if tc.labels != nil && tc.setupClient {
				node := &v1api.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   tc.nodeName,
						Labels: tc.labels,
					},
				}
				_, err := clientset.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			kataCCIsolation, kataVMIsolation, kataRuntime, err := getNodeInfoFromLabels(ctx, tc.nodeName, clientset)

			if tc.expectedErr != nil {
				assert.EqualError(t, err, tc.expectedErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedVals[0], kataCCIsolation)
				assert.Equal(t, tc.expectedVals[1], kataVMIsolation)
				assert.Equal(t, tc.expectedVals[2], kataRuntime)
			}
		})
	}
}

func TestIsKataNode(t *testing.T) {
	testCases := []struct {
		name        string
		nodeName    string
		labels      map[string]string
		setupClient bool
		expected    bool
	}{
		{
			name:        "Node does not exist",
			nodeName:    "",
			setupClient: true,
			expected:    false,
		},
		{
			name:        "Node exists but has no kata labels",
			nodeName:    "test-node",
			setupClient: true,
			labels: map[string]string{
				"some-other-label": "value",
			},
			expected: false,
		},
		{
			name:        "Node has kata labels",
			nodeName:    "test-node",
			setupClient: true,
			labels: map[string]string{
				"kubernetes.azure.com/kata-cc-isolation": "true",
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			var clientset kubernetes.Interface

			if tc.setupClient {
				clientset = fake.NewSimpleClientset()
			}

			if tc.labels != nil && tc.setupClient {
				node := &v1api.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   tc.nodeName,
						Labels: tc.labels,
					},
				}
				_, err := clientset.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			result := isKataNode(ctx, tc.nodeName, clientset)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestUseDataPlaneAPI(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tests := []struct {
		name                     string
		volumeID                 string
		accountName              string
		dataPlaneAPIVolMap       map[string]string
		dataPlaneAPIAccountCache map[string]string
		expectedResult           string
	}{
		{
			name:                     "dataPlaneAPIVolMap & dataPlaneAPIAccountCache is empty",
			dataPlaneAPIVolMap:       make(map[string]string),
			dataPlaneAPIAccountCache: make(map[string]string),
			expectedResult:           "",
		},
		{
			name:                     "dataPlaneAPIVolMap is not empty",
			volumeID:                 "test-volume",
			dataPlaneAPIVolMap:       map[string]string{"test-volume": "true"},
			dataPlaneAPIAccountCache: make(map[string]string),
			expectedResult:           "true",
		},
		{
			name:                     "dataPlaneAPIAccountCache is not empty",
			accountName:              "test-account",
			dataPlaneAPIVolMap:       make(map[string]string),
			dataPlaneAPIAccountCache: map[string]string{"test-account": "oatuh"},
			expectedResult:           "oatuh",
		},
		{
			name:                     "dataPlaneAPIVolMap & dataPlaneAPIAccountCache is not empty",
			volumeID:                 "test-volume",
			accountName:              "test-account",
			dataPlaneAPIVolMap:       map[string]string{"test-volume": "true"},
			dataPlaneAPIAccountCache: map[string]string{"test-account": "oatuh"},
			expectedResult:           "true",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataPlaneAPIAccountCache, _ := cache.NewTimedCache(10*time.Minute, func(_ context.Context, _ string) (interface{}, error) { return nil, nil }, false)
			for k, v := range test.dataPlaneAPIVolMap {
				d.dataPlaneAPIVolMap.Store(k, v)
			}
			for k, v := range test.dataPlaneAPIAccountCache {
				dataPlaneAPIAccountCache.Set(k, v)
			}
			d.dataPlaneAPIAccountCache = dataPlaneAPIAccountCache
			result := d.useDataPlaneAPI(context.TODO(), test.volumeID, test.accountName)
			assert.Equal(t, test.expectedResult, result)
		})
	}
}

func TestSetAzureCredentials(t *testing.T) {
	testCases := []struct {
		name               string
		secretName         string
		seceretNamespace   string
		accountName        string
		accountKey         string
		expectedError      error
		expectedSecretName string
	}{
		{
			name:          "kubeClient is nil",
			accountName:   "test-account",
			accountKey:    "test-key",
			expectedError: nil,
		},
		{
			name:          "accountName is empty",
			expectedError: fmt.Errorf("the account info is not enough, accountName(%v), accountKey(%v)", "", ""),
		},
		{
			name:          "accountKey is empty",
			expectedError: fmt.Errorf("the account info is not enough, accountName(%v), accountKey(%v)", "", ""),
		},
		{
			name:               "success when secretName is empty",
			accountName:        "test-account",
			accountKey:         "test-key",
			expectedError:      nil,
			expectedSecretName: fmt.Sprintf(secretNameTemplate, "test-account"),
		},
		{
			name:               "success when secretName is not empty",
			secretName:         "test-secret",
			accountName:        "test-account",
			accountKey:         "test-key",
			expectedError:      nil,
			expectedSecretName: "test-secret",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewFakeDriver()
			if tc.name == "kubeClient is nil" {
				d.kubeClient = nil
			} else {
				d.kubeClient = fake.NewSimpleClientset()
			}
			secretName, err := d.SetAzureCredentials(context.TODO(), tc.accountName, tc.accountKey, tc.secretName, tc.seceretNamespace)
			assert.Equal(t, tc.expectedError, err)
			assert.Equal(t, tc.expectedSecretName, secretName)
		})
	}
}

func TestIsSupportedPublicNetworkAccess(t *testing.T) {
	tests := []struct {
		publicNetworkAccess string
		expectedResult      bool
	}{
		{
			publicNetworkAccess: "",
			expectedResult:      true,
		},
		{
			publicNetworkAccess: "Enabled",
			expectedResult:      true,
		},
		{
			publicNetworkAccess: "Disabled",
			expectedResult:      true,
		},
		{
			publicNetworkAccess: "InvalidValue",
			expectedResult:      false,
		},
	}

	for _, test := range tests {
		result := isSupportedPublicNetworkAccess(test.publicNetworkAccess)
		if result != test.expectedResult {
			t.Errorf("isSupportedPublicNetworkAccess(%s) returned %v, expected %v", test.publicNetworkAccess, result, test.expectedResult)
		}
	}
}
