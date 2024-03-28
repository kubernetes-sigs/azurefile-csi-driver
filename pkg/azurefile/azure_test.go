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
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2022-07-01/network"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"sigs.k8s.io/azurefile-csi-driver/test/utils/testutil"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/subnetclient/mocksubnetclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/retry"

	azureprovider "sigs.k8s.io/cloud-provider-azure/pkg/provider"
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
      apiVersion: client.authentication.k8s.io/v1beta1
      args:
      - arg-1
      - arg-2
      command: foo-command
`

	if err := createTestFile(emptyKubeConfig); err != nil {
		t.Error(err)
	}
	defer func() {
		if err := os.Remove(emptyKubeConfig); err != nil {
			t.Error(err)
		}
	}()

	tests := []struct {
		desc                                  string
		createFakeCredFile                    bool
		createFakeKubeConfig                  bool
		setFederatedWorkloadIdentityEnv       bool
		kubeconfig                            string
		userAgent                             string
		allowEmptyCloudConfig                 bool
		aadFederatedTokenFile                 string
		useFederatedWorkloadIdentityExtension bool
		aadClientID                           string
		tenantID                              string
		expectedErr                           testutil.TestError
	}{
		{
			desc:                  "out of cluster, no kubeconfig, no credential file",
			kubeconfig:            "",
			allowEmptyCloudConfig: true,
			expectedErr:           testutil.TestError{},
		},
		{
			desc:                  "[failure][disallowEmptyCloudConfig] out of cluster, no kubeconfig, no credential file",
			kubeconfig:            "",
			allowEmptyCloudConfig: false,
			expectedErr: testutil.TestError{
				DefaultError: fmt.Errorf("no cloud config provided, error"),
			},
		},
		{
			desc:                  "[failure] out of cluster & in cluster, specify a non-exist kubeconfig, no credential file",
			kubeconfig:            notExistKubeConfig,
			allowEmptyCloudConfig: true,
			expectedErr:           testutil.TestError{},
		},
		{
			desc:                  "[failure] out of cluster & in cluster, specify a empty kubeconfig, no credential file",
			kubeconfig:            emptyKubeConfig,
			allowEmptyCloudConfig: true,
			expectedErr: testutil.TestError{
				DefaultError: fmt.Errorf("failed to get KubeClient: invalid configuration: no configuration has been provided, try setting KUBERNETES_MASTER environment variable"),
			},
		},
		{
			desc:                  "[failure] out of cluster & in cluster, specify a fake kubeconfig, no credential file",
			createFakeKubeConfig:  true,
			kubeconfig:            fakeKubeConfig,
			allowEmptyCloudConfig: true,
			expectedErr:           testutil.TestError{},
		},
		{
			desc:                  "[success] out of cluster & in cluster, no kubeconfig, a fake credential file",
			createFakeCredFile:    true,
			kubeconfig:            "",
			userAgent:             "useragent",
			allowEmptyCloudConfig: true,
			expectedErr:           testutil.TestError{},
		},
		{
			desc:                                  "[success] get azure client with workload identity",
			createFakeKubeConfig:                  true,
			createFakeCredFile:                    true,
			setFederatedWorkloadIdentityEnv:       true,
			kubeconfig:                            fakeKubeConfig,
			userAgent:                             "useragent",
			useFederatedWorkloadIdentityExtension: true,
			aadFederatedTokenFile:                 "fake-token-file",
			aadClientID:                           "fake-client-id",
			tenantID:                              "fake-tenant-id",
			expectedErr:                           testutil.TestError{},
		},
	}

	for _, test := range tests {
		if test.createFakeKubeConfig {
			if err := createTestFile(fakeKubeConfig); err != nil {
				t.Error(err)
			}
			defer func() {
				if err := os.Remove(fakeKubeConfig); err != nil && !os.IsNotExist(err) {
					t.Error(err)
				}
			}()

			if err := os.WriteFile(fakeKubeConfig, []byte(fakeContent), 0666); err != nil {
				t.Error(err)
			}
		}
		if test.createFakeCredFile {
			if err := createTestFile(fakeCredFile); err != nil {
				t.Error(err)
			}
			defer func() {
				if err := os.Remove(fakeCredFile); err != nil && !os.IsNotExist(err) {
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
		if test.setFederatedWorkloadIdentityEnv {
			t.Setenv("AZURE_TENANT_ID", test.tenantID)
			t.Setenv("AZURE_CLIENT_ID", test.aadClientID)
			t.Setenv("AZURE_FEDERATED_TOKEN_FILE", test.aadFederatedTokenFile)
		}

		cloud, err := getCloudProvider(context.Background(), test.kubeconfig, "", "", "", test.userAgent, test.allowEmptyCloudConfig, false, 5, 10)
		if !testutil.AssertError(err, &test.expectedErr) && !strings.Contains(err.Error(), test.expectedErr.DefaultError.Error()) {
			t.Errorf("desc: %s,\n input: %q, getCloudProvider err: %v, expectedErr: %v", test.desc, test.kubeconfig, err, test.expectedErr)
		}
		if cloud == nil {
			t.Errorf("return value of getCloudProvider should not be nil even there is error")
		} else {
			assert.Equal(t, cloud.UserAgent, test.userAgent)
			assert.Equal(t, cloud.AADFederatedTokenFile, test.aadFederatedTokenFile)
			assert.Equal(t, cloud.UseFederatedWorkloadIdentityExtension, test.useFederatedWorkloadIdentityExtension)
			assert.Equal(t, cloud.AADClientID, test.aadClientID)
			assert.Equal(t, cloud.TenantID, test.tenantID)
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
	d := NewFakeDriver()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockSubnetClient := mocksubnetclient.NewMockInterface(ctrl)

	config := azureprovider.Config{
		ResourceGroup: "rg",
		Location:      "loc",
		VnetName:      "fake-vnet",
		SubnetName:    "fake-subnet",
	}

	d.cloud = &azureprovider.Cloud{
		SubnetsClient: mockSubnetClient,
		Config:        config,
	}
	ctx := context.TODO()

	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "[fail] no subnet",
			testFunc: func(t *testing.T) {
				retErr := retry.NewError(false, fmt.Errorf("the subnet does not exist"))
				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(network.Subnet{}, retErr).Times(1)
				expectedErr := fmt.Errorf("failed to get the subnet %s under vnet %s: %v", config.SubnetName, config.VnetName, retErr)
				err := d.updateSubnetServiceEndpoints(ctx, "", "", "")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "[success] subnetPropertiesFormat is nil",
			testFunc: func(t *testing.T) {
				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(network.Subnet{}, nil).Times(1)
				mockSubnetClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

				err := d.updateSubnetServiceEndpoints(ctx, "", "", "")
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

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).Times(1)
				mockSubnetClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

				err := d.updateSubnetServiceEndpoints(ctx, "", "", "")
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

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).Times(1)
				mockSubnetClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

				err := d.updateSubnetServiceEndpoints(ctx, "", "", "")
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

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).Times(1)

				err := d.updateSubnetServiceEndpoints(ctx, "", "", "")
				if !reflect.DeepEqual(err, nil) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "[fail] SubnetsClient is nil",
			testFunc: func(t *testing.T) {
				d.cloud.SubnetsClient = nil
				expectedErr := fmt.Errorf("SubnetsClient is nil")
				err := d.updateSubnetServiceEndpoints(ctx, "", "", "")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestGetKubeConfig(t *testing.T) {
	// skip for now as this is very flaky on Windows
	skipIfTestingOnWindows(t)
	emptyKubeConfig := "empty-Kube-Config"
	validKubeConfig := "valid-Kube-Config"
	fakeContent := `
