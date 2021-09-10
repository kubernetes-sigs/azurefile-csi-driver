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
	"testing"
	"time"

	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/stretchr/testify/assert"

	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/retry"
)

func TestGetFileSvcClient(t *testing.T) {
	accountName := "ut"
	accountKey := "ut"
	f := azureFileClient{
		env: &azure.Environment{
			StorageEndpointSuffix: "ut",
		},
		backoff: &retry.Backoff{
			Steps:    1,
			Duration: time.Second,
		},
	}
	_, actualErr := f.getFileSvcClient(accountName, accountKey)
	expectedErr := fmt.Errorf("error creating azure client: azure: account name is not valid: it must be between 3 and 24 characters, and only may contain numbers and lowercase letters: ut")
	if !reflect.DeepEqual(actualErr, expectedErr) {
		t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
	}
	accountName = "unittest"
	accountKey = "dW5pdHRlc3Q="
	fileserviceClient, err := f.getFileSvcClient(accountName, accountKey)
	assert.NotNil(t, fileserviceClient)
	assert.NoError(t, err)

	//Test that the client uses defaultStorageEndpointSuffix
	f = azureFileClient{
		env: &azure.Environment{},
		backoff: &retry.Backoff{
			Steps:    1,
			Duration: time.Second,
		},
	}
	accountName = "unittest"
	accountKey = "dW5pdHRlc3Q="
	fileserviceClient, err = f.getFileSvcClient(accountName, accountKey)
	assert.NotNil(t, fileserviceClient)
	assert.NoError(t, err)
}

func TestCreateFileShare(t *testing.T) {

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "ShareOptions empty",
			testFunc: func(t *testing.T) {
				accountName := "unittest"
				accountKey := "dW5pdHRlc3Q="
				f := azureFileClient{}
				actualErr := f.CreateFileShare(accountName, accountKey, nil)
				expectedErr := fmt.Errorf("shareOptions of account(%s) is nil", accountName)
				if !reflect.DeepEqual(actualErr, expectedErr) {
					t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
				}
			},
		},
		{
			name: "AccountName invalid",
			testFunc: func(t *testing.T) {
				accountName := "ut"
				accountKey := "dW5pdHRlc3Q="
				options := &fileclient.ShareOptions{
					RequestGiB: 10,
				}
				f := azureFileClient{
					env: &azure.Environment{
						StorageEndpointSuffix: "ut",
					},
				}
				actualErr := f.CreateFileShare(accountName, accountKey, options)
				expectedErr := fmt.Errorf("error creating azure client: azure: account name is not valid: it must be between 3 and 24 characters, and only may contain numbers and lowercase letters: ut")
				if !reflect.DeepEqual(actualErr, expectedErr) {
					t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
				}
				actualErr = f.createFileShare(accountName, accountKey, "unit-test", 10)
				if !reflect.DeepEqual(actualErr, expectedErr) {
					t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestDeleteFileShare(t *testing.T) {
	accountName := "ut"
	accountKey := "ut"
	f := azureFileClient{
		env: &azure.Environment{
			StorageEndpointSuffix: "ut",
		},
	}
	actualErr := f.deleteFileShare(accountName, accountKey, "")
	expectedErr := fmt.Errorf("error creating azure client: azure: account name is not valid: it must be between 3 and 24 characters, and only may contain numbers and lowercase letters: ut")
	if !reflect.DeepEqual(actualErr, expectedErr) {
		t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
	}
}

func TestResizeFileShare(t *testing.T) {
	accountName := "ut"
	accountKey := "dW5pdHRlc3Q="
	f := azureFileClient{
		env: &azure.Environment{
			StorageEndpointSuffix: "ut",
		},
	}
	actualErr := f.resizeFileShare(accountName, accountKey, "", 10)
	expectedErr := fmt.Errorf("error creating azure client: azure: account name is not valid: it must be between 3 and 24 characters, and only may contain numbers and lowercase letters: ut")
	if !reflect.DeepEqual(actualErr, expectedErr) {
		t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
	}
	//file share size is already greater or equal than requested size
	accountName = "unittest"
	actualErr = f.resizeFileShare(accountName, accountKey, "", -2)
	assert.NoError(t, actualErr)

	//Request quota size is invalid
	accountName = "unittest"
	actualErr = f.resizeFileShare(accountName, accountKey, "", 6000)
	expectedErr = fmt.Errorf("failed to set quota on file share , err: invalid value 6000 for quota, valid values are [1, 5120]")
	if !reflect.DeepEqual(actualErr, expectedErr) {
		t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
	}
}
