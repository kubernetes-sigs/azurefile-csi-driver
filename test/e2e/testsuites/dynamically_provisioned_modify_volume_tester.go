/*
Copyright 2026 The Kubernetes Authors.

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
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"

	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
)

// DynamicallyProvisionedModifyVolumeTest provisions a PremiumV2 volume, then
// creates a VolumeAttributesClass, patches the PVC to use it, and verifies
// that the provisioned IOPS and bandwidth are modified successfully.
type DynamicallyProvisionedModifyVolumeTest struct {
	CSIDriver              driver.DynamicPVTestDriver
	Pods                   []PodDetails
	StorageClassParameters map[string]string
	VolumeAttributesClass  *storagev1.VolumeAttributesClass
}

func (t *DynamicallyProvisionedModifyVolumeTest) Run(ctx context.Context, cs clientset.Interface, ns *v1.Namespace) {
	// Check if VolumeAttributesClass API is available
	_, err := cs.StorageV1().VolumeAttributesClasses().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		ginkgo.Skip(fmt.Sprintf("VolumeAttributesClass API not available: %v", err))
	}

	for _, pod := range t.Pods {
		tpod, cleanup := pod.SetupWithDynamicVolumes(ctx, cs, ns, t.CSIDriver, t.StorageClassParameters)
		for i := range cleanup {
			defer cleanup[i](ctx)
		}

		ginkgo.By("deploying the pod")
		tpod.Create(ctx)
		defer tpod.Cleanup(ctx)

		ginkgo.By("checking that the pod is running")
		tpod.WaitForRunning(ctx)

		ginkgo.By("creating VolumeAttributesClass")
		createdVAC, err := cs.StorageV1().VolumeAttributesClasses().Create(ctx, t.VolumeAttributesClass, metav1.CreateOptions{})
		framework.ExpectNoError(err, "while creating VolumeAttributesClass")
		defer func() {
			_ = cs.StorageV1().VolumeAttributesClasses().Delete(ctx, createdVAC.Name, metav1.DeleteOptions{})
		}()

		// Find PVC name from the pod's volumes
		pvcName := tpod.pod.Spec.Volumes[0].VolumeSource.PersistentVolumeClaim.ClaimName

		ginkgo.By(fmt.Sprintf("patching PVC %s to use VolumeAttributesClass %s", pvcName, createdVAC.Name))
		pvc, err := cs.CoreV1().PersistentVolumeClaims(ns.Name).Get(ctx, pvcName, metav1.GetOptions{})
		framework.ExpectNoError(err, "while getting PVC")

		pvc.Spec.VolumeAttributesClassName = &createdVAC.Name
		_, err = cs.CoreV1().PersistentVolumeClaims(ns.Name).Update(ctx, pvc, metav1.UpdateOptions{})
		framework.ExpectNoError(err, "while patching PVC with VolumeAttributesClass")

		ginkgo.By("waiting for volume modification to complete")
		gomega.Eventually(func() bool {
			pvc, err := cs.CoreV1().PersistentVolumeClaims(ns.Name).Get(ctx, pvcName, metav1.GetOptions{})
			if err != nil {
				return false
			}
			currentVAC := ""
			if pvc.Status.CurrentVolumeAttributesClassName != nil {
				currentVAC = *pvc.Status.CurrentVolumeAttributesClassName
			}
			return currentVAC == createdVAC.Name && pvc.Status.ModifyVolumeStatus == nil
		}, 5*time.Minute, 15*time.Second).Should(gomega.BeTrue(),
			"volume modification did not complete")
	}
}
