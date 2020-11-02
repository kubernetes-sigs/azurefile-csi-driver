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
	"io/ioutil"
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2019-06-01/network"
	"github.com/golang/mock/gomock"

	"k8s.io/legacy-cloud-providers/azure"
	"k8s.io/legacy-cloud-providers/azure/clients/subnetclient/mocksubnetclient"
	"k8s.io/legacy-cloud-providers/azure/retry"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/testutil"
)

func skipIfTestingOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}
}

// TestGetCloudProvider tests the func GetCloudProvider().
// To run this unit test successfully, need to ensure /etc/kubernetes/azure.json nonexistent.
func TestGetCloudProvider(t *testing.T) {
	var (
		fakeCredFile       = testutil.GetWorkDirPath("fake-cred-file.json", t)
		fakeKubeConfig     = testutil.GetWorkDirPath("fake-kube-config", t)
		emptyKubeConfig    = testutil.GetWorkDirPath("empty-kube-config", t)
		notExistKubeConfig = testutil.GetWorkDirPath("non-exist.json", t)
	)

	fakeContent := `apiVersion: v1
clusters:
- cluster:
    server: https://localhost:8080
  name: foo-cluster
contexts:
- context:
    cluster: foo-cluster
    user: foo-user
    namespace: bar
  name: foo-context
current-context: foo-context
kind: Config
users:
- name: foo-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1alpha1
      args:
      - arg-1
      - arg-2
      command: foo-command
`

	err := createTestFile(emptyKubeConfig)
	if err != nil {
		t.Error(err)
	}
	defer func() {
		if err := os.Remove(emptyKubeConfig); err != nil {
			t.Error(err)
		}
	}()

	tests := []struct {
		desc        string
		kubeconfig  string
		expectedErr testutil.TestError
	}{
		{
			desc:       "[failure] out of cluster, no kubeconfig, no credential file",
			kubeconfig: "",
			expectedErr: testutil.TestError{
				DefaultError: fmt.Errorf("Failed to load config from file: %s, cloud not get azure cloud provider", DefaultCredFilePathLinux),
				WindowsError: fmt.Errorf("Failed to load config from file: %s, cloud not get azure cloud provider", DefaultCredFilePathWindows),
			},
		},
		{
			desc:       "[failure] out of cluster & in cluster, specify a non-exist kubeconfig, no credential file",
			kubeconfig: notExistKubeConfig,
			expectedErr: testutil.TestError{
				DefaultError: fmt.Errorf("Failed to load config from file: %s, cloud not get azure cloud provider", DefaultCredFilePathLinux),
				WindowsError: fmt.Errorf("Failed to load config from file: %s, cloud not get azure cloud provider", DefaultCredFilePathWindows),
			},
		},
		{
			desc:       "[failure] out of cluster & in cluster, specify a empty kubeconfig, no credential file",
			kubeconfig: emptyKubeConfig,
			expectedErr: testutil.TestError{
				DefaultError: fmt.Errorf("failed to get KubeClient: invalid configuration: no configuration has been provided, try setting KUBERNETES_MASTER environment variable"),
			},
		},
		{
			desc:       "[failure] out of cluster & in cluster, specify a fake kubeconfig, no credential file",
			kubeconfig: fakeKubeConfig,
			expectedErr: testutil.TestError{
				DefaultError: fmt.Errorf("Failed to load config from file: %s, cloud not get azure cloud provider", DefaultCredFilePathLinux),
				WindowsError: fmt.Errorf("Failed to load config from file: %s, cloud not get azure cloud provider", DefaultCredFilePathWindows),
			},
		},
		{
			desc:        "[success] out of cluster & in cluster, no kubeconfig, a fake credential file",
			kubeconfig:  "",
			expectedErr: testutil.TestError{},
		},
	}

	for _, test := range tests {
		if test.desc == "[failure] out of cluster & in cluster, specify a fake kubeconfig, no credential file" {
			err := createTestFile(fakeKubeConfig)
			if err != nil {
				t.Error(err)
			}
			defer func() {
				if err := os.Remove(fakeKubeConfig); err != nil {
					t.Error(err)
				}
			}()

			if err := ioutil.WriteFile(fakeKubeConfig, []byte(fakeContent), 0666); err != nil {
				t.Error(err)
			}
		}
		if test.desc == "[success] out of cluster & in cluster, no kubeconfig, a fake credential file" {
			err := createTestFile(fakeCredFile)
			if err != nil {
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
		}
		_, err := GetCloudProvider(test.kubeconfig)
		if !testutil.AssertError(err, &test.expectedErr) {
			t.Errorf("desc: %s,\n input: %q, GetCloudProvider err: %v, expectedErr: %v", test.desc, test.kubeconfig, err, test.expectedErr)
		}
	}
}

func createTestFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return nil
}

