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

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/gomega"
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
	suiteCreds                     *credentials.Credentials
	suiteAzureClient               *azure.Client
)

type testCmd struct {
	command     string
	args        []string
	startLog    string
	endLog      string
	ignoreError bool
}

// suiteSetup carries the cluster-wide setup results from the first Ginkgo
// process to every parallel process.
type suiteSetup struct {
	ResourceGroup        string
	Location             string
	MIRoleSetupSucceeded bool
}

// Cluster and Azure fixtures are created once; each parallel process then
// initializes its own clients, region capabilities and in-process driver.
var _ = ginkgo.SynchronizedBeforeSuite(func(ctx ginkgo.SpecContext) []byte {
	log.Println(driver.AzureDriverNameVar, os.Getenv(driver.AzureDriverNameVar), fmt.Sprintf("%v", isUsingInTreeVolumePlugin))
	log.Println(testMigrationEnvVar, os.Getenv(testMigrationEnvVar), fmt.Sprintf("%v", isTestingMigration))
	log.Println(testWindowsEnvVar, os.Getenv(testWindowsEnvVar), fmt.Sprintf("%v", isWindowsCluster))
	log.Println(testWinServerVerEnvVar, os.Getenv(testWinServerVerEnvVar), fmt.Sprintf("%v", winServerVer))

	setDefaultKubeconfig()
	setup := suiteSetup{}
	// Default storage driver configuration is CSI. Freshly built
	// CSI driver is installed for that case.
	if isTestingMigration || !isUsingInTreeVolumePlugin {
		creds, err := credentials.CreateAzureCredentialFile(false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		azureClient, err := azure.GetAzureClient(creds.Cloud, creds.SubscriptionID, creds.AADClientID, creds.TenantID, creds.AADClientSecret, creds.AADFederatedTokenFile)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		setup.ResourceGroup = creds.ResourceGroup
		setup.Location = creds.Location
		_, err = azureClient.EnsureResourceGroup(ctx, creds.ResourceGroup, creds.Location, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Assign Storage File Data SMB MI Admin role to node identities and
		// set up workload identity for mountWithManagedIdentity/mountWithWorkloadIdentityToken e2e tests (CAPZ Linux only)
		if isCapzTest && !isWindowsCluster && !isUsingInTreeVolumePlugin && !isTestingMigration {
			err := azureClient.EnsureNodeStorageFileDataRole(ctx, creds.ResourceGroup)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to assign Storage File Data SMB MI Admin role to node identity")
			setup.MIRoleSetupSucceeded = true
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
	}
	data, err := json.Marshal(setup)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return data
}, func(ctx ginkgo.SpecContext, data []byte) {
	setDefaultKubeconfig()
	if !isTestingMigration && isUsingInTreeVolumePlugin {
		return
	}
	var setup suiteSetup
	gomega.Expect(json.Unmarshal(data, &setup)).To(gomega.Succeed())
	// Every process must use the first process's resource group, including a
	// generated one, and the same credential file contents.
	for name, value := range map[string]string{"AZURE_RESOURCE_GROUP": setup.ResourceGroup, "AZURE_LOCATION": setup.Location} {
		if os.Getenv(name) == "" {
			os.Setenv(name, value)
		}
	}
	creds, err := credentials.CreateAzureCredentialFile(false)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	azureClient, err := azure.GetAzureClient(creds.Cloud, creds.SubscriptionID, creds.AADClientID, creds.TenantID, creds.AADClientSecret, creds.AADFederatedTokenFile)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	suiteCreds = creds
	suiteAzureClient = azureClient
	miRoleSetupSucceeded = setup.MIRoleSetupSucceeded

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
})

func setDefaultKubeconfig() {
	// k8s.io/kubernetes/test/e2e/framework requires env KUBECONFIG to be set
	// it does not fall back to defaults
	if os.Getenv(kubeconfigEnvVar) == "" {
		kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		os.Setenv(kubeconfigEnvVar, kubeconfig)
	}
}

// Cluster teardown and leak checks run once after every parallel process finishes.
var _ = ginkgo.SynchronizedAfterSuite(func() {}, func(ctx ginkgo.SpecContext) {
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

	accountLimitInTest := 22
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
