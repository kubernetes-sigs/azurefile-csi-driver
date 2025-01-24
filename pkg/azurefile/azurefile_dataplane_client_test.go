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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
)

// mockShareClient implements the necessary methods of share.Client.
type mockShareClient struct {
	// Embed any necessary fields
}

// GetProperties returns dummy share properties.
func (m *mockShareClient) GetProperties(_ context.Context, _ *share.GetPropertiesOptions) (share.GetPropertiesResponse, error) {
	return share.GetPropertiesResponse{
		Quota: to.Ptr(int32(10)),
	}, nil
}

func (m *mockShareClient) Create(_ context.Context, _ *share.CreateOptions) (share.CreateResponse, error) {
	return share.CreateResponse{}, nil
}

func (m *mockShareClient) Delete(_ context.Context, _ *share.DeleteOptions) (share.DeleteResponse, error) {
	return share.DeleteResponse{}, nil
}

func (m *mockShareClient) SetProperties(_ context.Context, _ *share.SetPropertiesOptions) (share.SetPropertiesResponse, error) {
	return share.SetPropertiesResponse{}, nil
}

func init() {
	newShareClient = func(_ *azureFileDataplaneClient, _ string) ShareClientInterface {
		return &mockShareClient{}
	}
}

func TestCreateFileShare(t *testing.T) {

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success on create",
			testFunc: func(t *testing.T) {
				options := &ShareOptions{
					Name:       "devstoreaccount1",
					RequestGiB: 10,
				}
				f, err := newAzureFileClient("test", "dW5pdHRlc3Q=", "ut")
				if err != nil {
					t.Errorf("error creating azure client: %v", err)
				}
				actualErr := f.CreateFileShare(context.Background(), options)
				if actualErr != nil {
					t.Errorf("actualErr: (%v), expectedErr: nil", actualErr)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestNewAzureFileClient(t *testing.T) {
	_, actualErr := newAzureFileClient("ut", "ut", "ut")
	if actualErr != nil {
		expectedErr := fmt.Errorf("error creating azure client: decode account key: illegal base64 data at input byte 0")
		if !reflect.DeepEqual(actualErr, expectedErr) {
			t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
		}
	}
}

func TestDeleteFileShare(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success on delete",
			testFunc: func(t *testing.T) {
				f, err := newAzureFileClient("test", "dW5pdHRlc3Q=", "ut")
				if err != nil {
					t.Errorf("error creating azure client: %v", err)
				}
				shareName := "nonexistent"
				actualErr := f.DeleteFileShare(context.Background(), shareName)
				if actualErr != nil {
					t.Errorf("actualErr: (%v), expectedErr: nil", actualErr)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestResizeFileShare(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success (already greater) on resize",
			testFunc: func(t *testing.T) {
				f, err := newAzureFileClient("test", "dW5pdHRlc3Q=", "ut")
				if err != nil {
					t.Errorf("error creating azure client: %v", err)
				}
				shareName := "nonexistent"
				actualErr := f.ResizeFileShare(context.Background(), shareName, 5)
				if actualErr != nil {
					t.Errorf("actualErr: (%v), expectedErr: nil", actualErr)
				}
			},
		},
		{
			name: "success on resize",
			testFunc: func(t *testing.T) {
				f, err := newAzureFileClient("test", "dW5pdHRlc3Q=", "ut")
				if err != nil {
					t.Errorf("error creating azure client: %v", err)
				}
				shareName := "nonexistent"
				actualErr := f.ResizeFileShare(context.Background(), shareName, 20)
				if actualErr != nil {
					t.Errorf("actualErr: (%v), expectedErr: nil", actualErr)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestGetFileShareQuotaDataPlane(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "success on get quota",
			testFunc: func(t *testing.T) {
				f, err := newAzureFileClient("test", "dW5pdHRlc3Q=", "ut")
				if err != nil {
					t.Errorf("error creating azure client: %v", err)
				}
				shareName := "nonexistent"
				actualQuota, actualErr := f.GetFileShareQuota(context.Background(), shareName)
				expectedQuota := 10
				if actualErr != nil || actualQuota != expectedQuota {
					t.Errorf("actualErr: (%v), expectedErr: (nil), actualQuota: (%v), expectedQuota: (%v)", actualErr, actualQuota, expectedQuota)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}
