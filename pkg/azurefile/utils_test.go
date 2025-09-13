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
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	fake "k8s.io/client-go/kubernetes/fake"
	utiltesting "k8s.io/client-go/util/testing"
	"k8s.io/kubernetes/pkg/volume"
	azureconfig "sigs.k8s.io/cloud-provider-azure/pkg/provider/config"
)

func TestSimpleLockEntry(t *testing.T) {
	testLockMap := newLockMap()

	callbackChan1 := make(chan interface{})
	go testLockMap.lockAndCallback(t, "entry1", callbackChan1)
	ensureCallbackHappens(t, callbackChan1)
}

func TestSimpleLockUnlockEntry(t *testing.T) {
	testLockMap := newLockMap()

	callbackChan1 := make(chan interface{})
	go testLockMap.lockAndCallback(t, "entry1", callbackChan1)
	ensureCallbackHappens(t, callbackChan1)
	testLockMap.UnlockEntry("entry1")
}

func TestConcurrentLockEntry(t *testing.T) {
	testLockMap := newLockMap()

	callbackChan1 := make(chan interface{})
	callbackChan2 := make(chan interface{})

	go testLockMap.lockAndCallback(t, "entry1", callbackChan1)
	ensureCallbackHappens(t, callbackChan1)

	go testLockMap.lockAndCallback(t, "entry1", callbackChan2)
	ensureNoCallback(t, callbackChan2)

	testLockMap.UnlockEntry("entry1")
	ensureCallbackHappens(t, callbackChan2)
	testLockMap.UnlockEntry("entry1")
}

func (lm *lockMap) lockAndCallback(_ *testing.T, entry string, callbackChan chan<- interface{}) {
	lm.LockEntry(entry)
	callbackChan <- true
}

var callbackTimeout = 2 * time.Second

func ensureCallbackHappens(t *testing.T, callbackChan <-chan interface{}) bool {
	select {
	case <-callbackChan:
		return true
	case <-time.After(callbackTimeout):
		t.Fatalf("timed out waiting for callback")
		return false
	}
}

func ensureNoCallback(t *testing.T, callbackChan <-chan interface{}) bool {
	select {
	case <-callbackChan:
		t.Fatalf("unexpected callback")
		return false
	case <-time.After(callbackTimeout):
		return true
	}
}

func TestUnlockEntryNotExists(t *testing.T) {
	testLockMap := newLockMap()

	callbackChan1 := make(chan interface{})
	go testLockMap.lockAndCallback(t, "entry1", callbackChan1)
	ensureCallbackHappens(t, callbackChan1)
	// entry2 does not exist
	testLockMap.UnlockEntry("entry2")
	testLockMap.UnlockEntry("entry1")
}

func TestIsDiskFsType(t *testing.T) {
	tests := []struct {
		fsType         string
		expectedResult bool
	}{
		{
			fsType:         "ext4",
			expectedResult: true,
		},
		{
			fsType:         "ext3",
			expectedResult: true,
		},
		{
			fsType:         "ext2",
			expectedResult: true,
		},
		{
			fsType:         "xfs",
			expectedResult: true,
		},
		{
			fsType:         "",
			expectedResult: false,
		},
		{
			fsType:         "cifs",
			expectedResult: false,
		},
		{
			fsType:         "invalid",
			expectedResult: false,
		},
	}

	for _, test := range tests {
		result := isDiskFsType(test.fsType)
		if result != test.expectedResult {
			t.Errorf("isDiskFsType(%s) returned with %v, not equal to %v", test.fsType, result, test.expectedResult)
		}
	}
}

func TestIsSupportedShareNamePrefix(t *testing.T) {
	tests := []struct {
		prefix         string
		expectedResult bool
	}{
		{
			prefix:         "",
			expectedResult: true,
		},
		{
			prefix:         "ext3",
			expectedResult: true,
		},
		{
			prefix:         "ext-2",
			expectedResult: true,
		},
		{
			prefix:         "-xfs",
			expectedResult: false,
		},
		{
			prefix:         "Absdf",
			expectedResult: false,
		},
		{
			prefix:         "tooooooooooooooooooooooooolong",
			expectedResult: false,
		},
		{
			prefix:         "+invalid",
			expectedResult: false,
		},
		{
			prefix:         " invalidspace",
			expectedResult: false,
		},
	}

	for _, test := range tests {
		result := isSupportedShareNamePrefix(test.prefix)
		if result != test.expectedResult {
			t.Errorf("isSupportedShareNamePrefix(%s) returned with %v, not equal to %v", test.prefix, result, test.expectedResult)
		}
	}
}

