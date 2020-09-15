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
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2019-06-01/storage"
	azure2 "github.com/Azure/go-autorest/autorest/azure"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/legacy-cloud-providers/azure"
	"k8s.io/legacy-cloud-providers/azure/clients/fileclient/mockfileclient"
	"k8s.io/legacy-cloud-providers/azure/clients/storageaccountclient/mockstorageaccountclient"
	csicommon "sigs.k8s.io/azurefile-csi-driver/pkg/csi-common"
)

const (
	fakeNodeID     = "fakeNodeID"
	fakeDriverName = "fake"
)

var (
	vendorVersion = "0.3.0"
)

func NewFakeDriver() *Driver {
	driver := NewDriver(fakeNodeID)
	driver.Name = fakeDriverName
	driver.Version = vendorVersion
	return driver
}

func TestNewFakeDriver(t *testing.T) {
	d := NewDriver(fakeNodeID)
	assert.NotNil(t, d)
}

func TestAppendDefaultMountOptions(t *testing.T) {
	tests := []struct {
		options  []string
		expected []string
	}{
		{
			options: []string{"dir_mode=0777"},
			expected: []string{"dir_mode=0777",
				fmt.Sprintf("%s=%s", fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", vers, defaultVers)},
		},
		{
			options: []string{"file_mode=0777"},
			expected: []string{"file_mode=0777",
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				fmt.Sprintf("%s=%s", vers, defaultVers)},
		},
		{
			options: []string{"vers=2.1"},
			expected: []string{"vers=2.1",
				fmt.Sprintf("%s=%s", fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode)},
		},
		{
			options: []string{""},
			expected: []string{"", fmt.Sprintf("%s=%s",
				fileMode, defaultFileMode),
				fmt.Sprintf("%s=%s", dirMode, defaultDirMode),
				fmt.Sprintf("%s=%s", vers, defaultVers)},
		},
		{
			options:  []string{"file_mode=0777", "dir_mode=0777"},
			expected: []string{"file_mode=0777", "dir_mode=0777", fmt.Sprintf("%s=%s", vers, defaultVers)},
		},
	}

	for _, test := range tests {
		result := appendDefaultMountOptions(test.options)
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
		expectedError     error
	}{
		{
			id:                "rg#f5713de20cde511e8ba4900#pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41#diskname.vhd",
			resourceGroupName: "rg",
			accountName:       "f5713de20cde511e8ba4900",
			fileShareName:     "pvc-file-dynamic-17e43f84-f474-11e8-acd0-000d3a00df41",
			diskName:          "diskname.vhd",
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
	}

	for _, test := range tests {
		resourceGroupName, accountName, fileShareName, diskName, expectedError := GetFileShareInfo(test.id)
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
		"azurestorageaccountname": "",
		"azurestorageaccountkey":  "testkey",
	}

	emptyAzureAccountKeyMap := map[string]string{
		"azurestorageaccountname": "testaccount",
		"azurestorageaccountkey":  "",
	}

	emptyAzureAccountNameMap := map[string]string{
		"azurestorageaccountname": "",
		"azurestorageaccountkey":  "testkey",
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
				"azurestorageaccountname": "testaccount",
				"azurestorageaccountkey":  "testkey",
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
	existingMountPath, err := ioutil.TempDir(os.TempDir(), "csi-mount-test")
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
		result := NewDriver(test.nodeID)
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
		"azurestorageaccountname": "testaccount",
		"azurestorageaccountkey":  "testkey",
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
		volumeID   string
		resGroup   string
		secrets    map[string]string
		reqContext map[string]string
		expectErr  bool
		err        error
	}{
		{
			volumeID: "##",
			resGroup: "",
			secrets:  emptySecret,
			reqContext: map[string]string{
				shareNameField: "test_sharename",
				diskNameField:  "test_diskname",
			},
			expectErr: false,
			err:       nil,
		},
		{
			volumeID: "vol_1##",
			resGroup: "vol_1",
			secrets:  validSecret,
			reqContext: map[string]string{
				shareNameField: "test_sharename",
				diskNameField:  "test_diskname",
			},
			expectErr: false,
			err:       nil,
		},
		{
			volumeID: "vol_1##",
			resGroup: "vol_1",
			secrets:  emptySecret,
			reqContext: map[string]string{
				shareNameField: "test_sharename",
				diskNameField:  "test_diskname",
			},
			expectErr: false,
			err:       nil,
		},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		d.cloud.StorageAccountClient = mockStorageAccountsClient
		d.cloud.KubeClient = clientSet
		d.cloud.Environment = azure2.Environment{StorageEndpointSuffix: "abc"}
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), test.resGroup, gomock.Any()).Return(key, nil).AnyTimes()
		_, _, _, _, _, err := d.GetAccountInfo(test.volumeID, test.secrets, test.reqContext)
		if test.expectErr && err == nil {
			t.Errorf("Unexpected non-error")
			continue
		}
		if !test.expectErr && err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
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
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	shareQuota := int32(10)
	resourceGroupName := "rg"
	accountName := "accountname"
	fileShareName := "filesharename"

	tests := []struct {
		desc                string
		mockedFileShareResp storage.FileShare
		mockedFileShareErr  error
		expectedQuota       int
		expectedError       error
	}{
		{
			desc:                "Get file share return error",
			mockedFileShareResp: storage.FileShare{},
			mockedFileShareErr:  fmt.Errorf("test error"),
			expectedQuota:       -1,
			expectedError:       fmt.Errorf("test error"),
		},
		{
			desc:                "Share not found",
			mockedFileShareResp: storage.FileShare{},
			mockedFileShareErr:  fmt.Errorf("ShareNotFound"),
			expectedQuota:       -1,
			expectedError:       nil,
		},
		{
			desc:                "Volume already exists",
			mockedFileShareResp: storage.FileShare{FileShareProperties: &storage.FileShareProperties{ShareQuota: &shareQuota}},
			mockedFileShareErr:  nil,
			expectedQuota:       int(shareQuota),
			expectedError:       nil,
		},
	}

	for _, test := range tests {
		mockFileClient := mockfileclient.NewMockInterface(ctrl)
		d.cloud.FileClient = mockFileClient
		mockFileClient.EXPECT().GetFileShare(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockedFileShareResp, test.mockedFileShareErr).AnyTimes()
		quota, err := d.getFileShareQuota(resourceGroupName, accountName, fileShareName, map[string]string{})
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
				if err := ioutil.WriteFile(fakeCredFile, []byte(fakeCredContent), 0666); err != nil {
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
				if err := ioutil.WriteFile(fakeCredFile, []byte(fakeCredContent), 0666); err != nil {
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

func TestUtilsRunNodePublishServer(t *testing.T) {
	d := NewFakeDriver()
	csicommon.RunNodePublishServer("tcp://127.0.0.1:0", &d.CSIDriver, d, true)
}

func TestUtilsRunControllerandNodePublishServer(t *testing.T) {
	d := NewFakeDriver()
	csicommon.RunControllerandNodePublishServer("tcp://127.0.0.1:0", &d.CSIDriver, d, d, true)
}

func TestUtilsRunControllerPublishServer(t *testing.T) {
	d := NewFakeDriver()
	csicommon.RunControllerPublishServer("tcp://127.0.0.1:0", &d.CSIDriver, d, true)
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
