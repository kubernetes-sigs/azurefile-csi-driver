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

	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"

	"github.com/onsi/ginkgo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	clientset "k8s.io/client-go/kubernetes"
	restclientset "k8s.io/client-go/rest"
	"k8s.io/kubernetes/test/e2e/framework"
)

var _ = ginkgo.Describe("Dynamic Provisioning", func() {
	f := framework.NewDefaultFramework("azurefile")

	var (
		cs          clientset.Interface
		ns          *v1.Namespace
		snapshotrcs restclientset.Interface
		testDriver  driver.PVTestDriver
	)

	ginkgo.BeforeEach(func() {
		checkPodsRestart := testCmd{
			command:  "sh",
			args:     []string{"test/utils/check_driver_pods_restart.sh"},
			startLog: "Check driver pods if restarts ...",
			endLog:   "Check successfully",
		}
		execTestCmd([]testCmd{checkPodsRestart})

		cs = f.ClientSet
		ns = f.Namespace

		var err error
		snapshotrcs, err = restClient(testsuites.SnapshotAPIGroup, testsuites.APIVersionv1beta1)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("could not get rest clientset: %v", err))
		}
	})

	testDriver = driver.InitAzureFileDriver()

	ginkgo.It("should create a storage account with tags [file.csi.azure.com] [Windows]", func() {
		// Because the pv object created by kubernetes.io/azure-file does not contain storage account name, skip the test with in-tree volume plugin.
		skipIfUsingInTreeVolumePlugin()

		volumes := []testsuites.VolumeDetails{
			{
				FSType:    "ext4",
				ClaimSize: "100Gi",
				VolumeMount: testsuites.VolumeMountDetails{
					NameGenerate:      "test-volume-",
					MountPathGenerate: "/mnt/test-",
				},
			},
		}
		tags := "account=azurefile-test"
		test := testsuites.DynamicallyProvisionedAccountWithTags{
			CSIDriver:              testDriver,
			Volumes:                volumes,
			StorageClassParameters: map[string]string{"skuName": "Premium_LRS", "tags": tags},
			Tags:                   tags,
		}

		test.Run(cs, ns)
	})

	ginkgo.It("should create a volume on demand with mount options [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func() {
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
				IsWindows: isWindowsCluster,
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: map[string]string{"skuName": "Standard_LRS"},
		}
		test.Run(cs, ns)
	})

	ginkgo.It("should create multiple PV objects, bind to PVCs and attach all to different pods on the same node [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func() {
		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("while true; do echo $(date -u) >> /mnt/test-1/data; sleep 100; done"),
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
				IsWindows: isWindowsCluster,
			},
			{
				Cmd: convertToPowershellCommandIfNecessary("while true; do echo $(date -u) >> /mnt/test-1/data; sleep 100; done"),
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
				IsWindows: isWindowsCluster,
			},
		}
		test := testsuites.DynamicallyProvisionedCollocatedPodTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			ColocatePods:           true,
			StorageClassParameters: map[string]string{"skuName": "Standard_LRS"},
		}
		test.Run(cs, ns)
	})

	// Track issue https://github.com/kubernetes/kubernetes/issues/70505
	ginkgo.It("should create a volume on demand and mount it as readOnly in a pod [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func() {
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
				IsWindows: isWindowsCluster,
			},
		}
		test := testsuites.DynamicallyProvisionedReadOnlyVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: map[string]string{"skuName": "Standard_GRS"},
		}
		test.Run(cs, ns)
	})

	ginkgo.It("should create a deployment object, write and read to it, delete the pod and write and read to it again [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func() {
		pod := testsuites.PodDetails{
			Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' >> /mnt/test-1/data && while true; do sleep 100; done"),
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
			IsWindows: isWindowsCluster,
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
		test.Run(cs, ns)
	})

	ginkgo.It(fmt.Sprintf("should delete PV with reclaimPolicy %q [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", v1.PersistentVolumeReclaimDelete), func() {
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
			StorageClassParameters: map[string]string{"skuName": "Standard_RAGRS"},
		}
		test.Run(cs, ns)
	})

	ginkgo.It(fmt.Sprintf("should retain PV with reclaimPolicy %q [file.csi.azure.com] [Windows]", v1.PersistentVolumeReclaimRetain), func() {
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
			CSIDriver:              testDriver,
			Volumes:                volumes,
			Azurefile:              azurefileDriver,
			StorageClassParameters: map[string]string{"skuName": "Premium_LRS"},
		}
		test.Run(cs, ns)
	})

	ginkgo.It("should create a volume on demand and resize it [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func() {
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
				IsWindows: isWindowsCluster,
			},
		}
		test := testsuites.DynamicallyProvisionedResizeVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: map[string]string{"skuName": "Standard_LRS"},
		}
		test.Run(cs, ns)
	})

	ginkgo.It("should create a vhd disk volume on demand [kubernetes.io/azure-file] [file.csi.azure.com][disk]", func() {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pods := []testsuites.PodDetails{
			{
				Cmd: "echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "1024Gi", // test with big size
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			StorageClassParameters: map[string]string{"skuName": "Standard_LRS", "fsType": "ext4"},
		}
		test.Run(cs, ns)
	})

	ginkgo.It("should receive FailedMount event with invalid mount options [file.csi.azure.com] [disk]", func() {
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
		test.Run(cs, ns)
	})

	ginkgo.It("should receive FailedMount event with invalid mount options [file.csi.azure.com] [disk]", func() {
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
		test.Run(cs, ns)
	})

	ginkgo.It("should create multiple PV objects, bind to PVCs and attach all to different pods on the same node [file.csi.azure.com][disk]", func() {
		skipIfUsingInTreeVolumePlugin()
		skipIfTestingInWindowsCluster()

		pods := []testsuites.PodDetails{
			{
				Cmd: "while true; do echo $(date -u) >> /mnt/test-1/data; sleep 100; done",
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
			},
			{
				Cmd: convertToPowershellCommandIfNecessary("while true; do echo $(date -u) >> /mnt/test-1/data; sleep 100; done"),
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
				IsWindows: isWindowsCluster,
			},
		}
		test := testsuites.DynamicallyProvisionedCollocatedPodTest{
			CSIDriver:              testDriver,
			Pods:                   pods,
			ColocatePods:           true,
			StorageClassParameters: map[string]string{"skuName": "Premium_LRS", "fsType": "xfs"},
		}
		test.Run(cs, ns)
	})

	// Track issue https://github.com/kubernetes/kubernetes/issues/70505
	ginkgo.It("should create a vhd disk volume on demand and mount it as readOnly in a pod [file.csi.azure.com][disk]", func() {
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
		test.Run(cs, ns)
	})

	ginkgo.It("should create a deployment object, write and read to it, delete the pod and write and read to it again [file.csi.azure.com] [disk]", func() {
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
		test.Run(cs, ns)
	})

	ginkgo.It(fmt.Sprintf("should delete PV with reclaimPolicy %q [file.csi.azure.com] [disk]", v1.PersistentVolumeReclaimDelete), func() {
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
		test.Run(cs, ns)
	})

	ginkgo.It(fmt.Sprintf("[env] should retain PV with reclaimPolicy %q [file.csi.azure.com] [disk]", v1.PersistentVolumeReclaimRetain), func() {
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
		test.Run(cs, ns)
	})

	ginkgo.It("should create a pod with multiple volumes [kubernetes.io/azure-file] [file.csi.azure.com] [Windows]", func() {
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
				Cmd:       convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes:   volumes,
				IsWindows: isWindowsCluster,
			},
		}
		test := testsuites.DynamicallyProvisionedPodWithMultiplePVsTest{
			CSIDriver: testDriver,
			Pods:      pods,
		}
		test.Run(cs, ns)
	})

	ginkgo.It("should create a pod, write and read to it, take a volume snapshot, and validate whether it is ready to use [file.csi.azure.com]", func() {
		skipIfTestingInWindowsCluster()
		skipIfUsingInTreeVolumePlugin()

		pod := testsuites.PodDetails{
			Cmd: "echo 'hello world' > /mnt/test-1/data",
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
		}
		podWithSnapshot := testsuites.PodDetails{
			Cmd: "grep 'hello world' /mnt/test-1/data",
		}
		test := testsuites.DynamicallyProvisionedVolumeSnapshotTest{
			CSIDriver:              testDriver,
			Pod:                    pod,
			PodWithSnapshot:        podWithSnapshot,
			StorageClassParameters: map[string]string{"skuName": "Standard_LRS"},
		}
		test.Run(cs, snapshotrcs, ns)
	})

	ginkgo.It("should create a NFS volume on demand with mount options [kubernetes.io/azure-file] [file.csi.azure.com] [nfs]", func() {
		skipIfTestingInWindowsCluster()
		skipIfUsingInTreeVolumePlugin()

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						ClaimSize: "100Gi",
						MountOptions: []string{
							"remount",
						},
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows: isWindowsCluster,
			},
		}
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver: testDriver,
			Pods:      pods,
			StorageClassParameters: map[string]string{
				"skuName":  "Premium_LRS",
				"protocol": "nfs",
			},
		}
		test.Run(cs, ns)
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