func TestIsSupportedFsType(t *testing.T) {
	tests := []struct {
		fsType         string
		expectedResult bool
	}{
		{
			fsType:         "ext4",
			expectedResult: true,
		},
		{
			fsType:         "ext3",
			expectedResult: true,
		},
		{
			fsType:         "ext2",
			expectedResult: true,
		},
		{
			fsType:         "xfs",
			expectedResult: true,
		},
		{
			fsType:         "",
			expectedResult: true,
		},
		{
			fsType:         "cifs",
			expectedResult: true,
		},
		{
			fsType:         "smb",
			expectedResult: true,
		},
		{
			fsType:         "invalid",
			expectedResult: false,
		},
	}

	for _, test := range tests {
		result := isSupportedFsType(test.fsType)
		if result != test.expectedResult {
			t.Errorf("isSupportedFsType(%s) returned with %v, not equal to %v", test.fsType, result, test.expectedResult)
		}
	}
}

func TestIsSupportedFSGroupChangePolicy(t *testing.T) {
	tests := []struct {
		policy         string
		expectedResult bool
	}{
		{
			policy:         "",
			expectedResult: true,
		},
		{
			policy:         "None",
			expectedResult: true,
		},
		{
			policy:         "Always",
			expectedResult: true,
		},
		{
			policy:         "OnRootMismatch",
			expectedResult: true,
		},
		{
			policy:         "onRootMismatch",
			expectedResult: false,
		},
		{
			policy:         "invalid",
			expectedResult: false,
		},
	}

	for _, test := range tests {
		result := isSupportedFSGroupChangePolicy(test.policy)
		if result != test.expectedResult {
			t.Errorf("isSupportedFSGroupChangePolicy(%s) returned with %v, not equal to %v", test.policy, result, test.expectedResult)
		}
	}
}

func TestIsRetriableError(t *testing.T) {
	tests := []struct {
		desc         string
		rpcErr       error
		expectedBool bool
	}{
		{
			desc:         "non-retriable error",
			rpcErr:       nil,
			expectedBool: false,
		},
		{
			desc:         "accountNotProvisioned",
			rpcErr:       errors.New("could not get storage key for storage account : could not get storage key for storage account f233333: Retriable: true, RetryAfter: 0001-01-01 00:00:00 +0000 UTC, HTTPStatusCode: 409, RawError: storage.AccountsClient#ListKeys: Failure sending request: StatusCode=409 -- Original Error: autorest/azure: Service returned an error. Status=<nil> Code=\"StorageAccountIsNotProvisioned\" Message=\"The storage account provisioning state must be 'Succeeded' before executing the operation.\""),
			expectedBool: true,
		},
		{
			desc:         "tooManyRequests",
			rpcErr:       errors.New("could not get storage key for storage account : could not list storage accounts for account type Premium_LRS: Retriable: true, RetryAfter: 0001-01-01 00:00:00 +0000 UTC m=+231.866923225, HTTPStatusCode: 429, RawError: storage.AccountsClient#ListByResourceGroup: Failure responding to request: StatusCode=429 -- Original Error: autorest/azure: Service returned an error. Status=429 Code=\"TooManyRequests\" Message=\"The request is being throttled as the limit has been reached for operation type - List. For more information, see - https://aka.ms/srpthrottlinglimits\""),
			expectedBool: true,
		},
		{
			desc:         "shareBeingDeleted",
			rpcErr:       errors.New("storage.FileSharesClient#Create: Failure sending request: StatusCode=409 -- Original Error: autorest/azure: Service returned an error. Status=<nil> Code=\"ShareBeingDeleted\" Message=\"The specified share is being deleted. Try operation later.\""),
			expectedBool: true,
		},
		{
			desc:         "clientThrottled",
			rpcErr:       errors.New("could not list storage accounts for account type : Retriable: true, RetryAfter: 16s, HTTPStatusCode: 0, RawError: azure cloud provider throttled for operation StorageAccountListByResourceGroup with reason \"client throttled\""),
			expectedBool: true,
		},
	}

	for _, test := range tests {
		result := isRetriableError(test.rpcErr)
		if result != test.expectedBool {
			t.Errorf("desc: (%s), input: rpcErr(%v), isRetriableError returned with bool(%v), not equal to expectedBool(%v)",
				test.desc, test.rpcErr, result, test.expectedBool)
		}
	}
}

func TestSleepIfThrottled(t *testing.T) {
	start := time.Now()
	sleepIfThrottled(errors.New("tooManyRequests"), 10)
	elapsed := time.Since(start)
	if elapsed.Seconds() < 10 {
		t.Errorf("Expected sleep time(%d), Actual sleep time(%f)", 10, elapsed.Seconds())
	}
}

