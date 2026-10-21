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
	"strings"

	"github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
)

// PreProvisionedConcurrentMountsTest will provision required PV(s), PVC(s) and long-running Pod(s)
// Testing if every Pod's Cmd keeps passing while each Pod in turn is replaced and freshly mounted,
// and if every DeniedPods entry fails to mount while the Pods stay mounted
type PreProvisionedConcurrentMountsTest struct {
	CSIDriver  driver.PreProvisionedVolumeTestDriver
	Pods       []PodDetails
	DeniedPods []PodDetails
}

func (t *PreProvisionedConcurrentMountsTest) Run(ctx context.Context, f *framework.Framework, namespace *v1.Namespace) {
	tpods := make([]*TestPod, len(t.Pods))
	for i, pod := range t.Pods {
		longRunning := pod
		longRunning.Cmd = pod.Cmd + " && sleep 86400"
		tpod, cleanup := longRunning.SetupWithPreProvisionedVolumes(ctx, f.ClientSet, namespace, t.CSIDriver)
		// defer must be called here for resources not get removed before using them
		for j := range cleanup {
			defer cleanup[j](ctx)
		}
		ginkgo.By("deploying the pod")
		tpod.Create(ctx)
		tpods[i] = tpod
		defer func() { tpods[i].Cleanup(ctx) }()
	}
	checkAll := func() {
		for i, pod := range t.Pods {
			tpods[i].WaitForRunning(ctx)
			ginkgo.By(fmt.Sprintf("checking that pod %d's command passes again", i+1))
			authenticationExpectIO(ctx, f, tpods[i].pod, pod.Cmd)
		}
	}
	checkAll()

	for i, pod := range t.Pods {
		ginkgo.By(fmt.Sprintf("replacing pod %d while the other pods stay mounted", i+1))
		mountPath := pod.Volumes[0].VolumeMount.MountPathGenerate + "1"
		source, err := authenticationExec(ctx, f, tpods[i].pod, "volume-tester", fmt.Sprintf("awk '$2 == \"%s\" {print $1}' /proc/mounts", mountPath))
		framework.ExpectNoError(err)
		current, err := f.ClientSet.CoreV1().Pods(namespace.Name).Get(ctx, tpods[i].pod.Name, metav1.GetOptions{})
		framework.ExpectNoError(err)
		tpods[i].CleanupAndWait(ctx)
		// A fully unstaged share makes the replacement authenticate again.
		authenticationWaitForShareUnmount(ctx, f, current.Spec.NodeName, strings.TrimSpace(source))
		tpods[i] = tpods[i].recreate()
		tpods[i].Create(ctx)
		checkAll()
	}

	for _, pod := range t.DeniedPods {
		tpod, cleanup := pod.SetupWithPreProvisionedVolumes(ctx, f.ClientSet, namespace, t.CSIDriver)
		for j := range cleanup {
			defer cleanup[j](ctx)
		}
		ginkgo.By("deploying the pod that must not mount")
		tpod.Create(ctx)
		defer tpod.Cleanup(ctx)
		ginkgo.By("checking that the pod has 'FailedMount' event")
		tpod.WaitForFailedMountError(ctx)
	}
}

// recreate returns an uncreated copy of the pod with the same spec and volumes.
func (t *TestPod) recreate() *TestPod {
	return &TestPod{
		client:    t.client,
		namespace: t.namespace,
		pod: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{GenerateName: t.pod.GenerateName},
			Spec:       *t.pod.Spec.DeepCopy(),
		},
	}
}
