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
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/gomega"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
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

	// wiClientID is set during BeforeSuite after WI configuration succeeds.
	wiClientID string
	// wiReady is closed when the background AAD OIDC cache warm-up finishes.
	wiReady = make(chan struct{})
	// errWISetup holds any error from the background WI warm-up goroutine.
	errWISetup error
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
					clientID, setupErr := setupWorkloadIdentity(ctx, wiCS, azureClient, creds)
					if setupErr != nil {
						log.Printf("WARNING: workload identity setup failed: %v", setupErr)
					} else {
						wiSetupSucceeded = true
						wiClientID = clientID
						// Start background AAD OIDC cache warm-up
						go func() {
							defer close(wiReady)
							bgCS, csErr := clientset.NewForConfig(kubeConfig)
							if csErr != nil {
								errWISetup = fmt.Errorf("failed to create kube client for WI warm-up: %v", csErr)
								log.Printf("WARNING: %v", errWISetup)
								return
							}
							if warmErr := waitForAADTokenExchange(context.Background(), bgCS, wiClientID, creds, 45*time.Minute); warmErr != nil {
								errWISetup = fmt.Errorf("AAD token exchange not ready: %v", warmErr)
								log.Printf("WARNING: WI background warm-up failed: %v", errWISetup)
							} else {
								log.Printf("AAD token exchange warm-up succeeded in background")
							}
						}()
					}
				}
			}
			if !wiSetupSucceeded {
				close(wiReady) // Unblock WI test immediately if setup failed
			}
		} else {
			close(wiReady) // No WI setup needed
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
func setupWorkloadIdentity(ctx context.Context, cs clientset.Interface, azureClient *azure.Client, creds *credentials.Credentials) (string, error) {
	log.Println("Setting up workload identity for e2e tests...")

	// Step 1: Discover OIDC issuer URL from kube-apiserver
	oidcIssuerURL, err := discoverOIDCIssuer(ctx, cs)
	if err != nil {
		return "", fmt.Errorf("failed to discover OIDC issuer: %v", err)
	}
	log.Printf("Discovered OIDC issuer URL: %s", oidcIssuerURL)

	// Step 2: Get node identity info (single call to avoid nondeterministic map iteration)
	identityInfo, err := azureClient.GetNodeIdentityInfo(ctx, creds.ResourceGroup)
	if err != nil {
		return "", fmt.Errorf("failed to get node identity: %v", err)
	}
	log.Printf("Node identity clientID: %s, principalID: %s", identityInfo.ClientID, identityInfo.PrincipalID)

	// Parse identity name and resource group from resource ID
	// Format: /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.ManagedIdentity/userAssignedIdentities/<name>
	parts := strings.Split(identityInfo.ResourceID, "/")
	if len(parts) < 9 || !strings.EqualFold(parts[3], "resourceGroups") || !strings.EqualFold(parts[7], "userAssignedIdentities") {
		return "", fmt.Errorf("invalid identity resource ID format (expected ARM managed identity resource ID): %s", identityInfo.ResourceID)
	}
	identityRG := parts[4]
	identityName := parts[8]
	log.Printf("Identity resource group: %s, name: %s", identityRG, identityName)

	// Step 3: Create federated identity credential
	subject := fmt.Sprintf("system:serviceaccount:%s:%s", wiServiceAccountNamespace, wiServiceAccountName)
	err = azureClient.CreateFederatedIdentityCredential(ctx, identityRG, identityName, wiFederatedCredentialName, oidcIssuerURL, subject)
	if err != nil {
		return "", fmt.Errorf("failed to create federated identity credential: %v", err)
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
				return "", fmt.Errorf("failed to get existing service account: %v", getErr)
			}
			existing.Labels = sa.Labels
			existing.Annotations = sa.Annotations
			_, err = cs.CoreV1().ServiceAccounts(wiServiceAccountNamespace).Update(ctx, existing, metav1.UpdateOptions{})
			if err != nil {
				return "", fmt.Errorf("failed to update service account: %v", err)
			}
		} else {
			return "", fmt.Errorf("failed to create service account: %v", err)
		}
	}
	log.Printf("Service account %s created/updated in namespace %s", wiServiceAccountName, wiServiceAccountNamespace)

	// Step 5: Assign Storage File Data SMB MI Admin role to identity
	err = azureClient.AssignRoleToIdentity(ctx, creds.ResourceGroup, identityInfo.PrincipalID, wiStorageFileDataSMBMIAdmin)
	if err != nil {
		return "", fmt.Errorf("failed to assign Storage File Data SMB MI Admin role: %v", err)
	}
	log.Printf("Assigned Storage File Data SMB MI Admin role to identity")

	// Step 6: Verify OIDC JWKS endpoint is accessible
	if err := waitForOIDCJWKS(oidcIssuerURL, 5*time.Minute); err != nil {
		return "", fmt.Errorf("OIDC JWKS not ready: %v", err)
	}

	// Step 7: Verify JWKS key match (detect and repair CAPZ key mismatch)
	if err := verifyJWKSKeyMatch(ctx, cs, oidcIssuerURL, creds); err != nil {
		return "", fmt.Errorf("OIDC JWKS key mismatch: %v", err)
	}

	return identityInfo.ClientID, nil
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

