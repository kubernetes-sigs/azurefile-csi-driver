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

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/fileshareclient/mock_fileshareclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/mock_azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/storage"
)

func TestNewAzureMgmtFileClientClientFactoryNil(t *testing.T) {
	_, actualErr := newAzureFileMgmtClient(nil, nil)
	if actualErr != nil {
		expectedErr := fmt.Errorf("cloud provider is not initialized")
		if !reflect.DeepEqual(actualErr, expectedErr) {
			t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
		}
	}
}
func TestNewAzureMgmtFileClientAccountOptionNil(t *testing.T) {
	cntl := gomock.NewController(t)
	defer cntl.Finish()
	cloud := &storage.AccountRepo{
		ComputeClientFactory: mock_azclient.NewMockClientFactory(cntl),
	}
	_, actualErr := newAzureFileMgmtClient(cloud, nil)
	if actualErr != nil {
		expectedErr := fmt.Errorf("accountOptions is nil")
		if !reflect.DeepEqual(actualErr, expectedErr) {
			t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
		}
	}
}

func TestNewAzureMgmtFileClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	computeClientFactory := mock_azclient.NewMockClientFactory(ctrl)
	mockFileClient := mock_fileshareclient.NewMockInterface(ctrl)
	computeClientFactory.EXPECT().GetFileShareClientForSub("testsub").Return(mockFileClient, nil).AnyTimes()
	cloud := &storage.AccountRepo{
		ComputeClientFactory: computeClientFactory,
	}
	_, actualErr := newAzureFileMgmtClient(cloud, &storage.AccountOptions{
		SubscriptionID: "testsub",
		ResourceGroup:  "testrg",
	})
	assert.Equal(t, actualErr, nil, "newAzureFileMgmtClient should return success")
}
