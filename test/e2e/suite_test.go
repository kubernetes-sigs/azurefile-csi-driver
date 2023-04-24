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
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/gomega"
	"github.com/pborman/uuid"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/config"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/azure"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/credentials"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/testutil"
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
	isUsingInTreeVolumePlugin      = os.Getenv(driver.AzureDriverNameVar) == inTreeStorageClass
	isTestingMigration             = os.Getenv(testMigrationEnvVar) != ""
	isWindowsCluster               = os.Getenv(testWindowsEnvVar) != ""
	isCapzTest                     = os.Getenv("NODE_MACHINE_TYPE") != ""
	winServerVer                   = os.Getenv(testWinServerVerEnvVar)
	bringKeyStorageClassParameters = map[string]string{
		"csi.storage.k8s.io/provisioner-secret-namespace": "default",
		"csi.storage.k8s.io/node-stage-secret-namespace":  "default",
	}
	supportZRSwithNFS bool
)

type testCmd struct {
	command  string
	args     []string
	startLog string
	endLog   string
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
	handleFlags()
	framework.AfterReadingAllFlags(&framework.TestContext)

	// Default storage driver configuration is CSI. Freshly built
	// CSI driver is installed for that case.
	if testutil.IsRunningInProw() && (isTestingMigration || !isUsingInTreeVolumePlugin) {
		creds, err := credentials.CreateAzureCredentialFile(false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		azureClient, err := azure.GetAzureClient(creds.Cloud, creds.SubscriptionID, creds.AADClientID, creds.TenantID, creds.AADClientSecret)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = azureClient.EnsureResourceGroup(ctx, creds.ResourceGroup, creds.Location, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// check whether current region supports Premium_ZRS with NFS protocol
		supportedRegions := []string{"southeastasia", "australiaeast", "europenorth", "europewest", "francecentral", "japaneast", "uksouth", "useast", "useast2", "uswest2"}
		for _, region := range supportedRegions {
			if creds.Location == region {
				supportZRSwithNFS = true
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
		}
		azurefileDriver = azurefile.NewDriver(&driverOptions)
		go func() {
			os.Setenv("AZURE_CREDENTIAL_FILE", credentials.TempAzureCredentialFilePath)
			azurefileDriver.Run(fmt.Sprintf("unix:///tmp/csi-%s.sock", uuid.NewUUID().String()), kubeconfig, false)
		}()
	}
})

var _ = ginkgo.AfterSuite(func(ctx ginkgo.SpecContext) {
	if testutil.IsRunningInProw() {
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
				command:  "bash",
				args:     []string{"test/utils/azurefile_log.sh"},
				startLog: "===================azurefile log===================",
				endLog:   "===================================================",
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

				// uninstall CSI Driver deployment scripts test
				uninstallDriver := testCmd{
					command:  "bash",
					args:     []string{"deploy/uninstall-driver.sh", "master", "windows,local"},
					startLog: "===================uninstall CSI Driver deployment scripts test===================",
					endLog:   "===================================================",
				}
				execTestCmd([]testCmd{installDriver, uninstallDriver})
			}

			checkAccountCreationLeak(ctx)

			err := credentials.DeleteAzureCredentialFile()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}
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
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		log.Println(cmd.endLog)
	}
}

func checkAccountCreationLeak(ctx context.Context) {
	creds, err := credentials.CreateAzureCredentialFile(false)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	azureClient, err := azure.GetAzureClient(creds.Cloud, creds.SubscriptionID, creds.AADClientID, creds.TenantID, creds.AADClientSecret)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	accountNum, err := azureClient.GetAccountNumByResourceGroup(ctx, creds.ResourceGroup)
	framework.ExpectNoError(err, fmt.Sprintf("failed to GetAccountNumByResourceGroup(%s): %v", creds.ResourceGroup, err))
	ginkgo.By(fmt.Sprintf("GetAccountNumByResourceGroup(%s) returns %d accounts", creds.ResourceGroup, accountNum))

	accountLimitInTest := 13
	framework.ExpectEqual(accountNum >= accountLimitInTest, false, fmt.Sprintf("current account num %d should not exceed %d", accountNum, accountLimitInTest))
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
func handleFlags() {
	config.CopyFlags(config.Flags, flag.CommandLine)
	framework.RegisterCommonFlags(flag.CommandLine)
	framework.RegisterClusterFlags(flag.CommandLine)
	flag.Parse()
}
