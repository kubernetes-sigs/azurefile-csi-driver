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
	"k8s.io/utils/ptr"

	"sigs.k8s.io/azurefile-csi-driver/test/utils/testutil"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/subnetclient/mocksubnetclient"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	fake "k8s.io/client-go/kubernetes/fake"
	azureprovider "sigs.k8s.io/cloud-provider-azure/pkg/provider"
	azureconfig "sigs.k8s.io/cloud-provider-azure/pkg/provider/config"
)

func skipIfTestingOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}
}

func TestGetRuntimeClassForPod(t *testing.T) {
	ctx := context.TODO()

	// Test the case where kubeClient is nil
	_, err := getRuntimeClassForPod(ctx, nil, "test-pod", "default")
	if err == nil || err.Error() != "kubeClient is nil" {
		t.Fatalf("expected error 'kubeClient is nil', got %v", err)
	}

	// Create a fake clientset
	clientset := fake.NewSimpleClientset()

	// Test the case where the pod exists and has a RuntimeClassName
	runtimeClassName := "my-runtime-class"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: &runtimeClassName,
		},
	}
	_, err = clientset.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	runtimeClass, err := getRuntimeClassForPod(ctx, clientset, "test-pod", "default")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runtimeClass != runtimeClassName {
		t.Fatalf("expected runtime class name to be '%s', got '%s'", runtimeClassName, runtimeClass)
	}

	// Test the case where the pod exists but does not have a RuntimeClassName
	podWithoutRuntimeClass := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-no-runtime",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{},
	}
	_, err = clientset.CoreV1().Pods("default").Create(ctx, podWithoutRuntimeClass, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	runtimeClass, err = getRuntimeClassForPod(ctx, clientset, "test-pod-no-runtime", "default")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runtimeClass != "" {
		t.Fatalf("expected runtime class name to be '', got '%s'", runtimeClass)
	}

	// Test the case where the pod does not exist
	_, err = getRuntimeClassForPod(ctx, clientset, "nonexistent-pod", "default")
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
}

// TestGetCloudProvider tests the func GetCloudProvider().
// To run this unit test successfully, need to ensure /etc/kubernetes/azure.json nonexistent.
func TestGetCloudProvider(t *testing.T) {
	var (
		fakeCredFile = testutil.GetWorkDirPath("fake-cred-file.json", t)
	)
	tests := []struct {
		desc                                  string
		createFakeCredFile                    bool
		setFederatedWorkloadIdentityEnv       bool
		kubeclient                            kubernetes.Interface
		userAgent                             string
		allowEmptyCloudConfig                 bool
		aadFederatedTokenFile                 string
		useFederatedWorkloadIdentityExtension bool
		aadClientID                           string
		tenantID                              string
		expectedErr                           *testutil.TestError
	}{
		{
			desc:                  "out of cluster, no kubeconfig, no credential file",
			kubeclient:            nil,
			allowEmptyCloudConfig: true,
			expectedErr:           nil,
		},
		{
			desc:                  "[failure][disallowEmptyCloudConfig] out of cluster, no kubeconfig, no credential file",
			kubeclient:            nil,
			allowEmptyCloudConfig: false,
			expectedErr: &testutil.TestError{
				DefaultError: fmt.Errorf("no cloud config provided, error"),
			},
		},
		{
			desc:                  "[failure] out of cluster & in cluster, specify a non-exist kubeconfig, no credential file",
			kubeclient:            nil,
			allowEmptyCloudConfig: true,
			expectedErr:           nil,
		},
		{
			desc:                  "[failure] out of cluster & in cluster, specify a fake kubeconfig, no credential file",
			kubeclient:            fake.NewSimpleClientset(),
			allowEmptyCloudConfig: true,
			expectedErr:           nil,
		},
		{
			desc:                  "[success] out of cluster & in cluster, no kubeconfig, a fake credential file",
			createFakeCredFile:    true,
			kubeclient:            nil,
			userAgent:             "useragent",
			allowEmptyCloudConfig: true,
			expectedErr:           nil,
		},
		{
			desc:                                  "[success] get azure client with workload identity",
			createFakeCredFile:                    true,
			setFederatedWorkloadIdentityEnv:       true,
			kubeclient:                            fake.NewSimpleClientset(),
			userAgent:                             "useragent",
			useFederatedWorkloadIdentityExtension: true,
			aadFederatedTokenFile:                 "fake-token-file",
			aadClientID:                           "fake-client-id",
			tenantID:                              "fake-tenant-id",
			expectedErr:                           nil,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
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

			cloud, err := getCloudProvider(context.Background(), test.kubeclient, "", "", "", test.userAgent, test.allowEmptyCloudConfig)
			if test.expectedErr != nil {
				if err == nil {
					t.Errorf("desc: %s,\n input: %q, getCloudProvider err: %v, expectedErr: %v", test.desc, test.kubeclient, err, test.expectedErr)
				}
				if !testutil.AssertError(err, test.expectedErr) && !strings.Contains(err.Error(), test.expectedErr.DefaultError.Error()) {
					t.Errorf("desc: %s,\n input: %q, getCloudProvider err: %v, expectedErr: %v", test.desc, test.kubeclient, err, test.expectedErr)
				}
			}
			if cloud == nil {
				t.Errorf("return value of getCloudProvider should not be nil even there is error")
			} else {
				assert.Equal(t, test.userAgent, cloud.UserAgent)
				assert.Equal(t, cloud.AADFederatedTokenFile, test.aadFederatedTokenFile)
				assert.Equal(t, cloud.UseFederatedWorkloadIdentityExtension, test.useFederatedWorkloadIdentityExtension)
				assert.Equal(t, cloud.AADClientID, test.aadClientID)
				assert.Equal(t, cloud.TenantID, test.tenantID)
			}
		})
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

	config := azureconfig.Config{
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
			name: "[fail] subnet name is nil",
			testFunc: func(t *testing.T) {
				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(network.Subnet{}, nil).Times(1)
				mockSubnetClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)

				_, err := d.updateSubnetServiceEndpoints(ctx, "", "", "subnetname")
				expectedErr := fmt.Errorf("subnet name is nil")
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "[success] ServiceEndpoints is nil",
			testFunc: func(t *testing.T) {
				fakeSubnet := network.Subnet{
					SubnetPropertiesFormat: &network.SubnetPropertiesFormat{},
					Name:                   ptr.To("subnetName"),
				}

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).Times(1)
				_, err := d.updateSubnetServiceEndpoints(ctx, "", "", "subnetname")
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
					Name: ptr.To("subnetName"),
				}

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).AnyTimes()

				_, err := d.updateSubnetServiceEndpoints(ctx, "", "", "subnetname")
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
					Name: ptr.To("subnetName"),
				}

				mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).AnyTimes()

				_, err := d.updateSubnetServiceEndpoints(ctx, "", "", "subnetname")
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
				_, err := d.updateSubnetServiceEndpoints(ctx, "", "", "")
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
