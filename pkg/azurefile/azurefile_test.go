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
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2021-09-01/storage"
	azure2 "github.com/Azure/go-autorest/autorest/azure"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/kubernetes/fake"

	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient/mockfileclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/storageaccountclient/mockstorageaccountclient"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
	auth "sigs.k8s.io/cloud-provider-azure/pkg/provider/config"
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
		NodeID:     fakeNodeID,
		DriverName: DefaultDriverName,
	}
	driver := NewDriver(&driverOptions)
	driver.Name = fakeDriverName
	driver.Version = vendorVersion
	driver.cloud = &azure.Cloud{
		Config: azure.Config{
			AzureAuthConfig: auth.AzureAuthConfig{
				SubscriptionID: "subscriptionID",
			},
		},
	}
	return driver
}

func NewFakeDriverCustomOptions(opts DriverOptions) *Driver {
	driverOptions := opts
	driver := NewDriver(&driverOptions)
	driver.Name = fakeDriverName
	driver.Version = vendorVersion
	driver.cloud = &azure.Cloud{
		Config: azure.Config{
			AzureAuthConfig: auth.AzureAuthConfig{
				SubscriptionID: "subscriptionID",
			},
		},
	}
	return driver
}

func TestNewFakeDriver(t *testing.T) {
	driverOptions := DriverOptions{
		NodeID:     fakeNodeID,
		DriverName: DefaultDriverName,
	}
	d := NewDriver(&driverOptions)
	assert.NotNil(t, d)
}

func TestAppendDefaultMountOptions(t *testing.T) {
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
		result := appendDefaultMountOptions(test.options, test.appendNoShareSockOption, test.appendClosetimeoOption)
		sort.Strings(result)
		sort.Strings(test.expected)

		if !reflect.DeepEqual(result, test.expected) {
			t.Errorf("input: %q, appendDefaultMountOptions result: %q, expected: %q", test.options, result, test.expected)
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
			expected3: fmt.Errorf("could not find accountname or azurestorageaccountname field secrets(map[accountname: accountkey:])"),
		},
		{
			options:   emptyAccountKeyMap,
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("could not find accountkey or azurestorageaccountkey field in secrets(%v)", emptyAccountKeyMap),
		},
		{
			options:   emptyAccountNameMap,
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("could not find accountname or azurestorageaccountname field secrets(%v)", emptyAccountNameMap),
		},
		{
			options:   emptyAzureAccountKeyMap,
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("could not find accountkey or azurestorageaccountkey field in secrets(%v)", emptyAzureAccountKeyMap),
		},
		{
			options:   emptyAzureAccountNameMap,
			expected1: "",
			expected2: "",
			expected3: fmt.Errorf("could not find accountname or azurestorageaccountname field secrets(%v)", emptyAzureAccountNameMap),
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
			expectedError:         fmt.Errorf("NewSharedKeyCredential(f5713de20cde511e8ba4900) failed with error: illegal base64 data at input byte 0"),
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
		_, err := getFileURL(test.accountName, test.accountKey, test.storageEndpointSuffix, test.fileShareName, test.diskName)
		if !reflect.DeepEqual(err, test.expectedError) {
			t.Errorf("accountName: %v accountKey: %v storageEndpointSuffix: %v fileShareName: %v diskName: %v Error: %v",
				test.accountName, test.accountKey, test.storageEndpointSuffix, test.fileShareName, test.diskName, err)
		}
	}
}

func TestGetAccountInfo(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &azure.Cloud{}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	validSecret := map[string]string{
		defaultSecretAccountName: "testaccount",
		defaultSecretAccountKey:  "testkey",
	}
	emptySecret := map[string]string{}
	value := base64.StdEncoding.EncodeToString([]byte("acc_key"))
	key := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &value},
		},
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
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), test.rgName, gomock.Any()).Return(key, nil).AnyTimes()
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
	d.cloud = &azure.Cloud{}
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
	d.cloud = &azure.Cloud{}
	d.fileClient = &azureFileClient{env: &azure2.Environment{}}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	shareQuota := int32(10)
	resourceGroupName := "rg"
	accountName := "accountname"
	fileShareName := "filesharename"

	tests := []struct {
		desc                string
		secrets             map[string]string
		mockedFileShareResp storage.FileShare
		mockedFileShareErr  error
		expectedQuota       int
		expectedError       error
	}{
		{
			desc:                "Get file share return error",
			secrets:             map[string]string{},
			mockedFileShareResp: storage.FileShare{},
			mockedFileShareErr:  fmt.Errorf("test error"),
			expectedQuota:       -1,
			expectedError:       fmt.Errorf("test error"),
		},
		{
			desc:                "Share not found",
			secrets:             map[string]string{},
			mockedFileShareResp: storage.FileShare{},
			mockedFileShareErr:  fmt.Errorf("ShareNotFound"),
			expectedQuota:       -1,
			expectedError:       nil,
		},
		{
			desc:                "Volume already exists",
			secrets:             map[string]string{},
			mockedFileShareResp: storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &shareQuota}},
			mockedFileShareErr:  nil,
			expectedQuota:       int(shareQuota),
			expectedError:       nil,
		},
		{
			desc: "Could not find accountname in secrets",
			secrets: map[string]string{
				"secrets": "secrets",
			},
			mockedFileShareResp: storage.FileShare{},
			mockedFileShareErr:  nil,
			expectedQuota:       -1,
			expectedError:       fmt.Errorf("could not find accountname or azurestorageaccountname field secrets(map[secrets:secrets])"),
		},
		{
			desc: "Error creating azure client",
			secrets: map[string]string{
				"accountname": "ut",
				"accountkey":  "testkey",
			},
			mockedFileShareResp: storage.FileShare{},
			mockedFileShareErr:  nil,
			expectedQuota:       -1,
			expectedError:       fmt.Errorf("error creating azure client: azure: account name is not valid: it must be between 3 and 24 characters, and only may contain numbers and lowercase letters: ut"),
		},
	}

	for _, test := range tests {
		mockFileClient := mockfileclient.NewMockInterface(ctrl)
		d.cloud.FileClient = mockFileClient
		mockFileClient.EXPECT().GetFileShare(context.TODO(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockedFileShareResp, test.mockedFileShareErr).AnyTimes()
		mockFileClient.EXPECT().WithSubscriptionID(gomock.Any()).Return(mockFileClient).AnyTimes()
		quota, err := d.getFileShareQuota(context.TODO(), "", resourceGroupName, accountName, fileShareName, test.secrets)
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
				d.Run("tcp://127.0.0.1:0", "", true)
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
				d.cloud = &azure.Cloud{}
				d.NodeID = ""
				d.Run("tcp://127.0.0.1:0", "", true)
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
				d.cloud = &azure.Cloud{}
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
				d.cloud = &azure.Cloud{}
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
				d.cloud = &azure.Cloud{}
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
				d.cloud = &azure.Cloud{}
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
				d.cloud = &azure.Cloud{}
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