func TestGetRetryAfterSeconds(t *testing.T) {
	tests := []struct {
		desc     string
		err      error
		expected int
	}{
		{
			desc:     "nil error",
			err:      nil,
			expected: 0,
		},
		{
			desc:     "no match",
			err:      errors.New("no match"),
			expected: 0,
		},
		{
			desc:     "match",
			err:      errors.New("RetryAfter: 10s"),
			expected: 10,
		},
		{
			desc:     "match error message",
			err:      errors.New("could not list storage accounts for account type Premium_LRS: Retriable: true, RetryAfter: 217s, HTTPStatusCode: 0, RawError: azure cloud provider throttled for operation StorageAccountListByResourceGroup with reason \"client throttled\""),
			expected: 217,
		},
		{
			desc:     "match error message exceeds 1200s",
			err:      errors.New("could not list storage accounts for account type Premium_LRS: Retriable: true, RetryAfter: 2170s, HTTPStatusCode: 0, RawError: azure cloud provider throttled for operation StorageAccountListByResourceGroup with reason \"client throttled\""),
			expected: maxThrottlingSleepSec,
		},
	}

	for _, test := range tests {
		result := getRetryAfterSeconds(test.err)
		if result != test.expected {
			t.Errorf("desc: (%s), input: err(%v), getRetryAfterSeconds returned with int(%d), not equal to expected(%d)",
				test.desc, test.err, result, test.expected)
		}
	}
}

func TestCreateStorageAccountSecret(t *testing.T) {
	result := createStorageAccountSecret("TestAccountName", "TestAccountKey")
	if result[defaultSecretAccountName] != "TestAccountName" || result[defaultSecretAccountKey] != "TestAccountKey" {
		t.Errorf("Expected account name(%s), Actual account name(%s); Expected account key(%s), Actual account key(%s)", "TestAccountName", result[defaultSecretAccountName], "TestAccountKey", result[defaultSecretAccountKey])
	}
}

func TestConvertTagsToMap(t *testing.T) {
	tests := []struct {
		desc          string
		tags          string
		tagsDelimiter string
		expectedError error
	}{
		{
			desc:          "Invalid tag",
			tags:          "invalid,test,tag",
			tagsDelimiter: ",",
			expectedError: errors.New("Tags 'invalid,test,tag' are invalid, the format should like: 'key1=value1,key2=value2'"),
		},
		{
			desc:          "Invalid key",
			tags:          "=test",
			tagsDelimiter: ",",
			expectedError: errors.New("Tags '=test' are invalid, the format should like: 'key1=value1,key2=value2'"),
		},
		{
			desc:          "Valid tags",
			tags:          "testTag=testValue",
			tagsDelimiter: ",",
			expectedError: nil,
		},
		{
			desc:          "should return success for empty tagsDelimiter",
			tags:          "key1=value1,key2=value2",
			tagsDelimiter: "",
			expectedError: nil,
		},
		{
			desc:          "should return success for special tagsDelimiter and tag values containing commas and equal sign",
			tags:          "key1=aGVsbG8=;key2=value-2, value-3",
			tagsDelimiter: ";",
			expectedError: nil,
		},
	}

	for _, test := range tests {
		_, err := ConvertTagsToMap(test.tags, test.tagsDelimiter)
		if !reflect.DeepEqual(err, test.expectedError) {
			t.Errorf("test[%s]: unexpected error: %v, expected error: %v", test.desc, err, test.expectedError)
		}
	}
}

func TestChmodIfPermissionMismatch(t *testing.T) {
	skipIfTestingOnWindows(t)
	permissionMatchingPath, _ := getWorkDirPath("permissionMatchingPath")
	_ = makeDir(permissionMatchingPath, 0755)
	defer os.RemoveAll(permissionMatchingPath)

	permissionMismatchPath, _ := getWorkDirPath("permissionMismatchPath")
	_ = makeDir(permissionMismatchPath, 0721)
	defer os.RemoveAll(permissionMismatchPath)

	permissionMatchGidMismatchPath, _ := getWorkDirPath("permissionMatchGidMismatchPath")
	_ = makeDir(permissionMatchGidMismatchPath, 0755)
	_ = os.Chmod(permissionMatchGidMismatchPath, 0755|os.ModeSetgid) // Setgid bit is set
	defer os.RemoveAll(permissionMatchGidMismatchPath)

	permissionMismatchGidMismatch, _ := getWorkDirPath("permissionMismatchGidMismatch")
	_ = makeDir(permissionMismatchGidMismatch, 0721)
	_ = os.Chmod(permissionMismatchGidMismatch, 0721|os.ModeSetgid) // Setgid bit is set
	defer os.RemoveAll(permissionMismatchGidMismatch)

	tests := []struct {
		desc           string
		path           string
		mode           os.FileMode
		expectedPerms  os.FileMode
		expectedGidBit bool
		expectedError  error
	}{
		{
			desc:          "Invalid path",
			path:          "invalid-path",
			mode:          0755,
			expectedError: fmt.Errorf("CreateFile invalid-path: The system cannot find the file specified"),
		},
		{
			desc:           "permission matching path",
			path:           permissionMatchingPath,
			mode:           0755,
			expectedPerms:  0755,
			expectedGidBit: false,
			expectedError:  nil,
		},
		{
			desc:           "permission mismatch path",
			path:           permissionMismatchPath,
			mode:           0755,
			expectedPerms:  0755,
			expectedGidBit: false,
			expectedError:  nil,
		},
		{
			desc:           "only match the permission mode bits",
			path:           permissionMatchGidMismatchPath,
			mode:           0755,
			expectedPerms:  0755,
			expectedGidBit: true,
			expectedError:  nil,
		},
		{
			desc:           "only change the permission mode bits when gid is set",
			path:           permissionMismatchGidMismatch,
			mode:           0755,
			expectedPerms:  0755,
			expectedGidBit: true,
			expectedError:  nil,
		},
		{
			desc:           "only change the permission mode bits when gid is not set but mode bits have gid set",
			path:           permissionMismatchPath,
			mode:           02755,
			expectedPerms:  0755,
			expectedGidBit: false,
			expectedError:  nil,
		},
	}

	for _, test := range tests {
		err := chmodIfPermissionMismatch(test.path, test.mode)
		if !reflect.DeepEqual(err, test.expectedError) {
			if err == nil || test.expectedError == nil && !strings.Contains(err.Error(), test.expectedError.Error()) {
				t.Errorf("test[%s]: unexpected error: %v, expected error: %v", test.desc, err, test.expectedError)
			}
		}

		if test.expectedError == nil {
			info, _ := os.Lstat(test.path)
			if test.expectedError == nil && (info.Mode()&os.ModePerm != test.expectedPerms) {
				t.Errorf("test[%s]: unexpected perms: %v, expected perms: %v, ", test.desc, info.Mode()&os.ModePerm, test.expectedPerms)
			}

			if (info.Mode()&os.ModeSetgid != 0) != test.expectedGidBit {
				t.Errorf("test[%s]: unexpected gid bit: %v, expected gid bit: %v", test.desc, info.Mode()&os.ModeSetgid != 0, test.expectedGidBit)
			}
		}

	}
}

