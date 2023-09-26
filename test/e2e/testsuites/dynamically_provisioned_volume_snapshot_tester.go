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
	"strconv"
	"time"

	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"

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
	CSIDriver                      driver.PVTestDriver
	Pod                            PodDetails
	ShouldOverwrite                bool
	ShouldRestore                  bool
	PodOverwrite                   PodDetails
	PodWithSnapshot                PodDetails
	StorageClassParameters         map[string]string
	SnapshotStorageClassParameters map[string]string
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
	ginkgo.By("sleep 10s to make sure the data is written to the disk")
	time.Sleep(time.Millisecond * 10000)

	ginkgo.By("creating volume snapshot class")
	tvsc, cleanup := CreateVolumeSnapshotClass(ctx, restclient, namespace, t.CSIDriver)
	if tvsc.volumeSnapshotClass.Parameters == nil {
		tvsc.volumeSnapshotClass.Parameters = map[string]string{}
	}
	defer cleanup()

	ginkgo.By("sleeping for 5 seconds to wait for data to be written to the volume")
	time.Sleep(5 * time.Second)

	for i := 0; i < 2; i++ {
		ginkgo.By("taking snapshot# " + strconv.Itoa(i+1))
		snapshot := tvsc.CreateSnapshot(ctx, tpvc.persistentVolumeClaim)

		ginkgo.By("sleeping for 3 seconds to wait for snapshot to be ready")
		time.Sleep(3 * time.Second)

		// If the field ReadyToUse is still false, there will be a timeout error.
		tvsc.ReadyToUse(ctx, snapshot)

		if t.ShouldOverwrite && i == 1 { // just overwrite in the last snapshot test case
			tpod = NewTestPod(client, namespace, t.PodOverwrite.Cmd, t.PodOverwrite.IsWindows, t.Pod.WinServerVer)

			tpod.SetupVolume(tpvc.persistentVolumeClaim, volume.VolumeMount.NameGenerate+"1", volume.VolumeMount.MountPathGenerate+"1", volume.VolumeMount.ReadOnly)
			tpod.SetLabel(TestLabel)
			ginkgo.By("deploying a new pod to overwrite pv data")
			tpod.Create(ctx)
			defer tpod.Cleanup(ctx)
			ginkgo.By("checking that the pod is running")
			tpod.WaitForRunning(ctx)
		}

		defer tvsc.DeleteSnapshot(ctx, snapshot)

		if t.ShouldRestore {
			snapshotVolume := volume
			snapshotVolume.DataSource = &DataSource{
				Kind: VolumeSnapshotKind,
				Name: snapshot.Name,
			}
			snapshotVolume.ClaimSize = volume.ClaimSize
			snapshotStorageClassParameters := t.StorageClassParameters
			if t.SnapshotStorageClassParameters != nil {
				snapshotStorageClassParameters = t.SnapshotStorageClassParameters
			}
			t.PodWithSnapshot.Volumes = []VolumeDetails{snapshotVolume}
			tPodWithSnapshot, tPodWithSnapshotCleanup := t.PodWithSnapshot.SetupWithDynamicVolumes(ctx, client, namespace, t.CSIDriver, snapshotStorageClassParameters)
			for idx := range tPodWithSnapshotCleanup {
				defer tPodWithSnapshotCleanup[idx](ctx)
			}

			ginkgo.By("deploying a pod with a volume restored from the snapshot")
			tPodWithSnapshot.Create(ctx)
			defer tPodWithSnapshot.Cleanup(ctx)
			ginkgo.By("checking that the pod's command exits with no error")
			tPodWithSnapshot.WaitForSuccess(ctx)
		}
	}
}
