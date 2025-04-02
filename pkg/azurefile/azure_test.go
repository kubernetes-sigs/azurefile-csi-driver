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
	"runtime"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/testutil"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/mock_azclient"

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/subnetclient/mock_subnetclient"
	azureconfig "sigs.k8s.io/cloud-provider-azure/pkg/provider/config"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/storage"
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

		cloud, _, err := getCloudProvider(context.Background(), test.kubeconfig, "", "", "", test.userAgent, test.allowEmptyCloudConfig, false, 5, 10)
		if test.expectedErr.DefaultError != nil && test.expectedErr.WindowsError != nil {
			if !testutil.AssertError(err, &test.expectedErr) && !strings.Contains(err.Error(), test.expectedErr.DefaultError.Error()) {
				t.Errorf("desc: %s,\n input: %q, getCloudProvider err: %v, expectedErr: %v", test.desc, test.kubeconfig, err, test.expectedErr)
			}
		}
		if cloud != nil {
			assert.Equal(t, test.userAgent, cloud.UserAgent)
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

var _ = ginkgo.Describe("AzureFile", func() {
	var mockSubnetClient *mock_subnetclient.MockInterface
	var mockClientFactory *mock_azclient.MockClientFactory
	var ctx context.Context
	var ctrl *gomock.Controller
	var d *Driver

	ginkgo.BeforeEach(func() {
		ctx = context.TODO()
		d = NewFakeDriver()
		ctrl = gomock.NewController(ginkgo.GinkgoT())
		mockSubnetClient = mock_subnetclient.NewMockInterface(ctrl)
		mockClientFactory = mock_azclient.NewMockClientFactory(ctrl)
		mockClientFactory.EXPECT().GetSubnetClient().Return(mockSubnetClient).AnyTimes()
		config := azureconfig.Config{
			ResourceGroup: "rg",
			Location:      "loc",
			VnetName:      "fake-vnet",
			SubnetName:    "fake-subnet",
		}

		var err error
		d.cloud, err = storage.NewRepository(config, &azclient.Environment{
			StorageEndpointSuffix: "fake-endpoint",
		}, nil, mockClientFactory, mockClientFactory)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Unexpected error:", err)
	})
	ginkgo.AfterEach(func() {
		ctrl.Finish()
	})
	ginkgo.It("[fail] subnet name is nil", func() {
		mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&armnetwork.Subnet{}, nil).Times(1)
		_, err := d.updateSubnetServiceEndpoints(ctx, "", "", "subnetname")
		expectedErr := fmt.Errorf("subnet name is nil")
		gomega.Expect(err).To(gomega.Equal(expectedErr), "Unexpected error:", err)
	})

	ginkgo.It("[success] ServiceEndpoints is nil", func() {
		fakeSubnet := &armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{},
			Name:       ptr.To("subnetName"),
		}

		mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).Times(1)
		mockSubnetClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)
		_, err := d.updateSubnetServiceEndpoints(ctx, "", "", "subnetname")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Unexpected error:", err)
	})
	ginkgo.It("[success] storageService does not exists", func() {
		fakeSubnet := &armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				ServiceEndpoints: []*armnetwork.ServiceEndpointPropertiesFormat{},
			},
			Name: ptr.To("subnetName"),
		}

		mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).AnyTimes()
		mockSubnetClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)

		_, err := d.updateSubnetServiceEndpoints(ctx, "", "", "subnetname")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Unexpected error:", err)

	})

	ginkgo.It("[success] storageService already exists", func() {
		fakeSubnet := &armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				ServiceEndpoints: []*armnetwork.ServiceEndpointPropertiesFormat{
					{
						Service: &storageService,
					},
				},
			},
			Name: ptr.To("subnetName"),
		}

		mockSubnetClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fakeSubnet, nil).AnyTimes()

		_, err := d.updateSubnetServiceEndpoints(ctx, "", "", "subnetname")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Unexpected error:", err)

	})

})

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
