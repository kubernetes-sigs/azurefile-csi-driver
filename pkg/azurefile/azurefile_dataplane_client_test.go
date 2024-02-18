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
	"fmt"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/fileclient"
)

func TestCreateFileShare(t *testing.T) {

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "",
			testFunc: func(t *testing.T) {
				options := &fileclient.ShareOptions{
					Name:       "devstoreaccount1",
					RequestGiB: 10,
				}
				f, err := newAzureFileClient("test", "dW5pdHRlc3Q=", "ut", nil)
				if err != nil {
					t.Errorf("error creating azure client: %v", err)
				}
				actualErr := f.CreateFileShare(context.Background(), options)
				expectedErr := fmt.Errorf("failed to create file share, err: ")
				if !strings.HasPrefix(actualErr.Error(), expectedErr.Error()) {
					t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNewAzureFileClient(t *testing.T) {
	_, actualErr := newAzureFileClient("ut", "ut", "ut", nil)
	if actualErr != nil {
		expectedErr := fmt.Errorf("error creating azure client: azure: account name is not valid: it must be between 3 and 24 characters, and only may contain numbers and lowercase letters: ut")
		if !reflect.DeepEqual(actualErr, expectedErr) {
			t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
		}
	}
}