// getWorkDirPath returns the path to the current working directory
func getWorkDirPath(dir string) (string, error) {
	path, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%c%s", path, os.PathSeparator, dir), nil
}

func TestSetVolumeOwnership(t *testing.T) {
	tmpVDir, err := utiltesting.MkTmpdir("SetVolumeOwnership")
	if err != nil {
		t.Fatalf("can't make a temp dir: %v", err)
	}
	//deferred clean up
	defer os.RemoveAll(tmpVDir)

	tests := []struct {
		path                string
		gid                 string
		fsGroupChangePolicy string
		expectedError       error
	}{
		{
			path:          "path",
			gid:           "",
			expectedError: fmt.Errorf("convert %s to int failed with %v", "", `strconv.Atoi: parsing "": invalid syntax`),
		},
		{
			path:          "path",
			gid:           "alpha",
			expectedError: fmt.Errorf("convert %s to int failed with %v", "alpha", `strconv.Atoi: parsing "alpha": invalid syntax`),
		},
		{
			path:          "not-exists",
			gid:           "1000",
			expectedError: fmt.Errorf("lstat not-exists: no such file or directory"),
		},
		{
			path:          tmpVDir,
			gid:           "1000",
			expectedError: nil,
		},
		{
			path:                tmpVDir,
			gid:                 "1000",
			fsGroupChangePolicy: "Always",
			expectedError:       nil,
		},
		{
			path:                tmpVDir,
			gid:                 "1000",
			fsGroupChangePolicy: "OnRootMismatch",
			expectedError:       nil,
		},
	}

	for _, test := range tests {
		err := SetVolumeOwnership(test.path, test.gid, test.fsGroupChangePolicy)
		if err != nil && (err.Error() != test.expectedError.Error()) {
			t.Errorf("unexpected error: %v, expected error: %v", err, test.expectedError)
		}
	}
}

func TestSetKeyValueInMap(t *testing.T) {
	tests := []struct {
		desc     string
		m        map[string]string
		key      string
		value    string
		expected map[string]string
	}{
		{
			desc:  "nil map",
			key:   "key",
			value: "value",
		},
		{
			desc:     "empty map",
			m:        map[string]string{},
			key:      "key",
			value:    "value",
			expected: map[string]string{"key": "value"},
		},
		{
			desc:  "non-empty map",
			m:     map[string]string{"k": "v"},
			key:   "key",
			value: "value",
			expected: map[string]string{
				"k":   "v",
				"key": "value",
			},
		},
		{
			desc:     "same key already exists",
			m:        map[string]string{"subDir": "value2"},
			key:      "subDir",
			value:    "value",
			expected: map[string]string{"subDir": "value"},
		},
		{
			desc:     "case insensitive key already exists",
			m:        map[string]string{"subDir": "value2"},
			key:      "subdir",
			value:    "value",
			expected: map[string]string{"subDir": "value"},
		},
	}

	for _, test := range tests {
		setKeyValueInMap(test.m, test.key, test.value)
		if !reflect.DeepEqual(test.m, test.expected) {
			t.Errorf("test[%s]: unexpected output: %v, expected result: %v", test.desc, test.m, test.expected)
		}
	}
}