// waitForOIDCJWKS polls the OIDC issuer's JWKS endpoint until it returns a
// valid response containing at least one signing key.
func waitForOIDCJWKS(issuerURL string, timeout time.Duration) error {
	jwksURL := strings.TrimSuffix(issuerURL, "/") + "/openid/v1/jwks"
	log.Printf("Waiting up to %v for OIDC JWKS to be available at %s", timeout, jwksURL)

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(jwksURL) //nolint:gosec
		if err != nil {
			lastErr = fmt.Errorf("GET %s: %v", jwksURL, err)
			log.Printf("JWKS not ready: %v, retrying...", lastErr)
			time.Sleep(10 * time.Second)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("reading JWKS response: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("JWKS returned status %d", resp.StatusCode)
			log.Printf("JWKS not ready: %v, retrying...", lastErr)
			time.Sleep(10 * time.Second)
			continue
		}

		var jwks struct {
			Keys []json.RawMessage `json:"keys"`
		}
		if err := json.Unmarshal(body, &jwks); err != nil {
			lastErr = fmt.Errorf("parsing JWKS: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		if len(jwks.Keys) == 0 {
			lastErr = fmt.Errorf("JWKS contains no keys")
			log.Printf("JWKS not ready: %v, retrying...", lastErr)
			time.Sleep(10 * time.Second)
			continue
		}

		log.Printf("OIDC JWKS is ready with %d key(s)", len(jwks.Keys))
		return nil
	}
	return fmt.Errorf("timed out waiting for OIDC JWKS: %v", lastErr)
}

// verifyJWKSKeyMatch ensures the OIDC issuer's JWKS contains the signing key
// actually used by kube-apiserver. Detects and repairs CAPZ key mismatch.
func verifyJWKSKeyMatch(ctx context.Context, cs clientset.Interface, issuerURL string, creds *credentials.Credentials) error {
	tokenReq := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			Audiences:         []string{"api://AzureADTokenExchange"},
			ExpirationSeconds: int64Ptr(600),
		},
	}
	tokenResp, err := cs.CoreV1().ServiceAccounts("default").
		CreateToken(ctx, "default", tokenReq, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating SA token: %v", err)
	}

	tokenKid, err := extractJWTKid(tokenResp.Status.Token)
	if err != nil {
		return fmt.Errorf("extracting kid from SA token: %v", err)
	}
	log.Printf("SA token kid: %s", tokenKid)

	jwksURL := strings.TrimSuffix(issuerURL, "/") + "/openid/v1/jwks"
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(jwksURL) //nolint:gosec
	if err != nil {
		return fmt.Errorf("fetching JWKS from %s: %v", jwksURL, err)
	}
	blobJWKSBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("reading JWKS response: %v", err)
	}

	blobKids, err := extractJWKSKids(blobJWKSBody)
	if err != nil {
		return fmt.Errorf("parsing blob JWKS: %v", err)
	}
	log.Printf("Blob JWKS kids: %v", blobKids)

	found := false
	for _, kid := range blobKids {
		if kid == tokenKid {
			found = true
			break
		}
	}
	if found {
		log.Printf("JWKS key match verified: SA token kid %s found in blob JWKS", tokenKid)
		return nil
	}

	log.Printf("WARNING: JWKS key mismatch detected! SA token kid %s not in blob JWKS %v", tokenKid, blobKids)
	log.Printf("Fetching correct JWKS from kube-apiserver and re-uploading to blob storage...")

	result := cs.Discovery().RESTClient().Get().AbsPath("/openid/v1/jwks").Do(ctx)
	apiJWKS, err := result.Raw()
	if err != nil {
		return fmt.Errorf("fetching JWKS from apiserver: %v", err)
	}

	apiKids, err := extractJWKSKids(apiJWKS)
	if err != nil {
		return fmt.Errorf("parsing apiserver JWKS: %v", err)
	}
	log.Printf("Apiserver JWKS kids: %v", apiKids)

	apiHasKid := false
	for _, kid := range apiKids {
		if kid == tokenKid {
			apiHasKid = true
			break
		}
	}
	if !apiHasKid {
		return fmt.Errorf("apiserver JWKS does not contain token kid %s (has %v); cannot repair", tokenKid, apiKids)
	}

	storageAccount, blobServiceURL, err := extractStorageInfoFromIssuer(issuerURL)
	if err != nil {
		return fmt.Errorf("extracting storage info from issuer URL: %v", err)
	}
	log.Printf("OIDC storage account: %s, blob service: %s", storageAccount, blobServiceURL)

	if err := uploadJWKSToBlob(blobServiceURL, apiJWKS, creds); err != nil {
		return fmt.Errorf("uploading corrected JWKS: %v", err)
	}

	oidcConfig := fmt.Sprintf(`{"issuer":"%s","jwks_uri":"%s","response_types_supported":["id_token"],"subject_types_supported":["public"],"id_token_signing_alg_values_supported":["RS256"]}`,
		issuerURL, jwksURL)
	if err := uploadOIDCConfigToBlob(blobServiceURL, []byte(oidcConfig), creds); err != nil {
		log.Printf("WARNING: failed to upload openid-configuration: %v (non-fatal)", err)
	}

	log.Printf("Successfully re-uploaded JWKS to blob storage")
	return nil
}

