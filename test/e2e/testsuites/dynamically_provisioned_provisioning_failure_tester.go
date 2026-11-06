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

	"github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2eevents "k8s.io/kubernetes/test/e2e/framework/events"
	e2epv "k8s.io/kubernetes/test/e2e/framework/pv"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
)

// DynamicallyProvisionedProvisioningFailureTest will provision required StorageClass(es) and PVC(s)
// Testing if the PV provisioner fails to create the volume(s)
type DynamicallyProvisionedProvisioningFailureTest struct {
	CSIDriver              driver.DynamicPVTestDriver
	Volumes                []VolumeDetails
	StorageClassParameters map[string]string
}

func (t *DynamicallyProvisionedProvisioningFailureTest) Run(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) {
	for _, volume := range t.Volumes {
		tsc, cleanup := volume.CreateStorageClass(ctx, client, namespace, t.CSIDriver, t.StorageClassParameters)
		defer cleanup(ctx)

		ginkgo.By("setting up the PVC")
		tpvc := NewTestPersistentVolumeClaim(client, namespace, volume.ClaimSize, volume.VolumeMode, tsc.storageClass)
		tpvc.Create(ctx)
		defer func() {
			framework.ExpectNoError(e2epv.DeletePersistentVolumeClaim(ctx, client, tpvc.persistentVolumeClaim.Name, namespace.Name))
		}()

		ginkgo.By("checking that the PVC has 'ProvisioningFailed' event")
		framework.ExpectNoError(e2eevents.WaitTimeoutForEvent(ctx, client, namespace.Name,
			fields.Set{"reason": "ProvisioningFailed", "involvedObject.name": tpvc.persistentVolumeClaim.Name}.AsSelector().String(),
			"", pollLongTimeout))
	}
}
