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

	"go.uber.org/mock/gomock"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/fileshareclient/mock_fileshareclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/mock_azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/storage"
)

func TestNewAzureMgmtFileClientClientFactoryNil(t *testing.T) {
	_, actualErr := newAzureFileMgmtClient(nil, nil)
	if actualErr != nil {
		expectedErr := fmt.Errorf("clientFactory is nil")
		if !reflect.DeepEqual(actualErr, expectedErr) {
			t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
		}
	}
}
func TestNewAzureMgmtFileClientAccountOptionNil(t *testing.T) {
	cntl := gomock.NewController(t)
	defer cntl.Finish()
	_, actualErr := newAzureFileMgmtClient(mock_azclient.NewMockClientFactory(cntl), nil)
	if actualErr != nil {
		expectedErr := fmt.Errorf("accountOptions is nil")
		if !reflect.DeepEqual(actualErr, expectedErr) {
			t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
		}
	}
}

func TestNewAzureMgmtFileClient(t *testing.T) {
	cntl := gomock.NewController(t)
	defer cntl.Finish()
	clientFactory := mock_azclient.NewMockClientFactory(cntl)
	fileClient := mock_fileshareclient.NewMockInterface(cntl)
	clientFactory.EXPECT().GetFileShareClientForSub("testsub").Return(fileClient, nil)
	_, actualErr := newAzureFileMgmtClient(clientFactory, &storage.AccountOptions{
		SubscriptionID: "testsub",
		ResourceGroup:  "testrg",
	})
	if actualErr != nil {
		expectedErr := fmt.Errorf("accountOptions is nil")
		if !reflect.DeepEqual(actualErr, expectedErr) {
			t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
		}
	}
}