func extractJWTKid(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}
	headerB64 := parts[0]
	if m := len(headerB64) % 4; m != 0 {
		headerB64 += strings.Repeat("=", 4-m)
	}
	headerBytes, err := base64.URLEncoding.DecodeString(headerB64)
	if err != nil {
		return "", fmt.Errorf("decoding JWT header: %v", err)
	}
	var header struct {
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return "", fmt.Errorf("parsing JWT header: %v", err)
	}
	if header.Kid == "" {
		return "", fmt.Errorf("JWT header has no kid")
	}
	return header.Kid, nil
}

func extractJWKSKids(jwksBody []byte) ([]string, error) {
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(jwksBody, &jwks); err != nil {
		return nil, fmt.Errorf("parsing JWKS: %v", err)
	}
	var kids []string
	for _, k := range jwks.Keys {
		if k.Kid != "" {
			kids = append(kids, k.Kid)
		}
	}
	return kids, nil
}

func extractStorageInfoFromIssuer(issuerURL string) (account string, blobServiceURL string, err error) {
	u, err := url.Parse(issuerURL)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(u.Hostname(), ".", 2)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("unexpected issuer hostname: %s", u.Hostname())
	}
	account = parts[0]
	hostSuffix := parts[1]
	webIdx := strings.Index(hostSuffix, "web.")
	if webIdx < 0 {
		return "", "", fmt.Errorf("unexpected issuer hostname (no 'web.' segment): %s", u.Hostname())
	}
	storageSuffix := hostSuffix[webIdx+4:]
	blobServiceURL = fmt.Sprintf("https://%s.blob.%s", account, storageSuffix)
	return account, blobServiceURL, nil
}

func getAzureCredential(creds *credentials.Credentials) (azcore.TokenCredential, error) {
	var cloudCfg cloud.Configuration
	switch strings.ToUpper(os.Getenv("AZURE_CLOUD_NAME")) {
	case "AZURECHINACLOUD":
		cloudCfg = cloud.AzureChina
	case "AZUREUSGOVERNMENTCLOUD":
		cloudCfg = cloud.AzureGovernment
	default:
		cloudCfg = cloud.AzurePublic
	}
	if host := os.Getenv("AZURE_AUTHORITY_HOST"); host != "" {
		cloudCfg.ActiveDirectoryAuthorityHost = host
	}

	if creds.AADFederatedTokenFile != "" {
		return azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
			ClientOptions: azcore.ClientOptions{Cloud: cloudCfg},
			TenantID:      creds.TenantID,
			ClientID:      creds.AADClientID,
			TokenFilePath: creds.AADFederatedTokenFile,
		})
	}
	return azidentity.NewClientSecretCredential(creds.TenantID, creds.AADClientID, creds.AADClientSecret,
		&azidentity.ClientSecretCredentialOptions{
			ClientOptions: azcore.ClientOptions{Cloud: cloudCfg},
		})
}