apiVersion: v1
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
      apiVersion: client.authentication.k8s.io/v1beta1
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

	err = createTestFile(validKubeConfig)
	if err != nil {
		t.Error(err)
	}
	defer func() {
		if err := os.Remove(validKubeConfig); err != nil {
			t.Error(err)
		}
	}()

	if err := os.WriteFile(validKubeConfig, []byte(fakeContent), 0666); err != nil {
		t.Error(err)
	}

	os.Setenv("CONTAINER_SANDBOX_MOUNT_POINT", "C:\\var\\lib\\kubelet\\pods\\12345678-1234-1234-1234-123456789012")
	defer os.Unsetenv("CONTAINER_SANDBOX_MOUNT_POINT")

	tests := []struct {
		desc                     string
		kubeconfig               string
		enableWindowsHostProcess bool
		expectError              bool
		envVariableHasConfig     bool
		envVariableConfigIsValid bool
	}{
		{
			desc:                     "[success] valid kube config passed",
			kubeconfig:               validKubeConfig,
			enableWindowsHostProcess: false,
			expectError:              false,
			envVariableHasConfig:     false,
			envVariableConfigIsValid: false,
		},
		{
			desc:                     "[failure] invalid kube config passed",
			kubeconfig:               emptyKubeConfig,
			enableWindowsHostProcess: false,
			expectError:              true,
			envVariableHasConfig:     false,
			envVariableConfigIsValid: false,
		},
		{
			desc:                     "[failure] empty Kubeconfig under container sandbox mount path",
			kubeconfig:               "",
			enableWindowsHostProcess: true,
			expectError:              true,
			envVariableHasConfig:     false,
			envVariableConfigIsValid: false,
		},
	}

	for _, test := range tests {
		_, err := getKubeConfig(test.kubeconfig, test.enableWindowsHostProcess)
		receiveError := (err != nil)
		if test.expectError != receiveError {
			t.Errorf("desc: %s,\n input: %q, GetCloudProvider err: %v, expectErr: %v", test.desc, test.kubeconfig, err, test.expectError)
		}
	}
}
