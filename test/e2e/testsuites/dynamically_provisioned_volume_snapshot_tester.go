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

package testsuites

import (
	"context"

	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"strconv"
	"time"

	"github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	restclientset "k8s.io/client-go/rest"
)

// DynamicallyProvisionedVolumeSnapshotTest will provision required StorageClass(es),VolumeSnapshotClass(es), PVC(s) and Pod(s)
// Waiting for the PV provisioner to create a new PV
// Testing if the Pod(s) can write and read to mounted volumes
// Create a snapshot, validate whether it is ready to use.
// This test only supports a single volume
type DynamicallyProvisionedVolumeSnapshotTest struct {
	CSIDriver              driver.PVTestDriver
	Pod                    PodDetails
	PodWithSnapshot        PodDetails
	StorageClassParameters map[string]string
}

func (t *DynamicallyProvisionedVolumeSnapshotTest) Run(ctx context.Context, client clientset.Interface, restclient restclientset.Interface, namespace *v1.Namespace) {
	tpod := NewTestPod(client, namespace, t.Pod.Cmd, t.Pod.IsWindows, t.Pod.WinServerVer)
	volume := t.Pod.Volumes[0]
	tpvc, pvcCleanup := volume.SetupDynamicPersistentVolumeClaim(ctx, client, namespace, t.CSIDriver, t.StorageClassParameters)
	for i := range pvcCleanup {
		defer pvcCleanup[i](ctx)
	}
	tpod.SetupVolume(tpvc.persistentVolumeClaim, volume.VolumeMount.NameGenerate+"1", volume.VolumeMount.MountPathGenerate+"1", volume.VolumeMount.ReadOnly)

	ginkgo.By("deploying the pod")
	tpod.Create(ctx)
	defer tpod.Cleanup(ctx)
	ginkgo.By("checking that the pod's command exits with no error")
	tpod.WaitForSuccess(ctx)

	ginkgo.By("creating volume snapshot class")
	tvsc, cleanup := CreateVolumeSnapshotClass(ctx, restclient, namespace, t.CSIDriver)
	defer cleanup()

	for i := 0; i < 5; i++ {
		ginkgo.By("taking snapshot# " + strconv.Itoa(i+1))
		snapshot := tvsc.CreateSnapshot(ctx, tpvc.persistentVolumeClaim)
		defer tvsc.DeleteSnapshot(ctx, snapshot)

		ginkgo.By("sleeping for 3 seconds to wait for snapshot to be ready")
		time.Sleep(3 * time.Second)

		// If the field ReadyToUse is still false, there will be a timeout error.
		tvsc.ReadyToUse(ctx, snapshot)
	}

}
