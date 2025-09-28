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

package e2e

import (
	"fmt"

	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
)

const (
	defaultDiskSize      = 100
	defaultDiskSizeBytes = defaultDiskSize * 1024 * 1024 * 1024
)

var _ = ginkgo.Describe("Pre-Provisioned", func() {
	f := framework.NewDefaultFramework("azurefile")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var (
		cs         clientset.Interface
		ns         *v1.Namespace
		testDriver driver.PreProvisionedVolumeTestDriver
		volumeID   string
		// Set to true if the volume should be deleted automatically after test
		skipManuallyDeletingVolume bool
	)

	ginkgo.BeforeEach(func(_ ginkgo.SpecContext) {
		checkPodsRestart := testCmd{
			command:  "bash",
			args:     []string{"test/utils/check_driver_pods_restart.sh"},
			startLog: "Check driver pods if restarts ...",
			endLog:   "Check successfully",
		}
		execTestCmd([]testCmd{checkPodsRestart})

		cs = f.ClientSet
		ns = f.Namespace
		testDriver = driver.InitAzureFileDriver()
	})

	ginkgo.AfterEach(func(ctx ginkgo.SpecContext) {
		if !skipManuallyDeletingVolume {
			req := &csi.DeleteVolumeRequest{
				VolumeId: volumeID,
			}
			_, err := azurefileDriver.DeleteVolume(ctx, req)
			if err != nil {
				ginkgo.Fail(fmt.Sprintf("create volume %q error: %v", volumeID, err))
			}
		}
	})

	ginkgo.It("should use a pre-provisioned volume and mount it as readOnly in a pod [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		// Az tests are not yet working for in-tree
		skipIfUsingInTreeVolumePlugin()

		req := makeCreateVolumeReq("pre-provisioned-readonly", ns.Name)
		resp, err := azurefileDriver.CreateVolume(ctx, req)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("create volume error: %v", err))
		}
		volumeID = resp.Volume.VolumeId
		ginkgo.By(fmt.Sprintf("Successfully provisioned AzureFile volume: %q\n", volumeID))

		diskSize := fmt.Sprintf("%dGi", defaultDiskSize)
		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeID:  volumeID,
						ClaimSize: diskSize,
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
		test := testsuites.PreProvisionedReadOnlyVolumeTest{
			CSIDriver: testDriver,
			Pods:      pods,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should use a pre-provisioned volume and mount it by multiple pods [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		// Az tests are not yet working for in-tree
		skipIfUsingInTreeVolumePlugin()

		req := makeCreateVolumeReq("pre-provisioned-multiple-pods", ns.Name)
		resp, err := azurefileDriver.CreateVolume(ctx, req)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("create volume error: %v", err))
		}
		volumeID = resp.Volume.VolumeId
		ginkgo.By(fmt.Sprintf("Successfully provisioned AzureFile volume: %q\n", volumeID))

		diskSize := fmt.Sprintf("%dGi", defaultDiskSize)
		pods := []testsuites.PodDetails{}
		for i := 1; i <= 6; i++ {
			pod := testsuites.PodDetails{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						// make VolumeID unique in test
						VolumeID:  fmt.Sprintf("%s%d", volumeID, i),
						ClaimSize: diskSize,
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			}
			pods = append(pods, pod)
		}

		test := testsuites.PreProvisionedMultiplePods{
			CSIDriver: testDriver,
			Pods:      pods,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It(fmt.Sprintf("should use a pre-provisioned volume and retain PV with reclaimPolicy %q [file.csi.azure.com] [Windows]", v1.PersistentVolumeReclaimRetain), func(ctx ginkgo.SpecContext) {
		// Az tests are not yet working for in tree driver
		skipIfUsingInTreeVolumePlugin()

		req := makeCreateVolumeReq("pre-provisioned-retain-reclaimpolicy", ns.Name)
		resp, err := azurefileDriver.CreateVolume(ctx, req)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("create volume error: %v", err))
		}
		volumeID = resp.Volume.VolumeId
		ginkgo.By(fmt.Sprintf("Successfully provisioned AzureFile volume: %q\n", volumeID))

		diskSize := fmt.Sprintf("%dGi", defaultDiskSize)
		reclaimPolicy := v1.PersistentVolumeReclaimRetain
		volumes := []testsuites.VolumeDetails{
			{
				VolumeID:      volumeID,
				ClaimSize:     diskSize,
				ReclaimPolicy: &reclaimPolicy,
			},
		}
		test := testsuites.PreProvisionedReclaimPolicyTest{
			CSIDriver: testDriver,
			Volumes:   volumes,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should use existing credentials in k8s cluster [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		// Az tests are not yet working for in tree driver
		skipIfUsingInTreeVolumePlugin()

		req := makeCreateVolumeReq("pre-provisioned-existing-credentials", ns.Name)
		resp, err := azurefileDriver.CreateVolume(ctx, req)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("create volume error: %v", err))
		}
		volumeID = resp.Volume.VolumeId
		ginkgo.By(fmt.Sprintf("Successfully provisioned AzureFile volume: %q\n", volumeID))

		volumeSize := fmt.Sprintf("%dGi", defaultDiskSize)
		reclaimPolicy := v1.PersistentVolumeReclaimRetain
		volumeBindingMode := storagev1.VolumeBindingImmediate

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeID:          volumeID,
						FSType:            "ext4",
						ClaimSize:         volumeSize,
						ReclaimPolicy:     &reclaimPolicy,
						VolumeBindingMode: &volumeBindingMode,
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
		test := testsuites.PreProvisionedExistingCredentialsTest{
			CSIDriver: testDriver,
			Pods:      pods,
			Azurefile: azurefileDriver,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should use provided credentials [file.csi.azure.com] [Windows]", func(ctx ginkgo.SpecContext) {
		// Az tests are not yet working for in tree driver
		skipIfUsingInTreeVolumePlugin()

		req := makeCreateVolumeReq("pre-provisioned-provided-credentials", ns.Name)
		resp, err := azurefileDriver.CreateVolume(ctx, req)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("create volume error: %v", err))
		}
		volumeID = resp.Volume.VolumeId
		ginkgo.By(fmt.Sprintf("Successfully provisioned Azure File volume: %q\n", volumeID))

		volumeSize := fmt.Sprintf("%dGi", defaultDiskSize)
		reclaimPolicy := v1.PersistentVolumeReclaimRetain
		volumeBindingMode := storagev1.VolumeBindingImmediate

		pods := []testsuites.PodDetails{
			{
				Cmd: convertToPowershellCommandIfNecessary("echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data"),
				Volumes: []testsuites.VolumeDetails{
					{
						VolumeID:          volumeID,
						FSType:            "ext4",
						ClaimSize:         volumeSize,
						ReclaimPolicy:     &reclaimPolicy,
						VolumeBindingMode: &volumeBindingMode,
						VolumeMount: testsuites.VolumeMountDetails{
							NameGenerate:      "test-volume-",
							MountPathGenerate: "/mnt/test-",
						},
						NodeStageSecretRef: "azure-secret",
					},
				},
				IsWindows:    isWindowsCluster,
				WinServerVer: winServerVer,
			},
		}
		test := testsuites.PreProvisionedProvidedCredentiasTest{
			CSIDriver: testDriver,
			Pods:      pods,
			Azurefile: azurefileDriver,
		}
		test.Run(ctx, cs, ns)
	})
})

func makeCreateVolumeReq(volumeName, secretNamespace string) *csi.CreateVolumeRequest {
	req := &csi.CreateVolumeRequest{
		Name: volumeName,
		VolumeCapabilities: []*csi.VolumeCapability{
			{
				AccessType: &csi.VolumeCapability_Mount{
					Mount: &csi.VolumeCapability_MountVolume{},
				},
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
				},
			},
		},
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: defaultDiskSizeBytes,
			LimitBytes:    defaultDiskSizeBytes,
		},
		Parameters: map[string]string{
			"skuname":                  "Standard_LRS",
			"shareName":                volumeName,
			"secretNamespace":          secretNamespace,
			"mountWithManagedIdentity": "true",
		},
	}

	return req
}
