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
	clientset "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
)

// PreProvisionedMountFailureTest will provision required PV(s), PVC(s) or CSI inline volume(s) and Pod(s)
// Testing if the Pod(s) fail to mount the volume
type PreProvisionedMountFailureTest struct {
	CSIDriver       driver.PreProvisionedVolumeTestDriver
	Pods            []PodDetails
	CSIInlineVolume bool
}

func (t *PreProvisionedMountFailureTest) Run(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) {
	for _, pod := range t.Pods {
		var tpod *TestPod
		if t.CSIInlineVolume {
			tpod, _ = pod.SetupWithCSIInlineVolumes(client, namespace, "", "", "", false)
		} else {
			var cleanup []func(ctx context.Context)
			tpod, cleanup = pod.SetupWithPreProvisionedVolumes(ctx, client, namespace, t.CSIDriver)
			// defer must be called here for resources not get removed before using them
			for i := range cleanup {
				defer cleanup[i](ctx)
			}
		}

		ginkgo.By("deploying the pod")
		tpod.Create(ctx)
		defer tpod.Cleanup(ctx)
		ginkgo.By("checking that the pod has 'FailedMount' event")
		tpod.WaitForFailedMountError(ctx)
	}
}