func TestGetValueInMap(t *testing.T) {
	tests := []struct {
		desc     string
		m        map[string]string
		key      string
		expected string
	}{
		{
			desc:     "nil map",
			key:      "key",
			expected: "",
		},
		{
			desc:     "empty map",
			m:        map[string]string{},
			key:      "key",
			expected: "",
		},
		{
			desc:     "non-empty map",
			m:        map[string]string{"k": "v"},
			key:      "key",
			expected: "",
		},
		{
			desc:     "same key already exists",
			m:        map[string]string{"subDir": "value2"},
			key:      "subDir",
			expected: "value2",
		},
		{
			desc:     "case insensitive key already exists",
			m:        map[string]string{"subDir": "value2"},
			key:      "subdir",
			expected: "value2",
		},
	}

	for _, test := range tests {
		result := getValueInMap(test.m, test.key)
		if result != test.expected {
			t.Errorf("test[%s]: unexpected output: %v, expected result: %v", test.desc, result, test.expected)
		}
	}
}

func TestReplaceWithMap(t *testing.T) {
	tests := []struct {
		desc     string
		str      string
		m        map[string]string
		expected string
	}{
		{
			desc:     "empty string",
			str:      "",
			expected: "",
		},
		{
			desc:     "empty map",
			str:      "",
			m:        map[string]string{},
			expected: "",
		},
		{
			desc:     "empty key",
			str:      "prefix-" + pvNameMetadata,
			m:        map[string]string{"": "pv"},
			expected: "prefix-" + pvNameMetadata,
		},
		{
			desc:     "empty value",
			str:      "prefix-" + pvNameMetadata,
			m:        map[string]string{pvNameMetadata: ""},
			expected: "prefix-",
		},
		{
			desc:     "one replacement",
			str:      "prefix-" + pvNameMetadata,
			m:        map[string]string{pvNameMetadata: "pv"},
			expected: "prefix-pv",
		},
		{
			desc:     "multiple replacements",
			str:      pvcNamespaceMetadata + pvcNameMetadata,
			m:        map[string]string{pvcNamespaceMetadata: "namespace", pvcNameMetadata: "pvcname"},
			expected: "namespacepvcname",
		},
	}

	for _, test := range tests {
		result := replaceWithMap(test.str, test.m)
		if result != test.expected {
			t.Errorf("test[%s]: unexpected output: %v, expected result: %v", test.desc, result, test.expected)
		}
	}
}

func TestIsReadOnlyFromCapability(t *testing.T) {
	testCases := []struct {
		name           string
		vc             *csi.VolumeCapability
		expectedResult bool
	}{
		{
			name:           "false with empty capabilities",
			vc:             &csi.VolumeCapability{},
			expectedResult: false,
		},
		{
			name: "fail with capabilities no access mode",
			vc: &csi.VolumeCapability{
				AccessType: &csi.VolumeCapability_Mount{
					Mount: &csi.VolumeCapability_MountVolume{},
				},
			},
		},
		{
			name: "false with SINGLE_NODE_WRITER capabilities",
			vc: &csi.VolumeCapability{
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
				},
			},
			expectedResult: false,
		},
		{
			name: "true with MULTI_NODE_READER_ONLY capabilities",
			vc: &csi.VolumeCapability{
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
				},
			},
			expectedResult: true,
		},
		{
			name: "true with SINGLE_NODE_READER_ONLY capabilities",
			vc: &csi.VolumeCapability{
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
				},
			},
			expectedResult: true,
		},
	}

	for _, test := range testCases {
		result := isReadOnlyFromCapability(test.vc)
		if result != test.expectedResult {
			t.Errorf("case(%s): isReadOnlyFromCapability returned with %v, not equal to %v", test.name, result, test.expectedResult)
		}
	}
}

func TestIsConfidentialRuntimeClass(t *testing.T) {
	ctx := context.TODO()

	// Test the case where kubeClient is nil
	_, err := isConfidentialRuntimeClass(ctx, nil, "test-runtime-class", defaultRuntimeClassHandler)
	if err == nil || err.Error() != "kubeClient is nil" {
		t.Fatalf("expected error 'kubeClient is nil', got %v", err)
	}

	// Create a fake clientset
	clientset := fake.NewSimpleClientset()

	// Test the case where the runtime class exists and has the confidential handler
	runtimeClass := &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-runtime-class",
		},
		Handler: defaultRuntimeClassHandler,
	}
	_, err = clientset.NodeV1().RuntimeClasses().Create(ctx, runtimeClass, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	isConfidential, err := isConfidentialRuntimeClass(ctx, clientset, "test-runtime-class", defaultRuntimeClassHandler)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !isConfidential {
		t.Fatalf("expected runtime class to be confidential, got %v", isConfidential)
	}

	// Test the case where the runtime class exists but does not have the confidential handler
	nonConfidentialRuntimeClass := &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-runtime-class-non-confidential",
		},
		Handler: "non-confidential-handler",
	}
	_, err = clientset.NodeV1().RuntimeClasses().Create(ctx, nonConfidentialRuntimeClass, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	isConfidential, err = isConfidentialRuntimeClass(ctx, clientset, "test-runtime-class-non-confidential", defaultRuntimeClassHandler)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if isConfidential {
		t.Fatalf("expected runtime class to not be confidential, got %v", isConfidential)
	}

	// Test the case where the runtime class does not exist
	_, err = isConfidentialRuntimeClass(ctx, clientset, "nonexistent-runtime-class", defaultRuntimeClassHandler)
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
}