func TestUpdateSubnetServiceEndpoints(t *testing.T) {

	fakeSubnetLockMap := newLockMap()
	ctx := context.TODO()

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "[fail]  nil cloud parameter",
			testFunc: func(t *testing.T) {
				expectedErr := fmt.Errorf("the cloud parameter is nil")
				err := updateSubnetServiceEndpoints(ctx, nil, nil)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "[fail]  nil subnetLockMap parameter",
			testFunc: func(t *testing.T) {
				fakeCloud := &azure.Cloud{
					Config: azure.Config{
						ResourceGroup: "rg",
						Location:      "loc",
						VnetName:      "fake-vnet",
						SubnetName:    "fake-subnet",
					},
				}

				expectedErr := fmt.Errorf("the subnet lockMap parameter is nil")
				err := updateSubnetServiceEndpoints(ctx, fakeCloud, nil)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "[fail] no subnet",
			testFunc: func(t *testing.T) {
				retErr := retry.NewError(false, fmt.Errorf("the subnet does not exist"))
				fakeCloud := &azure.Cloud{
					Config: azure.Config{
						ResourceGroup: "rg",
						Location:      "loc",
						VnetName:      "fake-vnet",
						SubnetName:    "fake-subnet",
					},
				}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockSubnetClient := mocksubnetclient.NewMockInterface(ctrl)
				fakeCloud.SubnetsClient = mockSubnetClient

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(network.Subnet{}, retErr).Times(1)

				expectedErr := fmt.Errorf("failed to get the subnet %s under vnet %s: %v", fakeCloud.SubnetName, fakeCloud.VnetName, retErr)
				err := updateSubnetServiceEndpoints(ctx, fakeCloud, fakeSubnetLockMap)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "[success] subnetPropertiesFormat is nil",
			testFunc: func(t *testing.T) {
				fakeCloud := &azure.Cloud{
					Config: azure.Config{
						ResourceGroup: "rg",
						Location:      "loc",
						VnetName:      "fake-vnet",
						SubnetName:    "fake-subnet",
					},
				}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockSubnetClient := mocksubnetclient.NewMockInterface(ctrl)
				fakeCloud.SubnetsClient = mockSubnetClient

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(network.Subnet{}, nil).Times(1)
				mockSubnetClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

				err := updateSubnetServiceEndpoints(ctx, fakeCloud, fakeSubnetLockMap)
				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "[success] ServiceEndpoints is nil",
			testFunc: func(t *testing.T) {
				fakeSubnet := network.Subnet{
					SubnetPropertiesFormat: &network.SubnetPropertiesFormat{},
				}
				fakeCloud := &azure.Cloud{
					Config: azure.Config{
						ResourceGroup: "rg",
						Location:      "loc",
						VnetName:      "fake-vnet",
						SubnetName:    "fake-subnet",
					},
				}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockSubnetClient := mocksubnetclient.NewMockInterface(ctrl)
				fakeCloud.SubnetsClient = mockSubnetClient

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).Times(1)
				mockSubnetClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

				err := updateSubnetServiceEndpoints(ctx, fakeCloud, fakeSubnetLockMap)
				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "[success] storageService does not exists",
			testFunc: func(t *testing.T) {
				fakeSubnet := network.Subnet{
					SubnetPropertiesFormat: &network.SubnetPropertiesFormat{
						ServiceEndpoints: &[]network.ServiceEndpointPropertiesFormat{},
					},
				}
				fakeCloud := &azure.Cloud{
					Config: azure.Config{
						ResourceGroup: "rg",
						Location:      "loc",
						VnetName:      "fake-vnet",
						SubnetName:    "fake-subnet",
					},
				}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockSubnetClient := mocksubnetclient.NewMockInterface(ctrl)
				fakeCloud.SubnetsClient = mockSubnetClient

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).Times(1)
				mockSubnetClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

				err := updateSubnetServiceEndpoints(ctx, fakeCloud, fakeSubnetLockMap)
				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "[success] storageService already exists",
			testFunc: func(t *testing.T) {
				fakeSubnet := network.Subnet{
					SubnetPropertiesFormat: &network.SubnetPropertiesFormat{
						ServiceEndpoints: &[]network.ServiceEndpointPropertiesFormat{
							{
								Service: &storageService,
							},
						},
					},
				}
				fakeCloud := &azure.Cloud{
					Config: azure.Config{
						ResourceGroup: "rg",
						Location:      "loc",
						VnetName:      "fake-vnet",
						SubnetName:    "fake-subnet",
					},
				}

				ctrl := gomock.NewController(t)
				defer ctrl.Finish()
				mockSubnetClient := mocksubnetclient.NewMockInterface(ctrl)
				fakeCloud.SubnetsClient = mockSubnetClient

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).Times(1)

				err := updateSubnetServiceEndpoints(ctx, fakeCloud, fakeSubnetLockMap)
				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}
