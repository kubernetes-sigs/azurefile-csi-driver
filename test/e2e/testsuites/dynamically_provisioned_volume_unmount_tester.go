/*
Copyright 2022 The Kubernetes Authors.

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

package testsuites

import (
	"context"
	"time"

	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
)

// DynamicallyProvisionedVolumeUnmountTest will provision required StorageClass and Deployment
// Testing if the Pod can write and read to mounted volumes
// Delete the volume and check whether pod could be terminated successfully
type DynamicallyProvisionedVolumeUnmountTest struct {
	CSIDriver              driver.DynamicPVTestDriver
	Azurefile              *azurefile.Driver
	Pod                    PodDetails
	PodCheck               *PodExecCheck
	StorageClassParameters map[string]string
}

func (t *DynamicallyProvisionedVolumeUnmountTest) Run(client clientset.Interface, namespace *v1.Namespace) {
	tDeployment, cleanup, volumeID := t.Pod.SetupDeployment(client, namespace, t.CSIDriver, t.StorageClassParameters)
	// defer must be called here for resources not get removed before using them
	for i := range cleanup {
		defer cleanup[i]()
	}

	ginkgo.By("deploying the deployment")
	tDeployment.Create()

	ginkgo.By("checking that the pod is running")
	tDeployment.WaitForPodReady()

	if t.PodCheck != nil {
		time.Sleep(time.Second)
		ginkgo.By("check pod exec")
		tDeployment.PollForStringInPodsExec(t.PodCheck.Cmd, t.PodCheck.ExpectedString)
	}

	ginkgo.By("delete volume " + volumeID + " first, make sure pod could still be terminated")
	_, err := t.Azurefile.DeleteVolume(context.TODO(), &csi.DeleteVolumeRequest{VolumeId: volumeID})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("check whether " + volumeID + " exists")
	multiNodeVolCap := []*csi.VolumeCapability{
		{
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
			},
		},
	}
	req := &csi.ValidateVolumeCapabilitiesRequest{
		VolumeId:           volumeID,
		VolumeCapabilities: multiNodeVolCap,
	}

	if _, err = t.Azurefile.ValidateVolumeCapabilities(context.TODO(), req); err != nil {
		ginkgo.By("ValidateVolumeCapabilities " + volumeID + " returned with error: " + err.Error())
	}
	gomega.Expect(err).To(gomega.HaveOccurred())

	ginkgo.By("deleting the pod for deployment")
	tDeployment.DeletePodAndWait()
}