func TestIsThrottlingError(t *testing.T) {
	tests := []struct {
		desc     string
		err      error
		expected bool
	}{
		{
			desc:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			desc:     "no match",
			err:      errors.New("no match"),
			expected: false,
		},
		{
			desc:     "match error message",
			err:      errors.New("could not list storage accounts for account type Premium_LRS: Retriable: true, RetryAfter: 217s, HTTPStatusCode: 0, RawError: azure cloud provider throttled for operation StorageAccountListByResourceGroup with reason \"client throttled\""),
			expected: true,
		},
		{
			desc:     "match error message exceeds 1200s",
			err:      errors.New("could not list storage accounts for account type Premium_LRS: Retriable: true, RetryAfter: 2170s, HTTPStatusCode: 0, RawError: azure cloud provider throttled for operation StorageAccountListByResourceGroup with reason \"client throttled\""),
			expected: true,
		},
		{
			desc:     "match error message with TooManyRequests throttling",
			err:      errors.New("could not list storage accounts for account type Premium_LRS: Retriable: true, RetryAfter: 2170s, HTTPStatusCode: 429, RawError: azure cloud provider throttled for operation StorageAccountListByResourceGroup with reason \"TooManyRequests\""),
			expected: true,
		},
	}

	for _, test := range tests {
		result := isThrottlingError(test.err)
		if result != test.expected {
			t.Errorf("desc: (%s), input: err(%v), IsThrottlingError returned with bool(%t), not equal to expected(%t)",
				test.desc, test.err, result, test.expected)
		}
	}
}

func TestGetBackOff(t *testing.T) {
	tests := []struct {
		desc     string
		config   azureconfig.Config
		expected wait.Backoff
	}{
		{
			desc: "default backoff",
			config: azureconfig.Config{
				AzureClientConfig: azureconfig.AzureClientConfig{
					CloudProviderBackoffRetries:  0,
					CloudProviderBackoffDuration: 5,
				},
				CloudProviderBackoffExponent: 2,
				CloudProviderBackoffJitter:   1,
			},
			expected: wait.Backoff{
				Steps:    1,
				Duration: 5 * time.Second,
				Factor:   2,
				Jitter:   1,
			},
		},
		{
			desc: "backoff with retries > 1",
			config: azureconfig.Config{
				AzureClientConfig: azureconfig.AzureClientConfig{
					CloudProviderBackoffRetries:  3,
					CloudProviderBackoffDuration: 4,
				},
				CloudProviderBackoffExponent: 2,
				CloudProviderBackoffJitter:   1,
			},
			expected: wait.Backoff{
				Steps:    3,
				Duration: 4 * time.Second,
				Factor:   2,
				Jitter:   1,
			},
		},
	}

	for _, test := range tests {
		result := getBackOff(test.config)
		if !reflect.DeepEqual(result, test.expected) {
			t.Errorf("desc: (%s), input: config(%v), getBackOff returned with backoff(%v), not equal to expected(%v)",
				test.desc, test.config, result, test.expected)
		}
	}
}

func TestVolumeMounter(t *testing.T) {
	path := "/mnt/data"
	attributes := volume.Attributes{}

	mounter := &VolumeMounter{
		path:       path,
		attributes: attributes,
	}

	if mounter.GetPath() != path {
		t.Errorf("Expected path %s, but got %s", path, mounter.GetPath())
	}

	if mounter.GetAttributes() != attributes {
		t.Errorf("Expected attributes %v, but got %v", attributes, mounter.GetAttributes())
	}

	if err := mounter.CanMount(); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if err := mounter.SetUp(volume.MounterArgs{}); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if err := mounter.SetUpAt("", volume.MounterArgs{}); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	metrics, err := mounter.GetMetrics()
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if metrics != nil {
		t.Errorf("Expected nil metrics, but got %v", metrics)
	}
}

func TestGetFileServiceURL(t *testing.T) {
	tests := []struct {
		desc                  string
		account               string
		storageEndPointSuffix string
		expected              string
	}{
		{
			desc:                  "default storage endpoint",
			account:               "testaccount",
			storageEndPointSuffix: "",
			expected:              "https://testaccount.file.core.windows.net",
		},
		{
			desc:                  "custom storage endpoint",
			account:               "testaccount",
			storageEndPointSuffix: "core.windows.cn",
			expected:              "https://testaccount.file.core.windows.cn",
		},
		{
			desc:                  "storage endpoint core.windows.net",
			account:               "testaccount",
			storageEndPointSuffix: "core.windows.net",
			expected:              "https://testaccount.file.core.windows.net",
		},
	}

	for _, test := range tests {
		result := getFileServiceURL(test.account, test.storageEndPointSuffix)
		if result != test.expected {
			t.Errorf("desc: (%s), input: account(%s), storageEndPointSuffix(%s), getFileServiceURL returned with string(%s), not equal to expected(%s)",
				test.desc, test.account, test.storageEndPointSuffix, result, test.expected)
		}
	}
}