func uploadJWKSToBlob(blobServiceURL string, jwksBody []byte, creds *credentials.Credentials) error {
	azCred, err := getAzureCredential(creds)
	if err != nil {
		return fmt.Errorf("creating Azure credential: %v", err)
	}
	client, err := azblob.NewClient(blobServiceURL, azCred, nil)
	if err != nil {
		return fmt.Errorf("creating blob client: %v", err)
	}
	_, err = client.UploadBuffer(context.Background(), "$web", "openid/v1/jwks", jwksBody, nil)
	if err != nil {
		return fmt.Errorf("uploading JWKS blob: %v", err)
	}
	log.Printf("Uploaded corrected JWKS (%d bytes) to %s/$web/openid/v1/jwks", len(jwksBody), blobServiceURL)
	return nil
}

func uploadOIDCConfigToBlob(blobServiceURL string, configBody []byte, creds *credentials.Credentials) error {
	azCred, err := getAzureCredential(creds)
	if err != nil {
		return fmt.Errorf("creating Azure credential: %v", err)
	}
	client, err := azblob.NewClient(blobServiceURL, azCred, nil)
	if err != nil {
		return fmt.Errorf("creating blob client: %v", err)
	}
	_, err = client.UploadBuffer(context.Background(), "$web", ".well-known/openid-configuration", configBody, nil)
	return err
}

// waitForAADTokenExchange polls AAD until token exchange succeeds for the WI service account.
func waitForAADTokenExchange(ctx context.Context, cs clientset.Interface, clientID string, creds *credentials.Credentials, timeout time.Duration) error {
	log.Printf("Waiting up to %v for AAD to accept token exchange for client %s", timeout, clientID)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		tokenReq := &authv1.TokenRequest{
			Spec: authv1.TokenRequestSpec{
				Audiences:         []string{"api://AzureADTokenExchange"},
				ExpirationSeconds: int64Ptr(600),
			},
		}
		tokenResp, err := cs.CoreV1().ServiceAccounts(wiServiceAccountNamespace).
			CreateToken(ctx, wiServiceAccountName, tokenReq, metav1.CreateOptions{})
		if err != nil {
			lastErr = fmt.Errorf("creating SA token: %v", err)
			log.Printf("Token exchange check: %v, retrying...", lastErr)
			time.Sleep(15 * time.Second)
			continue
		}

		saToken := tokenResp.Status.Token

		tenantID := creds.TenantID
		if tenantID == "" {
			tenantID = os.Getenv("AZURE_TENANT_ID")
		}
		if tenantID == "" {
			return fmt.Errorf("tenant ID not available from credentials or environment")
		}

		authorityHost := os.Getenv("AZURE_AUTHORITY_HOST")
		if authorityHost == "" {
			switch strings.ToUpper(os.Getenv("AZURE_CLOUD_NAME")) {
			case "AZURECHINACLOUD":
				authorityHost = "https://login.chinacloudapi.cn"
			case "AZUREUSGOVERNMENTCLOUD":
				authorityHost = "https://login.microsoftonline.us"
			default:
				authorityHost = "https://login.microsoftonline.com"
			}
		}

		tokenURL := fmt.Sprintf("%s/%s/oauth2/v2.0/token", strings.TrimSuffix(authorityHost, "/"), tenantID)
		data := fmt.Sprintf(
			"grant_type=client_credentials&client_id=%s&scope=https://storage.azure.com/.default"+
				"&client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer"+
				"&client_assertion=%s",
			clientID, saToken)

		resp, err := httpClient.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data)) //nolint:gosec,noctx
		if err != nil {
			lastErr = fmt.Errorf("AAD token request: %v", err)
			log.Printf("Token exchange check: %v, retrying...", lastErr)
			time.Sleep(15 * time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			log.Printf("AAD token exchange succeeded")
			return nil
		}

		bodyStr := string(body)
		if strings.Contains(bodyStr, "AADSTS7000272") {
			lastErr = fmt.Errorf("AAD returned AADSTS7000272")
			log.Printf("Token exchange check: AADSTS7000272, retrying in 15s...")
			time.Sleep(15 * time.Second)
			continue
		}

		lastErr = fmt.Errorf("AAD returned status %d: %s", resp.StatusCode, bodyStr)
		log.Printf("Token exchange check: %v, retrying...", lastErr)
		time.Sleep(15 * time.Second)
	}
	return fmt.Errorf("timed out waiting for AAD token exchange: %v", lastErr)
}

func int64Ptr(i int64) *int64 { return &i }
