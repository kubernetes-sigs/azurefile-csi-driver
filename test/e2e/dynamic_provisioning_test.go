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
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	clientset "k8s.io/client-go/kubernetes"
	restclientset "k8s.io/client-go/rest"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
)

var _ = ginkgo.Describe("Dynamic Provisioning", func() {
	f := framework.NewDefaultFramework("azurefile")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var (
		cs          clientset.Interface
		ns          *v1.Namespace
		snapshotrcs restclientset.Interface
		testDriver  driver.PVTestDriver
	)

	ginkgo.BeforeEach(func(ctx ginkgo.SpecContext) {
		checkPodsRestart := testCmd{
			command:  "bash",
			args:     []string{"test/utils/check_driver_pods_restart.sh"},
			startLog: "Check driver pods if restarts ...",
			endLog:   "Check successfully",
		}
		execTestCmd([]testCmd{checkPodsRestart})

		cs = f.ClientSet
		ns = f.Namespace

		var err error
		snapshotrcs, err = restClient(testsuites.SnapshotAPIGroup, testsuites.APIVersionv1)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("could not get rest clientset: %v", err))
		}
	}, ginkgo.NodeTimeout(time.Hour*2))

	testDriver = driver.InitAzureFileDriver()

	ginkgo.It("should create a pod, write and read to it, take a volume snapshot, and create another pod from the snapshot [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
			Volumes: []testsuites.VolumeDetails{
				{
					ClaimSize: "100Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
		}
		podWithSnapshot := testsuites.PodDetails{
			Cmd: "grep 'hello world' /mnt/test-1/data",
		}
		test := testsuites.DynamicallyProvisionedVolumeSnapshotTest{
			CSIDriver:              testDriver,
			Pod:                    pod,
			ShouldOverwrite:        false,
			ShouldRestore:          true,
			PodWithSnapshot:        podWithSnapshot,
			StorageClassParameters: map[string]string{"skuName": "Standard_LRS"},
		}
		test.Run(ctx, cs, snapshotrcs, ns)
	})

	ginkgo.It("should create a pod, write to its pv, take a volume snapshot, overwrite data in original pv, create another pod from the snapshot, use another storage class, and read unaltered original data from original pv[file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pod := testsuites.PodDetails{
			IsWindows:    isWindowsCluster,
			WinServerVer: winServerVer,
			Cmd:          "echo 'hello world' > /mnt/test-1/data",
			Volumes: []testsuites.VolumeDetails{
				{
					ClaimSize: "100Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
		}

		podOverwrite := testsuites.PodDetails{
			IsWindows:    isWindowsCluster,
			WinServerVer: winServerVer,
			Cmd:          "echo 'overwrite' > /mnt/test-1/data; sleep 3600",
		}

		podWithSnapshot := testsuites.PodDetails{
			IsWindows:    isWindowsCluster,
			WinServerVer: winServerVer,
			Cmd:          "grep 'hello world' /mnt/test-1/data",
		}

		test := testsuites.DynamicallyProvisionedVolumeSnapshotTest{
			CSIDriver:                      testDriver,
			Pod:                            pod,
			ShouldOverwrite:                true,
			ShouldRestore:                  true,
			PodOverwrite:                   podOverwrite,
			PodWithSnapshot:                podWithSnapshot,
			StorageClassParameters:         map[string]string{"skuName": "Standard_LRS"},
			SnapshotStorageClassParameters: map[string]string{"skuName": "Premium_LRS"},
		}
		test.Run(ctx, cs, snapshotrcs, ns)
	})

	ginkgo.It("should create a storage account with tags [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		// Because the pv object created by kubernetes.io/azure-file does not contain storage account name, skip the test with in-tree volume plugin.
		skipIfUsingInTreeVolumePlugin()

		volumes := []testsuites.VolumeDetails{
			{
				ClaimSize: "100Gi",
				VolumeMount: testsuites.VolumeMountDetails{
					NameGenerate:      "test-volume-",
					MountPathGenerate: "/mnt/test-",
				},
			},
		}
		tags := "account=azurefile-test"
		test := testsuites.DynamicallyProvisionedAccountWithTags{
			CSIDriver: testDriver,
			Volumes:   volumes,
			StorageClassParameters: map[string]string{
				"skuName": "Premium_LRS",
				"tags":    tags,
				// make sure this is the first test case due to storeAccountKey is set as false
				"storeAccountKey":        "false",
				"getLatestAccountKey":    "true",
				"shareAccessTier":        "Premium",
				"requireInfraEncryption": "true",
			},
			Tags: tags,
		}

		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a volume on demand with mount options [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						MountOptions: []string{
							"dir_mode=0777",
							"file_mode=0777",
							"uid=0",
							"gid=0",
							"mfsymlinks",
							"cache=strict",
							"nosharesock",
							"vers=3.1.1",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}

		scParameters := map[string]string{
			"skuName":         "Standard_LRS",
			"secretNamespace": "kube-system",
		}
		if !isUsingInTreeVolumePlugin {
			scParameters["secretName"] = fmt.Sprintf("secret-%d", time.Now().Unix())
			scParameters["enableLargeFileshares"] = "true"
			scParameters["networkEndpointType"] = "privateEndpoint"
			scParameters["accessTier"] = "Hot"
			scParameters["selectRandomMatchingAccount"] = "true"
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a smb multi-channel volume with max_channels options [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		if !isCapzTest {
			ginkgo.Skip("test case is only available for capz test")
		}
		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "100Gi",
						MountOptions: []string{
							"max_channels=2",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}

		scParameters := map[string]string{
			"skuName":                     "Premium_LRS",
			"enableMultichannel":          "true",
			"selectRandomMatchingAccount": "true",
			"accountQuota":                "200",
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a pod with volume mount subpath [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}

		scParameters := map[string]string{
			"skuName":               "Standard_LRS",
			"secretNamespace":       "kube-system",
			"accessTier":            "Cool",
			"allowBlobPublicAccess": "false",
			"shareNamePrefix":       "fileprefix",
		}
		test := testsuites.DynamicallyProvisionedVolumeSubpathTester{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create multiple PV objects, bind to PVCs and attach all to different pods on the same node [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("while true; do echo $(date -u) >> /mnt/test-1/data; sleep 100; done"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
			{
				Cmd: convertToPowershellCommandIfNecessary("while true; do echo $(date -u) >> /mnt/test-1/data; sleep 100; done"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		scParameters := map[string]string{
			"skuName":         "Standard_LRS",
			"secretNamespace": "default",
		}
		if !isUsingInTreeVolumePlugin {
			scParameters["allowBlobPublicAccess"] = "false"
			scParameters["accessTier"] = "TransactionOptimized"
			scParameters["matchTags"] = "true"
			scParameters["shareName"] = "sharename-${pvc.metadata.name}"
		}
		test := testsuites.DynamicallyProvisionedCollocatedPodTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			ColocatePods:           true,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	// Track issue https://github.com/kubernetes/kubernetes/issues/70505
	ginkgo.It("should create a volume on demand and mount it as readOnly in a pod [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("touch /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						FSType:    "ext4",
						ClaimSize: "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
							ReadOnly:          true,
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		scParameters := map[string]string{
			"skuName": "Standard_GRS",
		}
		if !isUsingInTreeVolumePlugin {
			scParameters["allowBlobPublicAccess"] = "true"
			scParameters["shareAccessTier"] = "Cool"
		}
		test := testsuites.DynamicallyProvisionedReadOnlyVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a deployment object, write and read to it, delete the pod and write and read to it again [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		pod := testsuites.PodDetails{
			Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' >> /mnt/test-1/data && while true; do sleep 100; done"),
			Volumes: []testsuites.VolumeDetails{
				{
					FSType:    "ext3",
					ClaimSize: "10Gi",
					MountOptions: []string{
						"cache=none",
					},
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
			IsWindows:    isWindowsCluster,
			WinServerVer: winServerVer,
		}

		podCheckCmd := []string{"cat", "/mnt/test-1/data"}
		expectedString := "hello world\n"
		if isWindowsCluster {
			podCheckCmd = []string{"powershell.exe", "-Command", "Get-Content C:\\mnt\\test-1\\data.txt"}
			expectedString = "hello world\r\n"
		}
		test := testsuites.DynamicallyProvisionedDeletePodTest{
			CSIDriver: testDriver,
			Pod:       pod,
			PodCheck: &testsuites.PodExecCheck{
				Cmd:            podCheckCmd,
				ExpectedString: expectedString, // pod will be restarted so expect to see 2 instances of string
			},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It(fmt.Sprintf("should delete PV with reclaimPolicy %q [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", v1.PersistentVolumeReclaimDelete), func(ctx ginkgo.SpecContext) {
		reclaimPolicy := v1.PersistentVolumeReclaimDelete
		volumes := []testsuites.VolumeDetails{
			{
				FSType:        "ext4",
				ClaimSize:     "10Gi",
				ReclaimPolicy: &reclaimPolicy,
			},
		}
		scParameters := map[string]string{
			"skuName": "Standard_RAGRS",
		}
		if !isUsingInTreeVolumePlugin {
			scParameters["accessTier"] = ""
			scParameters["accountAccessTier"] = "Hot"
		}
		test := testsuites.DynamicallyProvisionedReclaimPolicyTest{
			CSIDriver:              testDriver,
			Volumes:                volumes,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It(fmt.Sprintf("should retain PV with reclaimPolicy %q [file.csi.azure.com] [Windows]", v1.PersistentVolumeReclaimRetain), func(ctx ginkgo.SpecContext) {
		// This tests uses the CSI driver to delete the PV.
		// TODO: Go via the k8s interfaces and also make it more reliable for in-tree and then
		//       test can be enabled.
		skipIfUsingInTreeVolumePlugin()

		reclaimPolicy := v1.PersistentVolumeReclaimRetain
		volumes := []testsuites.VolumeDetails{
			{
				FSType:        "ext4",
				ClaimSize:     "10Gi",
				ReclaimPolicy: &reclaimPolicy,
			},
		}
		test := testsuites.DynamicallyProvisionedReclaimPolicyTest{
			CSIDriver: testDriver,
			Volumes:   volumes,
			Azurefile: azurefileDriver,
			StorageClassParameters: map[string]string{
				"skuName": "Premium_LRS",
			},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a volume on demand and resize it [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}

		scParameters := map[string]string{
			"skuName": "Standard_LRS",
		}
		if !isUsingInTreeVolumePlugin {
			scParameters["accountAccessTier"] = "Cool"
		}
		test := testsuites.DynamicallyProvisionedResizeVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should receive FailedMount event with invalid mount options [file.csi.azure.com] [disk]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pods := []testsuites.PodDetails{
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						MountOptions: []string{
							"invalid",
							"mount",
							"options",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedInvalidMountOptions{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: map[string]string{"skuName": "Premium_LRS", "fsType": "ext4"},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should receive FailedMount event with invalid mount options [file.csi.azure.com] [disk]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pods := []testsuites.PodDetails{
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						MountOptions: []string{
							"invalid",
							"mount",
							"options",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedInvalidMountOptions{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: map[string]string{"skuName": "Premium_LRS", "fsType": "ext4"},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create multiple PV objects, bind to PVCs and attach all to different pods on the same node [file.csi.azure.com][disk]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pods := []testsuites.PodDetails{
			{
				Cmd: "while true; do echo $(date -u) >> /mnt/test-1/data; sleep 100; done",
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
			{
				Cmd: convertToPowershellCommandIfNecessary("while true; do echo $(date -u) >> /mnt/test-1/data; sleep 100; done"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		test := testsuites.DynamicallyProvisionedCollocatedPodTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			ColocatePods:           true,
			StorageClassParameters: map[string]string{"skuName": "Premium_LRS"},
		}
		test.Run(ctx, cs, ns)
	})

	// Track issue https://github.com/kubernetes/kubernetes/issues/70505
	ginkgo.It("should create a vhd disk volume on demand and mount it as readOnly in a pod [file.csi.azure.com][disk]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pods := []testsuites.PodDetails{
			{
				Cmd: "touch /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						FSType:    "ext4",
						ClaimSize: "10Gi",
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
							ReadOnly:          true,
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedReadOnlyVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: map[string]string{"skuName": "Premium_LRS", "fsType": "ext3"},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a deployment object, write and read to it, delete the pod and write and read to it again [file.csi.azure.com] [disk]", func(ctx ginkgo.SpecContext) {
		ginkgo.Skip("test case is disabled due to controller.attachRequired is disabled by default now")
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' >> /mnt/test-1/data && while true; do sleep 100; done",
			Volumes: []testsuites.VolumeDetails{
				{
					FSType:    "ext3",
					ClaimSize: "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedDeletePodTest{
			CSIDriver: testDriver,
			Pod:       pod,
			PodCheck: &testsuites.PodExecCheck{
				Cmd:            []string{"cat", "/mnt/test-1/data"},
				ExpectedString: "hello world\n",
			},
			StorageClassParameters: map[string]string{"skuName": "Standard_LRS", "fsType": "xfs"},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It(fmt.Sprintf("should delete PV with reclaimPolicy %q [file.csi.azure.com] [disk]", v1.PersistentVolumeReclaimDelete), func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		reclaimPolicy := v1.PersistentVolumeReclaimDelete
		volumes := []testsuites.VolumeDetails{
			{
				FSType:        "ext4",
				ClaimSize:     "10Gi",
				ReclaimPolicy: &reclaimPolicy,
			},
		}
		test := testsuites.DynamicallyProvisionedReclaimPolicyTest{
			CSIDriver:              testDriver,
			Volumes:                volumes,
			StorageClassParameters: map[string]string{"skuName": "Standard_RAGRS", "fsType": "ext2"},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It(fmt.Sprintf("[env] should retain PV with reclaimPolicy %q [file.csi.azure.com] [disk]", v1.PersistentVolumeReclaimRetain), func(ctx ginkgo.SpecContext) {
		// This tests uses the CSI driver to delete the PV.
		// TODO: Go via the k8s interfaces and also make it more reliable for in-tree and then
		//       test can be enabled.
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		reclaimPolicy := v1.PersistentVolumeReclaimRetain
		volumes := []testsuites.VolumeDetails{
			{
				FSType:        "ext4",
				ClaimSize:     "10Gi",
				ReclaimPolicy: &reclaimPolicy,
			},
		}
		test := testsuites.DynamicallyProvisionedReclaimPolicyTest{
			CSIDriver:              testDriver,
			Volumes:                volumes,
			Azurefile:              azurefileDriver,
			StorageClassParameters: map[string]string{"skuName": "Premium_LRS", "fsType": "xfs"},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a pod with multiple volumes [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()

		volumes := []testsuites.VolumeDetails{}
		for i := 1; i <= 6; i++ {
			volume := testsuites.VolumeDetails{
				ClaimSize: "100Gi",
				VolumeMount: testsuites.VolumeMountDetails{
					NameGenerate:      "test-volume-",
					MountPathGenerate: "/mnt/test-",
				},
			}
			volumes = append(volumes, volume)
		}

		pods := []testsuites.PodDetails{
			{
				Cmd:          convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes:      volumes,
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		test := testsuites.DynamicallyProvisionedPodWithMultiplePVsTest{
			CSIDriver: testDriver,
			Pods:      pods,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a pod with multiple volumes with accountQuota setting [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()

		volumes := []testsuites.VolumeDetails{}
		for i := 1; i <= 6; i++ {
			volume := testsuites.VolumeDetails{
				ClaimSize: "100Gi",
				VolumeMount: testsuites.VolumeMountDetails{
					NameGenerate:      "test-volume-",
					MountPathGenerate: "/mnt/test-",
				},
			}
			volumes = append(volumes, volume)
		}

		pods := []testsuites.PodDetails{
			{
				Cmd:          convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes:      volumes,
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		test := testsuites.DynamicallyProvisionedPodWithMultiplePVsTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: map[string]string{"skuName": "Premium_LRS", "accountQuota": "200"},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a pod, write and read to it, take a standard smb volume snapshot, and validate whether it is ready to use [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfTestingInWindowsCluster()
		skipIfUsingInTreeVolumePlugin()

		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' > /mnt/test-1/data",
			Volumes: []testsuites.VolumeDetails{
				{
					ClaimSize: "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
		}
		podWithSnapshot := testsuites.PodDetails{
			Cmd: "grep 'hello world' /mnt/test-1/data",
		}
		test := testsuites.DynamicallyProvisionedVolumeSnapshotTest{
			CSIDriver:       testDriver,
			Pod:             pod,
			ShouldOverwrite: false,
			ShouldRestore:   false,
			PodWithSnapshot: podWithSnapshot,
			StorageClassParameters: map[string]string{
				"skuName":                     "Standard_LRS",
				"selectRandomMatchingAccount": "true",
			},
		}
		test.Run(ctx, cs, snapshotrcs, ns)
	})

	ginkgo.It("should create a pod, write and read to it, take a premium smb volume snapshot, and validate whether it is ready to use [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfTestingInWindowsCluster()
		skipIfUsingInTreeVolumePlugin()

		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' > /mnt/test-1/data",
			Volumes: []testsuites.VolumeDetails{
				{
					ClaimSize: "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
		}
		podWithSnapshot := testsuites.PodDetails{
			Cmd: "grep 'hello world' /mnt/test-1/data",
		}
		test := testsuites.DynamicallyProvisionedVolumeSnapshotTest{
			CSIDriver:       testDriver,
			Pod:             pod,
			PodWithSnapshot: podWithSnapshot,
			StorageClassParameters: map[string]string{
				"skuName":                     "Premium_LRS",
				"selectRandomMatchingAccount": "true",
			},
		}
		test.Run(ctx, cs, snapshotrcs, ns)
	})

	ginkgo.It("should create a pod, write and read to it, take a nfs volume snapshot, and validate whether it is ready to use [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfTestingInWindowsCluster()
		skipIfUsingInTreeVolumePlugin()
		if !supportSnapshotwithNFS {
			ginkgo.Skip("take snapshot on nfs file share is not supported on current region")
		}

		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' > /mnt/test-1/data",
			Volumes: []testsuites.VolumeDetails{
				{
					ClaimSize: "100Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
		}
		podWithSnapshot := testsuites.PodDetails{
			Cmd: "grep 'hello world' /mnt/test-1/data",
		}
		test := testsuites.DynamicallyProvisionedVolumeSnapshotTest{
			CSIDriver:       testDriver,
			Pod:             pod,
			PodWithSnapshot: podWithSnapshot,
			StorageClassParameters: map[string]string{
				"skuName":                     "Premium_LRS",
				"selectRandomMatchingAccount": "true",
				"protocol":                    "nfs",
			},
		}
		test.Run(ctx, cs, snapshotrcs, ns)
	})

	ginkgo.It("should create a volume on demand with mount options (Bring Your Own Key) [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		// get storage account secret name
		err := os.Chdir("../..")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer func() {
			err := os.Chdir("test/e2e")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}()

		getSecretNameScript := "test/utils/get_storage_account_secret_name.sh"
		log.Printf("run script: %s\n", getSecretNameScript)

		cmd := exec.Command("bash", getSecretNameScript)
		output, err := cmd.CombinedOutput()
		log.Printf("got output: %v, error: %v\n", string(output), err)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		secretName := strings.TrimSuffix(string(output), "\n")
		log.Printf("got storage account secret name: %v\n", secretName)
		bringKeyStorageClassParameters["csi.storage.k8s.io/provisioner-secret-name"] = secretName
		bringKeyStorageClassParameters["csi.storage.k8s.io/node-stage-secret-name"] = secretName

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "100Gi",
						MountOptions: []string{
							"dir_mode=0777",
							"file_mode=0777",
							"uid=0",
							"gid=0",
							"mfsymlinks",
							"cache=strict",
							"nosharesock",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: bringKeyStorageClassParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a Premium_LRS volume on demand with useDataPlaneAPI [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						MountOptions: []string{
							"dir_mode=0777",
							"file_mode=0777",
							"uid=0",
							"gid=0",
							"mfsymlinks",
							"cache=strict",
							"nosharesock",
							"vers=3.1.1",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}

		scParameters := map[string]string{
			"skuName":                      "Premium_LRS",
			"secretNamespace":              "kube-system",
			"createAccount":                "true",
			"useDataPlaneAPI":              "true",
			"disableDeleteRetentionPolicy": "true",
			"accountAccessTier":            "Premium",
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a Standard_LRS volume on demand with disableDeleteRetentionPolicy [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						MountOptions: []string{
							"dir_mode=0777",
							"file_mode=0777",
							"uid=0",
							"gid=0",
							"mfsymlinks",
							"cache=strict",
							"nosharesock",
							"vers=3.1.1",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}

		scParameters := map[string]string{
			"skuName":                      "Standard_LRS",
			"secretNamespace":              "kube-system",
			"createAccount":                "true",
			"useDataPlaneAPI":              "true",
			"disableDeleteRetentionPolicy": "true",
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a statefulset object, write and read to it, delete the pod and write and read to it again [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()

		pod := testsuites.PodDetails{
			Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' >> /mnt/test-1/data && while true; do sleep 3600; done"),
			Volumes: []testsuites.VolumeDetails{
				{
					FSType:    "ext4",
					ClaimSize: "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
			IsWindows:    isWindowsCluster,
			WinServerVer: winServerVer,
			UseCMD:       false,
		}
		podCheckCmd := []string{"cat", "/mnt/test-1/data"}
		expectedString := "hello world\n"
		if isWindowsCluster {
			podCheckCmd = []string{"cmd", "/c", "type C:\\mnt\\test-1\\data.txt"}
			expectedString = "hello world\r\n"
		}
		test := testsuites.DynamicallyProvisionedStatefulSetTest{
			CSIDriver: testDriver,
			Pod:       pod,
			PodCheck: &testsuites.PodExecCheck{
				Cmd:            podCheckCmd,
				ExpectedString: expectedString, // pod will be restarted so expect to see 2 instances of string
			},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should be able to unmount smb volume if volume is already deleted [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()

		pod := testsuites.PodDetails{
			Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' >> /mnt/test-1/data && while true; do sleep 3600; done"),
			Volumes: []testsuites.VolumeDetails{
				{
					ClaimSize: "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
			IsWindows:    isWindowsCluster,
			WinServerVer: winServerVer,
			UseCMD:       false,
		}
		podCheckCmd := []string{"cat", "/mnt/test-1/data"}
		expectedString := "hello world\n"
		if isWindowsCluster {
			podCheckCmd = []string{"cmd", "/c", "type C:\\mnt\\test-1\\data.txt"}
			expectedString = "hello world\r\n"
		}
		test := testsuites.DynamicallyProvisionedVolumeUnmountTest{
			CSIDriver: testDriver,
			Azurefile: azurefileDriver,
			Pod:       pod,
			PodCheck: &testsuites.PodExecCheck{
				Cmd:            podCheckCmd,
				ExpectedString: expectedString, // pod will be restarted so expect to see 2 instances of string
			},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should be able to unmount nfs volume if volume is already deleted [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pod := testsuites.PodDetails{
			Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' >> /mnt/test-1/data && while true; do sleep 3600; done"),
			Volumes: []testsuites.VolumeDetails{
				{
					ClaimSize: "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
			IsWindows:    isWindowsCluster,
			WinServerVer: winServerVer,
			UseCMD:       false,
		}
		podCheckCmd := []string{"cat", "/mnt/test-1/data"}
		expectedString := "hello world\n"
		if isWindowsCluster {
			podCheckCmd = []string{"cmd", "/c", "type C:\\mnt\\test-1\\data.txt"}
			expectedString = "hello world\r\n"
		}
		test := testsuites.DynamicallyProvisionedVolumeUnmountTest{
			CSIDriver: testDriver,
			Azurefile: azurefileDriver,
			Pod:       pod,
			PodCheck: &testsuites.PodExecCheck{
				Cmd:            podCheckCmd,
				ExpectedString: expectedString, // pod will be restarted so expect to see 2 instances of string
			},
			StorageClassParameters: map[string]string{
				"protocol": "nfs",
			},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create an CSI inline volume [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()

		// get storage account secret name
		err := os.Chdir("../..")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer func() {
			err := os.Chdir("test/e2e")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}()

		getSecretNameScript := "test/utils/get_storage_account_secret_name.sh"
		log.Printf("run script: %s\n", getSecretNameScript)

		cmd := exec.Command("bash", getSecretNameScript)
		output, err := cmd.CombinedOutput()
		log.Printf("got output: %v, error: %v\n", string(output), err)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		secretName := strings.TrimSuffix(string(output), "\n")
		log.Printf("got storage account secret name: %v\n", secretName)
		segments := strings.Split(secretName, "-")
		if len(segments) != 5 {
			ginkgo.Fail(fmt.Sprintf("%s have %d elements, expected: %d ", secretName, len(segments), 5))
		}
		accountName := segments[3]

		shareName := "csi-inline-smb-volume"
		req := makeCreateVolumeReq(shareName, ns.Name)
		req.Parameters["storageAccount"] = accountName
		resp, err := azurefileDriver.CreateVolume(ctx, req)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("create volume error: %v", err))
		}
		volumeID := resp.Volume.VolumeId
		ginkgo.By(fmt.Sprintf("Successfully provisioned AzureFile volume: %q\n", volumeID))

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "100Gi",
						MountOptions: []string{
							"uid=0",
							"gid=0",
							"mfsymlinks",
							"cache=strict",
							"nosharesock",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}

		test := testsuites.DynamicallyProvisionedInlineVolumeTest{
			CSIDriver:       testDriver,
			Pods:            pods,
			SecretName:      secretName,
			ShareName:       shareName,
			ReadOnly:        false,
			CSIInlineVolume: true,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create an inline volume by in-tree driver [kubernetes.io/azure-file]", func(ctx ginkgo.SpecContext) {
		if !isTestingMigration {
			ginkgo.Skip("test case is only available for migration test")
		}
		// get storage account secret name
		err := os.Chdir("../..")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer func() {
			err := os.Chdir("test/e2e")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}()

		getSecretNameScript := "test/utils/get_storage_account_secret_name.sh"
		log.Printf("run script: %s\n", getSecretNameScript)

		cmd := exec.Command("bash", getSecretNameScript)
		output, err := cmd.CombinedOutput()
		log.Printf("got output: %v, error: %v\n", string(output), err)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		secretName := strings.TrimSuffix(string(output), "\n")
		log.Printf("got storage account secret name: %v\n", secretName)
		segments := strings.Split(secretName, "-")
		if len(segments) != 5 {
			ginkgo.Fail(fmt.Sprintf("%s have %d elements, expected: %d ", secretName, len(segments), 5))
		}
		accountName := segments[3]

		shareName := "intree-inline-smb-volume"
		req := makeCreateVolumeReq("intree-inline-smb-volume", ns.Name)
		req.Parameters["storageAccount"] = accountName
		resp, err := azurefileDriver.CreateVolume(ctx, req)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("create volume error: %v", err))
		}
		volumeID := resp.Volume.VolumeId
		ginkgo.By(fmt.Sprintf("Successfully provisioned AzureFile volume: %q\n", volumeID))

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						MountOptions: []string{
							"dir_mode=0777",
							"file_mode=0777",
							"uid=0",
							"gid=0",
							"mfsymlinks",
							"cache=strict",
							"nosharesock",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}

		test := testsuites.DynamicallyProvisionedInlineVolumeTest{
			CSIDriver:  testDriver,
			Pods:       pods,
			SecretName: secretName,
			ShareName:  shareName,
			ReadOnly:   false,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should mount on-prem smb server [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		if isWindowsCluster && isCapzTest {
			ginkgo.Skip("test case is not available for capz Windows test")
		}

		secretName := "smbcreds"
		ginkgo.By(fmt.Sprintf("creating secret %s in namespace %s", secretName, ns.Name))
		secreteData := map[string]string{"azurestorageaccountname": "USERNAME"}
		secreteData["azurestorageaccountkey"] = "PASSWORD"
		tsecret := testsuites.NewTestSecret(f.ClientSet, ns, secretName, secreteData)
		tsecret.Create(ctx)
		defer tsecret.Cleanup(ctx)

		server := "smb-server.default.svc.cluster.local"
		if isWindowsCluster {
			err := os.Chdir("../..")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			defer func() {
				err := os.Chdir("test/e2e")
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			}()

			getSMBPublicIPScript := "test/utils/get_smb_svc_public_ip.sh"
			log.Printf("run script: %s\n", getSMBPublicIPScript)

			cmd := exec.Command("bash", getSMBPublicIPScript)
			output, err := cmd.CombinedOutput()
			log.Printf("got output: %v, error: %v\n", string(output), err)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			server = strings.TrimSuffix(string(output), "\n")
			log.Printf("use server on Windows: %s\n", server)
		}

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "10Gi",
						MountOptions: []string{
							"dir_mode=0777",
							"file_mode=0777",
							"uid=0",
							"gid=0",
							"mfsymlinks",
							"cache=strict",
							"nosharesock",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}

		test := testsuites.DynamicallyProvisionedInlineVolumeTest{
			CSIDriver:       testDriver,
			Pods:            pods,
			SecretName:      secretName,
			Server:          server,
			ShareName:       "share",
			ReadOnly:        false,
			CSIInlineVolume: true,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a NFS volume on demand with mount options [file.csi.azure.com] [nfs]", func(ctx ginkgo.SpecContext) {
		skipIfTestingInWindowsCluster()
		skipIfUsingInTreeVolumePlugin()

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "100Gi",
						MountOptions: []string{
							"nconnect=4",
							"rsize=1048576",
							"wsize=1048576",
							"noresvport",
							"actimeo=30",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver: testDriver,
			Pods:      pods,
			StorageClassParameters: map[string]string{
				"skuName":          "Premium_LRS",
				"protocol":         "nfs",
				"rootSquashType":   "RootSquash",
				"mountPermissions": "0755",
			},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a NFS volume on demand on a storage account with private endpoint [file.csi.azure.com] [nfs]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "100Gi",
						MountOptions: []string{
							"nconnect=4",
							"rsize=1048576",
							"wsize=1048576",
							"noresvport",
							"actimeo=30",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		scParameters := map[string]string{
			"protocol":            "nfs",
			"networkEndpointType": "privateEndpoint",
			"skuName":             "Premium_LRS",
			"rootSquashType":      "AllSquash",
			"mountPermissions":    "0",
			"fsGroupChangePolicy": "Always",
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: scParameters,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should create a pod with multiple NFS volumes [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfTestingInWindowsCluster()
		skipIfUsingInTreeVolumePlugin()

		volumes := []testsuites.VolumeDetails{}
		for i := 1; i <= 6; i++ {
			volume := testsuites.VolumeDetails{
				ClaimSize: "100Gi",
				VolumeMount: testsuites.VolumeMountDetails{
					NameGenerate:      "test-volume-",
					MountPathGenerate: "/mnt/test-",
				},
			}
			volumes = append(volumes, volume)
		}

		pods := []testsuites.PodDetails{
			{
				Cmd:          convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes:      volumes,
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		test := testsuites.DynamicallyProvisionedPodWithMultiplePVsTest{
			CSIDriver: testDriver,
			Pods:      pods,
			StorageClassParameters: map[string]string{
				"protocol":         "nfs",
				"rootSquashType":   "NoRootSquash",
				"mountPermissions": "0777",
			},
		}
		if supportZRSwithNFS {
			test.StorageClassParameters["skuName"] = "Premium_ZRS"
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("smb volume mount is still valid after driver restart [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()

		// print azure file driver logs before driver restart
		azurefileLog := testCmd{
			command:     "bash",
			args:        []string{"test/utils/azurefile_log.sh"},
			startLog:    "===================azurefile log (before restart)===================",
			endLog:      "====================================================================",
			ignoreError: true,
		}
		execTestCmd([]testCmd{azurefileLog})

		pod := testsuites.PodDetails{
			Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' >> /mnt/test-1/data && while true; do sleep 3600; done"),
			Volumes: []testsuites.VolumeDetails{
				{
					FSType:    "ext3",
					ClaimSize: "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
			IsWindows:    isWindowsCluster,
			WinServerVer: winServerVer,
		}

		podCheckCmd := []string{"cat", "/mnt/test-1/data"}
		expectedString := "hello world\n"
		if isWindowsCluster {
			podCheckCmd = []string{"powershell.exe", "-Command", "Get-Content C:\\mnt\\test-1\\data.txt"}
			expectedString = "hello world\r\n"
		}
		test := testsuites.DynamicallyProvisionedRestartDriverTest{
			CSIDriver: testDriver,
			Pod:       pod,
			PodCheck: &testsuites.PodExecCheck{
				Cmd:            podCheckCmd,
				ExpectedString: expectedString,
			},
			StorageClassParameters: map[string]string{"skuName": "Standard_LRS"},
			RestartDriverFunc: func() {
				restartDriver := testCmd{
					command:  "bash",
					args:     []string{"test/utils/restart_driver_daemonset.sh"},
					startLog: "Restart driver node daemonset ...",
					endLog:   "Restart driver node daemonset done successfully",
				}
				execTestCmd([]testCmd{restartDriver})
			},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("nfs volume mount is still valid after driver restart [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pod := testsuites.PodDetails{
			Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' >> /mnt/test-1/data && while true; do sleep 3600; done"),
			Volumes: []testsuites.VolumeDetails{
				{
					FSType:    "ext3",
					ClaimSize: "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
			IsWindows:    isWindowsCluster,
			WinServerVer: winServerVer,
		}

		podCheckCmd := []string{"cat", "/mnt/test-1/data"}
		expectedString := "hello world\n"
		test := testsuites.DynamicallyProvisionedRestartDriverTest{
			CSIDriver: testDriver,
			Pod:       pod,
			PodCheck: &testsuites.PodExecCheck{
				Cmd:            podCheckCmd,
				ExpectedString: expectedString,
			},
			StorageClassParameters: map[string]string{"protocol": "nfs"},
			RestartDriverFunc: func() {
				restartDriver := testCmd{
					command:  "bash",
					args:     []string{"test/utils/restart_driver_daemonset.sh"},
					startLog: "Restart driver node daemonset ...",
					endLog:   "Restart driver node daemonset done successfully",
				}
				execTestCmd([]testCmd{restartDriver})
			},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should clone a volume from an existing volume [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfTestingInWindowsCluster()
		skipIfTestingInMigrationCluster()
		skipIfUsingInTreeVolumePlugin()

		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
			Volumes: []testsuites.VolumeDetails{
				{
					ClaimSize: "10Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
		}
		podWithClonedVolume := testsuites.PodDetails{
			Cmd: "grep 'hello world' /mnt/test-1/data",
		}
		test := testsuites.DynamicallyProvisionedVolumeCloningTest{
			CSIDriver:           testDriver,
			Pod:                 pod,
			PodWithClonedVolume: podWithClonedVolume,
			StorageClassParameters: map[string]string{
				"skuName": "Standard_LRS",
			},
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should clone a large size volume from an existing volume [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		skipIfTestingInWindowsCluster()
		skipIfTestingInMigrationCluster()
		skipIfUsingInTreeVolumePlugin()

		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data && dd if=/dev/zero of=/mnt/test-1/test bs=19G count=5",
			Volumes: []testsuites.VolumeDetails{
				{
					ClaimSize: "100Gi",
					VolumeMount: testsuites.VolumeMountDetails{
						NameGenerate:      "test-volume-",
						MountPathGenerate: "/mnt/test-",
					},
				},
			},
		}
		podWithClonedVolume := testsuites.PodDetails{
			Cmd: "grep 'hello world' /mnt/test-1/data",
		}
		test := testsuites.DynamicallyProvisionedVolumeCloningTest{
			CSIDriver:           testDriver,
			Pod:                 pod,
			PodWithClonedVolume: podWithClonedVolume,
			StorageClassParameters: map[string]string{
				"skuName": "Standard_LRS",
			},
		}
		test.Run(ctx, cs, ns)
	})

})

func restClient(group string, version string) (restclientset.Interface, error) {
	config, err := framework.LoadConfig()
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("could not load config: %v", err))
	}
	gv := schema.GroupVersion{Group: group, Version: version}
	config.GroupVersion = &gv
	config.APIPath = "/apis"
	config.NegotiatedSerializer = serializer.WithoutConversionCodecFactory{CodecFactory: serializer.NewCodecFactory(runtime.NewScheme())}
	return restclientset.RESTClientFor(config)
}