func TestIsValidSubscriptionID(t *testing.T) {
	tests := []struct {
		desc     string
		subsID   string
		expected bool
	}{
		{
			desc:     "valid subscription ID",
			subsID:   "c9d2281e-dcd5-4dfd-9a97-0d50377cdf76",
			expected: true,
		},
		{
			desc:     "invalid subscription ID - missing sections",
			subsID:   "123e4567-e89b-12d3-a456",
			expected: false,
		},
		{
			desc:     "invalid subscription ID - invalid characters",
			subsID:   "123e4567-e89b-12d3-a456-42661417400z",
			expected: false,
		},
		{
			desc:     "invalid subscription ID - empty string",
			subsID:   "",
			expected: false,
		},
		{
			desc:     "invalid subscription ID - too short",
			subsID:   "123e4567",
			expected: false,
		},
		{
			desc:     "invalid subscription ID - too long",
			subsID:   "123e4567-e89b-12d3-a456-426614174000-extra",
			expected: false,
		},
		{
			desc:     "invalid subscription ID - timestamp",
			subsID:   "2025-04-15T11:06:21.0000000Z",
			expected: false,
		},
	}

	for _, test := range tests {
		result := isValidSubscriptionID(test.subsID)
		if result != test.expected {
			t.Errorf("desc: (%s), input: subsID(%s), isValidSubscriptionID returned with bool(%v), not equal to expected(%v)",
				test.desc, test.subsID, result, test.expected)
		}
	}
}

func TestRemoveOptionIfExists(t *testing.T) {
	tests := []struct {
		desc            string
		options         []string
		removeOption    string
		expectedOptions []string
		expected        bool
	}{
		{
			desc:         "nil options",
			removeOption: "option",
			expected:     false,
		},
		{
			desc:            "empty options",
			options:         []string{},
			removeOption:    "option",
			expectedOptions: []string{},
			expected:        false,
		},
		{
			desc:            "option not found",
			options:         []string{"option1", "option2"},
			removeOption:    "option",
			expectedOptions: []string{"option1", "option2"},
			expected:        false,
		},
		{
			desc:            "option found in the last element",
			options:         []string{"option1", "option2", "option"},
			removeOption:    "option",
			expectedOptions: []string{"option1", "option2"},
			expected:        true,
		},
		{
			desc:            "option found in the first element",
			options:         []string{"option", "option1", "option2"},
			removeOption:    "option",
			expectedOptions: []string{"option1", "option2"},
			expected:        true,
		},
		{
			desc:            "option found in the middle element",
			options:         []string{"option1", "option", "option2"},
			removeOption:    "option",
			expectedOptions: []string{"option1", "option2"},
			expected:        true,
		},
		{
			desc:            "option found with case insensitive match",
			options:         []string{"option1", "encryptInTransit", "option2"},
			removeOption:    "encryptintransit",
			expectedOptions: []string{"option1", "option2"},
			expected:        true,
		},
	}

	for _, test := range tests {
		result, exists := removeOptionIfExists(test.options, test.removeOption)
		if !reflect.DeepEqual(result, test.expectedOptions) {
			t.Errorf("test[%s]: unexpected output: %v, expected result: %v", test.desc, result, test.expectedOptions)
		}
		if exists != test.expected {
			t.Errorf("test[%s]: unexpected output: %v, expected result: %v", test.desc, exists, test.expected)
		}
	}
}

