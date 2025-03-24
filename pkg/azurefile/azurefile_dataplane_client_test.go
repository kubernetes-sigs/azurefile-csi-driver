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
)

func TestCreateFileShare(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "",
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
				expectedErr := fmt.Errorf("")
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
			name: "expect error on delete",
			testFunc: func(t *testing.T) {
				f, err := newAzureFileClient("test", "dW5pdHRlc3Q=", "ut")
				if err != nil {
					t.Errorf("error creating azure client: %v", err)
				}
				shareName := "nonexistent"
				actualErr := f.DeleteFileShare(context.Background(), shareName)
				expectedErr := fmt.Errorf("Delete \"https://test.file.ut/%s?restype=share\"", shareName)
				if actualErr == nil || !strings.HasPrefix(actualErr.Error(), expectedErr.Error()) {
					t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
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
			name: "expect error on resize",
			testFunc: func(t *testing.T) {
				f, err := newAzureFileClient("test", "dW5pdHRlc3Q=", "ut")
				if err != nil {
					t.Errorf("error creating azure client: %v", err)
				}
				shareName := "nonexistent"
				actualErr := f.ResizeFileShare(context.Background(), shareName, 20)
				expectedErr := fmt.Errorf("failed to set quota on file share %s", shareName)
				if actualErr == nil || !strings.HasPrefix(actualErr.Error(), expectedErr.Error()) {
					t.Errorf("actualErr: (%v), expectedErr: (%v)", actualErr, expectedErr)
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
			name: "expect error on resize",
			testFunc: func(t *testing.T) {
				f, err := newAzureFileClient("test", "dW5pdHRlc3Q=", "ut")
				if err != nil {
					t.Errorf("error creating azure client: %v", err)
				}
				shareName := "nonexistent"
				actualQuota, actualErr := f.GetFileShareQuota(context.Background(), shareName)
				expectedErr := fmt.Errorf("Get \"https://test.file.ut/%s?restype=share\"", shareName)
				expectedQuota := -1
				if actualErr == nil || !strings.HasPrefix(actualErr.Error(), expectedErr.Error()) || actualQuota != -1 {
					t.Errorf("actualErr: (%v), expectedErr: (%v), actualQuota: (%v), expectedQuota: (%v)", actualErr, expectedErr, actualQuota, expectedQuota)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}
