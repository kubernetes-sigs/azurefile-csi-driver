/*
Copyright 2019 The Kubernetes Authors.

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

package e2e

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/config"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/azure"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/credentials"
)

const (
	kubeconfigEnvVar       = "KUBECONFIG"
	reportDirEnv           = "ARTIFACTS"
	testMigrationEnvVar    = "TEST_MIGRATION"
	testWindowsEnvVar      = "TEST_WINDOWS"
	testWinServerVerEnvVar = "WINDOWS_SERVER_VERSION"
	defaultReportDir       = "/workspace/_artifacts"
	inTreeStorageClass     = "kubernetes.io/azure-file"
)

var (
	azurefileDriver                *azurefile.Driver
	isUsingInTreeVolumePlugin      = (os.Getenv(driver.AzureDriverNameVar) == inTreeStorageClass || os.Getenv("runInTreeVolumeTestsOnly") != "")
	isTestingMigration             = os.Getenv(testMigrationEnvVar) != ""
	isWindowsCluster               = os.Getenv(testWindowsEnvVar) != ""
	isCapzTest                     = os.Getenv("NODE_MACHINE_TYPE") != ""
	winServerVer                   = os.Getenv(testWinServerVerEnvVar)
	bringKeyStorageClassParameters = map[string]string{
		"csi.storage.k8s.io/provisioner-secret-namespace": "default",
		"csi.storage.k8s.io/node-stage-secret-namespace":  "default",
	}
	supportZRSwithNFS              bool
	supportSnapshotwithNFS         bool
	supportEncryptInTransitwithNFS bool
	miRoleSetupSucceeded           bool
	wiSetupSucceeded               bool
	oauthTokenSetupSucceeded       bool
)

type testCmd struct {
	command     string
	args        []string
	startLog    string
	endLog      string
	ignoreError bool
}

var _ = ginkgo.BeforeSuite(func(ctx ginkgo.SpecContext) {
	log.Println(driver.AzureDriverNameVar, os.Getenv(driver.AzureDriverNameVar), fmt.Sprintf("%v", isUsingInTreeVolumePlugin))
	log.Println(testMigrationEnvVar, os.Getenv(testMigrationEnvVar), fmt.Sprintf("%v", isTestingMigration))
	log.Println(testWindowsEnvVar, os.Getenv(testWindowsEnvVar), fmt.Sprintf("%v", isWindowsCluster))
	log.Println(testWinServerVerEnvVar, os.Getenv(testWinServerVerEnvVar), fmt.Sprintf("%v", winServerVer))

	// k8s.io/kubernetes/test/e2e/framework requires env KUBECONFIG to be set
	// it does not fall back to defaults
	if os.Getenv(kubeconfigEnvVar) == "" {
		kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		os.Setenv(kubeconfigEnvVar, kubeconfig)
	}

	// Default storage driver configuration is CSI. Freshly built
	// CSI driver is installed for that case.
	if isTestingMigration || !isUsingInTreeVolumePlugin {
		creds, err := credentials.CreateAzureCredentialFile(false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		azureClient, err := azure.GetAzureClient(creds.Cloud, creds.SubscriptionID, creds.AADClientID, creds.TenantID, creds.AADClientSecret, creds.AADFederatedTokenFile)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = azureClient.EnsureResourceGroup(ctx, creds.ResourceGroup, creds.Location, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Assign Storage File Data SMB MI Admin role to node identities
		// This is required for mountWithManagedIdentity e2e tests (CAPZ only)
		if isCapzTest {
			err := azureClient.EnsureNodeStorageFileDataRole(ctx, creds.ResourceGroup)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to assign Storage File Data SMB MI Admin role to node identity")
			miRoleSetupSucceeded = true
		}

		// Set up workload identity for mountWithWorkloadIdentityToken e2e tests (CAPZ only)
		if isCapzTest {
			kubeConfig, err := framework.LoadConfig()
			if err != nil {
				log.Printf("WARNING: failed to load kubeconfig for WI setup: %v", err)
			} else {
				wiCS, err := clientset.NewForConfig(kubeConfig)
				if err != nil {
					log.Printf("WARNING: failed to create clientset for WI setup: %v", err)
				} else {
					if err := setupWorkloadIdentity(ctx, wiCS, azureClient, creds); err != nil {
						log.Printf("WARNING: workload identity setup failed: %v", err)
					} else {
						wiSetupSucceeded = true
					}
				}
			}
		}

		// check whether current region supports Premium_ZRS with NFS protocol
		supportedRegions := []string{"southeastasia", "australiaeast", "europenorth", "europewest", "francecentral", "japaneast", "uksouth", "useast", "useast2", "uswest2"}
		for _, region := range supportedRegions {
			if creds.Location == region {
				supportZRSwithNFS = true
			}
		}

		// check whether current region supports snapshot with NFS protocol
		supportedRegions = []string{"canadacentral", "uksouth", "francesouth", "francecentral", "germanywestcentral"}
		for _, region := range supportedRegions {
			if creds.Location == region {
				supportSnapshotwithNFS = true
			}
		}

		// check whether current region supports encryptInTransit with NFS protocol
		supportedRegions = []string{"canadacentral", "canadaeast", "southeastasia", "eastasia", "australiaeast", "europenorth", "francecentral", "francesouth", "germanywestcentral", "uksouth", "ukwest", "uswest", "uswest2", "uswest3", "ussouth", "ussouth2", "australiacentral", "australiacentral2"}
		for _, region := range supportedRegions {
			if creds.Location == region {
				supportEncryptInTransitwithNFS = true
				log.Printf("region %s supports encryptInTransit with NFS protocol", region)
			}
		}

		// Install Azure File CSI Driver on cluster from project root
		e2eBootstrap := testCmd{
			command:  "make",
			args:     []string{"e2e-bootstrap"},
			startLog: "Installing Azure File CSI Driver...",
			endLog:   "Azure File CSI Driver installed",
		}

		createMetricsSVC := testCmd{
			command:  "make",
			args:     []string{"create-metrics-svc"},
			startLog: "create metrics service ...",
			endLog:   "metrics service created",
		}
		execTestCmd([]testCmd{e2eBootstrap, createMetricsSVC})

		if !isTestingMigration {
			// Install SMB provisioner on cluster
			installSMBProvisioner := testCmd{
				command:  "make",
				args:     []string{"install-smb-provisioner"},
				startLog: "Installing SMB provisioner...",
				endLog:   "SMB provisioner installed",
			}
			execTestCmd([]testCmd{installSMBProvisioner})
		}

		kubeconfig := os.Getenv(kubeconfigEnvVar)
		driverOptions := azurefile.DriverOptions{
			NodeID:     os.Getenv("nodeid"),
			DriverName: azurefile.DefaultDriverName,
			Endpoint:   fmt.Sprintf("unix:///tmp/csi-%s.sock", uuid.NewString()),
			KubeConfig: kubeconfig,
		}
		azurefileDriver = azurefile.NewDriver(&driverOptions)
		go func() {
			os.Setenv("AZURE_CREDENTIAL_FILE", credentials.TempAzureCredentialFilePath)
			err := azurefileDriver.Run(context.Background())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}()

		// Setup OAuth token for mountWithOAuthToken e2e test (CAPZ only)
		if isCapzTest {
			err = setupOAuthToken(ctx, creds, azureClient)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "OAuth token setup failed")
			oauthTokenSetupSucceeded = true
			log.Println("OAuth token setup succeeded")
		}
	}
})

var _ = ginkgo.AfterSuite(func(ctx ginkgo.SpecContext) {
	if isTestingMigration || isUsingInTreeVolumePlugin {
		cmLog := testCmd{
			command:  "bash",
			args:     []string{"test/utils/controller-manager-log.sh"},
			startLog: "===================controller-manager log=======",
			endLog:   "===================================================",
		}
		execTestCmd([]testCmd{cmLog})
	}
	if isTestingMigration || !isUsingInTreeVolumePlugin {
		checkPodsRestart := testCmd{
			command:  "bash",
			args:     []string{"test/utils/check_driver_pods_restart.sh", "log"},
			startLog: "Check driver pods if restarts ...",
			endLog:   "Check successfully",
		}
		execTestCmd([]testCmd{checkPodsRestart})

		os := "linux"
		if isWindowsCluster {
			os = "windows"
			if winServerVer == "windows-2022" {
				os = winServerVer
			}
		}
		createExampleDeployment := testCmd{
			command:  "bash",
			args:     []string{"hack/verify-examples.sh", os},
			startLog: "create example deployments",
			endLog:   "example deployments created",
		}
		execTestCmd([]testCmd{createExampleDeployment})

		azurefileLog := testCmd{
			command:     "bash",
			args:        []string{"test/utils/azurefile_log.sh"},
			startLog:    "===================azurefile log===================",
			endLog:      "===================================================",
			ignoreError: true,
		}
		e2eTeardown := testCmd{
			command:  "make",
			args:     []string{"e2e-teardown"},
			startLog: "Uninstalling Azure File CSI Driver...",
			endLog:   "Azure File CSI Driver uninstalled",
		}
		execTestCmd([]testCmd{azurefileLog, e2eTeardown})

		if !isTestingMigration {
			// install CSI Driver deployment scripts test
			installDriver := testCmd{
				command:  "bash",
				args:     []string{"deploy/install-driver.sh", "master", "windows,local"},
				startLog: "===================install CSI Driver deployment scripts test===================",
				endLog:   "===================================================",
			}

			createExampleDeployment := testCmd{
				command:  "bash",
				args:     []string{"hack/verify-examples.sh", os},
				startLog: "create example deployments#2",
				endLog:   "example deployments#2 created",
			}
			execTestCmd([]testCmd{createExampleDeployment})

			execTestCmd([]testCmd{installDriver})
		}

		checkAccountCreationLeak(ctx)

		err := credentials.DeleteAzureCredentialFile()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
})

func TestE2E(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	reportDir := os.Getenv(reportDirEnv)
	if reportDir == "" {
		reportDir = defaultReportDir
	}
	r := []ginkgo.Reporter{reporters.NewJUnitReporter(path.Join(reportDir, "junit_01.xml"))}
	ginkgo.RunSpecsWithDefaultAndCustomReporters(t, "AzureFile CSI Driver End-to-End Tests", r)
}

func execTestCmd(cmds []testCmd) {
	err := os.Chdir("../..")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer func() {
		err := os.Chdir("test/e2e")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}()

	projectRoot, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(strings.HasSuffix(projectRoot, "azurefile-csi-driver")).To(gomega.Equal(true))

	for _, cmd := range cmds {
		log.Println(cmd.startLog)
		cmdSh := exec.Command(cmd.command, cmd.args...)
		cmdSh.Dir = projectRoot
		cmdSh.Stdout = os.Stdout
		cmdSh.Stderr = os.Stderr
		err = cmdSh.Run()
		if err != nil {
			log.Println(err)
			if !cmd.ignoreError {
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			}
		}
		log.Println(cmd.endLog)
	}
}

func checkAccountCreationLeak(_ context.Context) {
	creds, err := credentials.CreateAzureCredentialFile(false)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	azureClient, err := azure.GetAzureClient(creds.Cloud, creds.SubscriptionID, creds.AADClientID, creds.TenantID, creds.AADClientSecret, creds.AADFederatedTokenFile)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	accountNum, err := azureClient.GetAccountNumByResourceGroup(context.Background(), creds.ResourceGroup)
	if err != nil {
		framework.Logf("failed to GetAccountNumByResourceGroup(%s): %v", creds.ResourceGroup, err)
		return
	}
	ginkgo.By(fmt.Sprintf("GetAccountNumByResourceGroup(%s) returns %d accounts", creds.ResourceGroup, accountNum))

	accountLimitInTest := 17
	gomega.Expect(accountNum >= accountLimitInTest).To(gomega.BeFalse())
}

func skipIfTestingInWindowsCluster() {
	if isWindowsCluster {
		ginkgo.Skip("test case not supported by Windows clusters")
	}
}

func skipIfUsingInTreeVolumePlugin() {
	if isUsingInTreeVolumePlugin {
		log.Println("test case is only available for CSI drivers")
		ginkgo.Skip("test case is only available for CSI drivers")
	}
}

func skipIfTestingInMigrationCluster() {
	if isTestingMigration {
		ginkgo.Skip("test case not supported by Migration clusters")
	}
}

func convertToPowershellCommandIfNecessary(command string) string {
	if !isWindowsCluster {
		return command
	}

	switch command {
	case "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data":
		return "echo 'hello world' | Out-File -FilePath C:\\mnt\\test-1\\data.txt; Get-Content C:\\mnt\\test-1\\data.txt | findstr 'hello world'"
	case "touch /mnt/test-1/data":
		return "echo $null >> C:\\mnt\\test-1\\data"
	case "while true; do echo $(date -u) >> /mnt/test-1/data; sleep 100; done":
		return "while (1) { Add-Content -Encoding Unicode C:\\mnt\\test-1\\data.txt $(Get-Date -Format u); sleep 1 }"
	case "echo 'hello world' >> /mnt/test-1/data && while true; do sleep 100; done":
		return "Add-Content -Encoding Unicode C:\\mnt\\test-1\\data.txt 'hello world'; while (1) { sleep 1 }"
	case "echo 'hello world' >> /mnt/test-1/data && while true; do sleep 3600; done":
		return "Add-Content -Encoding Unicode C:\\mnt\\test-1\\data.txt 'hello world'; while (1) { sleep 1 }"
	}

	return command
}

// handleFlags sets up all flags and parses the command line.
func TestMain(m *testing.M) {
	config.CopyFlags(config.Flags, flag.CommandLine)
	framework.RegisterCommonFlags(flag.CommandLine)
	framework.RegisterClusterFlags(flag.CommandLine)
	framework.AfterReadingAllFlags(&framework.TestContext)
	flag.Parse()
	os.Exit(m.Run())
}

const (
	wiServiceAccountName        = "azurefile-wi-test-sa"
	wiServiceAccountNamespace   = "default"
	wiFederatedCredentialName   = "azurefile-e2e-wi-fic"
	wiStorageFileDataSMBMIAdmin = "a235d3ee-5935-4cfb-8cc5-a3303ad5995e" // Storage File Data SMB MI Admin role GUID
)

// setupWorkloadIdentity configures workload identity for e2e tests:
// 1. Discovers the OIDC issuer URL from kube-apiserver
// 2. Gets the node identity client ID and principal ID
// 3. Creates a federated identity credential
// 4. Creates a Kubernetes service account with WI annotation
// 5. Assigns Storage File Data SMB MI Admin role to the identity
func setupWorkloadIdentity(ctx context.Context, cs clientset.Interface, azureClient *azure.Client, creds *credentials.Credentials) error {
	log.Println("Setting up workload identity for e2e tests...")

	// Step 1: Discover OIDC issuer URL from kube-apiserver
	oidcIssuerURL, err := discoverOIDCIssuer(ctx, cs)
	if err != nil {
		return fmt.Errorf("failed to discover OIDC issuer: %v", err)
	}
	log.Printf("Discovered OIDC issuer URL: %s", oidcIssuerURL)

	// Step 2: Get node identity info (single call to avoid nondeterministic map iteration)
	identityInfo, err := azureClient.GetNodeIdentityInfo(ctx, creds.ResourceGroup)
	if err != nil {
		return fmt.Errorf("failed to get node identity: %v", err)
	}
	log.Printf("Node identity clientID: %s, principalID: %s", identityInfo.ClientID, identityInfo.PrincipalID)

	// Parse identity name and resource group from resource ID
	// Format: /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.ManagedIdentity/userAssignedIdentities/<name>
	parts := strings.Split(identityInfo.ResourceID, "/")
	if len(parts) < 9 || !strings.EqualFold(parts[3], "resourceGroups") || !strings.EqualFold(parts[7], "userAssignedIdentities") {
		return fmt.Errorf("invalid identity resource ID format (expected ARM managed identity resource ID): %s", identityInfo.ResourceID)
	}
	identityRG := parts[4]
	identityName := parts[8]
	log.Printf("Identity resource group: %s, name: %s", identityRG, identityName)

	// Step 3: Create federated identity credential
	subject := fmt.Sprintf("system:serviceaccount:%s:%s", wiServiceAccountNamespace, wiServiceAccountName)
	err = azureClient.CreateFederatedIdentityCredential(ctx, identityRG, identityName, wiFederatedCredentialName, oidcIssuerURL, subject)
	if err != nil {
		return fmt.Errorf("failed to create federated identity credential: %v", err)
	}
	log.Printf("Federated identity credential created")

	// Step 4: Create or update Kubernetes service account with WI annotation
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      wiServiceAccountName,
			Namespace: wiServiceAccountNamespace,
			Labels: map[string]string{
				"azure.workload.identity/use": "true",
			},
			Annotations: map[string]string{
				"azure.workload.identity/client-id": identityInfo.ClientID,
				"azure.workload.identity/tenant-id": creds.TenantID,
			},
		},
	}
	_, err = cs.CoreV1().ServiceAccounts(wiServiceAccountNamespace).Create(ctx, sa, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Printf("Service account %s already exists, updating...", wiServiceAccountName)
			existing, getErr := cs.CoreV1().ServiceAccounts(wiServiceAccountNamespace).Get(ctx, wiServiceAccountName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("failed to get existing service account: %v", getErr)
			}
			existing.Labels = sa.Labels
			existing.Annotations = sa.Annotations
			_, err = cs.CoreV1().ServiceAccounts(wiServiceAccountNamespace).Update(ctx, existing, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update service account: %v", err)
			}
		} else {
			return fmt.Errorf("failed to create service account: %v", err)
		}
	}
	log.Printf("Service account %s created/updated in namespace %s", wiServiceAccountName, wiServiceAccountNamespace)

	// Step 5: Assign Storage File Data SMB MI Admin role to identity
	err = azureClient.AssignRoleToIdentity(ctx, creds.ResourceGroup, identityInfo.PrincipalID, wiStorageFileDataSMBMIAdmin)
	if err != nil {
		return fmt.Errorf("failed to assign Storage File Data SMB MI Admin role: %v", err)
	}
	log.Printf("Assigned Storage File Data SMB MI Admin role to identity")

	return nil
}

// discoverOIDCIssuer retrieves the OIDC issuer URL from the kube-apiserver's
// well-known OpenID configuration endpoint.
func discoverOIDCIssuer(ctx context.Context, cs clientset.Interface) (string, error) {
	result := cs.Discovery().RESTClient().Get().AbsPath("/.well-known/openid-configuration").Do(ctx)
	body, err := result.Raw()
	if err != nil {
		return "", fmt.Errorf("failed to get OIDC configuration: %v", err)
	}

	var oidcConfig struct {
		Issuer string `json:"issuer"`
	}
	if err := json.Unmarshal(body, &oidcConfig); err != nil {
		return "", fmt.Errorf("failed to parse OIDC configuration: %v", err)
	}
	if oidcConfig.Issuer == "" {
		return "", fmt.Errorf("OIDC issuer URL is empty in cluster configuration")
	}
	return oidcConfig.Issuer, nil
}

const (
	oauthSecretName      = "azure-oauth-token-secret"
	oauthSecretNamespace = "default"
)

// setupOAuthToken obtains an OAuth token from the node's managed identity and stores it
// in a Kubernetes Secret for use by mountWithOAuthToken e2e tests.
func setupOAuthToken(ctx context.Context, creds *credentials.Credentials, azureClient *azure.Client) error {
	// Step 1: Get node managed identity info (uses Azure SDK with SP creds, works from Prow)
	identityInfo, err := azureClient.GetNodeIdentityInfo(ctx, creds.ResourceGroup)
	if err != nil {
		return fmt.Errorf("failed to get node identity info: %v", err)
	}
	log.Printf("Found node identity: clientID=%s, principalID=%s", identityInfo.ClientID, identityInfo.PrincipalID)

	// Step 2: Assign Storage File Data SMB MI Admin role to identity
	err = azureClient.AssignRoleToIdentity(ctx, creds.ResourceGroup, identityInfo.PrincipalID, wiStorageFileDataSMBMIAdmin)
	if err != nil {
		return fmt.Errorf("failed to assign Storage File Data SMB MI Admin role: %v", err)
	}
	log.Println("Storage File Data SMB MI Admin role assigned to node identity")

	// Step 3: Get OAuth token by running a pod on the workload cluster node.
	// The test runner (Prow pod) cannot access IMDS (169.254.169.254) because it runs
	// outside Azure VMs. Instead, we schedule a pod on an agent node that curls IMDS
	// to obtain the token, then read it from the pod logs.
	kubeconfig := os.Getenv(kubeconfigEnvVar)
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to build kubeconfig: %v", err)
	}
	cs, err := clientset.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	token, err := getOAuthTokenFromNode(ctx, cs, identityInfo.ClientID)
	if err != nil {
		return fmt.Errorf("failed to get storage OAuth token from node: %v", err)
	}
	log.Println("Obtained storage OAuth token from agent node via IMDS")

	// Step 4: Create Kubernetes Secret with the OAuth token
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      oauthSecretName,
			Namespace: oauthSecretNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"oauthtoken": []byte(token),
		},
	}
	_, err = cs.CoreV1().Secrets(oauthSecretNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			_, err = cs.CoreV1().Secrets(oauthSecretNamespace).Update(ctx, secret, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update OAuth token secret: %v", err)
			}
		} else {
			return fmt.Errorf("failed to create OAuth token secret: %v", err)
		}
	}
	log.Printf("Created/updated OAuth token secret %s/%s", oauthSecretNamespace, oauthSecretName)

	return nil
}

// getOAuthTokenFromNode deploys a pod on a workload cluster agent node that uses IMDS
// to obtain an Azure Storage OAuth token, then reads the token from the pod logs.
func getOAuthTokenFromNode(ctx context.Context, cs clientset.Interface, clientID string) (string, error) {
	namespace := "default"

	// IMDS curl command that outputs only the access_token value
	curlCmd := fmt.Sprintf(
		`curl -s -H "Metadata: true" "http://169.254.169.254/metadata/identity/oauth2/token?api-version=2018-02-01&client_id=%s&resource=https://storage.azure.com" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4`,
		clientID,
	)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "oauth-token-fetcher-",
			Namespace:    namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			// Use hostNetwork to ensure IMDS is accessible
			HostNetwork: true,
			Containers: []corev1.Container{
				{
					Name:    "token-fetcher",
					Image:   "mcr.microsoft.com/cbl-mariner/base/core:2.0",
					Command: []string{"/bin/sh", "-c", curlCmd},
				},
			},
			// Schedule on agent nodes only (not control plane — no managed identity there)
			NodeSelector: map[string]string{
				"kubernetes.io/os": "linux",
			},
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "node-role.kubernetes.io/control-plane",
										Operator: corev1.NodeSelectorOpDoesNotExist,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	created, err := cs.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create token fetcher pod: %v", err)
	}
	podName := created.Name
	defer func() {
		_ = cs.CoreV1().Pods(namespace).Delete(context.Background(), podName, metav1.DeleteOptions{})
	}()

	// Wait for pod to complete (up to 5 minutes)
	log.Printf("Waiting for token fetcher pod %s/%s to complete...", namespace, podName)
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = waitForPodComplete(waitCtx, cs, namespace, podName)
	if err != nil {
		return "", fmt.Errorf("token fetcher pod did not complete: %v", err)
	}

	// Read token from pod logs
	logBytes, err := cs.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{}).Do(ctx).Raw()
	if err != nil {
		return "", fmt.Errorf("failed to get token fetcher pod logs: %v", err)
	}

	token := strings.TrimSpace(string(logBytes))
	if token == "" {
		return "", fmt.Errorf("token fetcher pod returned empty token")
	}

	return token, nil
}

// waitForPodComplete polls until the pod reaches Succeeded or Failed phase.
func waitForPodComplete(ctx context.Context, cs clientset.Interface, namespace, name string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pod, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			return fmt.Errorf("pod failed with reason: %s", pod.Status.Reason)
		}

		time.Sleep(3 * time.Second)
	}
}