func TestGetDefaultIOPS(t *testing.T) {
	tests := []struct {
		desc               string
		requestGiB         int
		storageAccountType string
		expected           *int32
	}{
		{
			desc:               "standardv2 with small storage",
			requestGiB:         100,
			storageAccountType: "StandardV2_LRS",
			expected:           int32Ptr(1020),
		},
		{
			desc:               "standardv2 with 1GiB storage",
			requestGiB:         1,
			storageAccountType: "StandardV2_LRS",
			expected:           int32Ptr(1001),
		},
		{
			desc:               "standardv2 with large storage hitting max",
			requestGiB:         300000,
			storageAccountType: "StandardV2_LRS",
			expected:           int32Ptr(50000),
		},
		{
			desc:               "standardv2 case insensitive",
			requestGiB:         100,
			storageAccountType: "STANDARDV2",
			expected:           int32Ptr(1020),
		},
		{
			desc:               "premiumv2 with small storage",
			requestGiB:         100,
			storageAccountType: "PremiumV2_LRS",
			expected:           int32Ptr(3100),
		},
		{
			desc:               "premiumv2 with 1GiB storage",
			requestGiB:         1,
			storageAccountType: "PremiumV2_LRS",
			expected:           int32Ptr(3001),
		},
		{
			desc:               "premiumv2 with large storage hitting max",
			requestGiB:         200000,
			storageAccountType: "PremiumV2_LRS",
			expected:           int32Ptr(102400),
		},
		{
			desc:               "premiumv2 case insensitive",
			requestGiB:         100,
			storageAccountType: "PREMIUMV2",
			expected:           int32Ptr(3100),
		},
		{
			desc:               "unsupported storage account type",
			requestGiB:         100,
			storageAccountType: "Standard_LRS",
			expected:           nil,
		},
		{
			desc:               "empty storage account type",
			requestGiB:         100,
			storageAccountType: "",
			expected:           nil,
		},
		{
			desc:               "standardv2 with zero storage",
			requestGiB:         0,
			storageAccountType: "StandardV2_LRS",
			expected:           int32Ptr(1000),
		},
		{
			desc:               "premiumv2 with zero storage",
			requestGiB:         0,
			storageAccountType: "PremiumV2_LRS",
			expected:           int32Ptr(3000),
		},
	}

	for _, test := range tests {
		result := getDefaultIOPS(test.requestGiB, test.storageAccountType)
		if test.expected == nil {
			if result != nil {
				t.Errorf("test[%s]: unexpected output: %v, expected nil", test.desc, *result)
			}
		} else {
			if result == nil {
				t.Errorf("test[%s]: unexpected nil output, expected: %v", test.desc, *test.expected)
			} else if *result != *test.expected {
				t.Errorf("test[%s]: unexpected output: %v, expected: %v", test.desc, *result, *test.expected)
			}
		}
	}
}

func TestGetDefaultBandwidth(t *testing.T) {
	tests := []struct {
		desc               string
		requestGiB         int
		storageAccountType string
		expected           *int32
	}{
		{
			desc:               "standardv2 with small storage",
			requestGiB:         100,
			storageAccountType: "StandardV2_LRS",
			expected:           int32Ptr(62),
		},
		{
			desc:               "standardv2 with 1GiB storage",
			requestGiB:         1,
			storageAccountType: "StandardV2_LRS",
			expected:           int32Ptr(61),
		},
		{
			desc:               "standardv2 with large storage hitting max",
			requestGiB:         300000,
			storageAccountType: "StandardV2_LRS_",
			expected:           int32Ptr(5120),
		},
		{
			desc:               "standardv2 case insensitive",
			requestGiB:         100,
			storageAccountType: "STANDARDV2",
			expected:           int32Ptr(62),
		},
		{
			desc:               "premiumv2 with small storage",
			requestGiB:         100,
			storageAccountType: "PremiumV2_LRS",
			expected:           int32Ptr(110),
		},
		{
			desc:               "premiumv2 with 1GiB storage",
			requestGiB:         1,
			storageAccountType: "PremiumV2_LRS",
			expected:           int32Ptr(101),
		},
		{
			desc:               "premiumv2 with large storage hitting max",
			requestGiB:         200000,
			storageAccountType: "PremiumV2_LRS",
			expected:           int32Ptr(10340),
		},
		{
			desc:               "premiumv2 case insensitive",
			requestGiB:         100,
			storageAccountType: "PREMIUMV2",
			expected:           int32Ptr(110),
		},
		{
			desc:               "unsupported storage account type",
			requestGiB:         100,
			storageAccountType: "Standard_LRS",
			expected:           nil,
		},
		{
			desc:               "empty storage account type",
			requestGiB:         100,
			storageAccountType: "",
			expected:           nil,
		},
		{
			desc:               "standardv2 with zero storage",
			requestGiB:         0,
			storageAccountType: "StandardV2_LRS",
			expected:           int32Ptr(60),
		},
		{
			desc:               "premiumv2 with zero storage",
			requestGiB:         0,
			storageAccountType: "PremiumV2_LRS",
			expected:           int32Ptr(100),
		},
		{
			desc:               "standardv2 with storage for exact calculation",
			requestGiB:         2500,
			storageAccountType: "StandardV2_LRS",
			expected:           int32Ptr(110),
		},
		{
			desc:               "premiumv2 with storage for exact calculation",
			requestGiB:         1000,
			storageAccountType: "PremiumV2_LRS",
			expected:           int32Ptr(200),
		},
	}

	for _, test := range tests {
		result := getDefaultBandwidth(test.requestGiB, test.storageAccountType)
		if test.expected == nil {
			if result != nil {
				t.Errorf("test[%s]: unexpected output: %v, expected nil", test.desc, *result)
			}
		} else {
			if result == nil {
				t.Errorf("test[%s]: unexpected nil output, expected: %v", test.desc, *test.expected)
			} else if *result != *test.expected {
				t.Errorf("test[%s]: unexpected output: %v, expected: %v", test.desc, *result, *test.expected)
			}
		}
	}
}

func int32Ptr(i int32) *int32 {
	return &i
}
