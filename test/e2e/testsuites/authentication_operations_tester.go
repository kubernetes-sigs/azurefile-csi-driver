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

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/kubernetes/test/e2e/framework"
)

func (t *AuthenticationMountTest) RunExpansion(ctx context.Context, f *framework.Framework, namespace *v1.Namespace, verifyShareQuota func(context.Context, int32) error) {
	pvc := t.prepare(ctx, f.ClientSet, namespace)
	gomega.Expect(pvc).NotTo(gomega.BeNil())
	gomega.Expect(t.RequestSecrets.ControllerExpand).NotTo(gomega.BeEmpty())
	marker := uuid.NewString()
	pod := t.newPod(f.ClientSet, namespace, pvc, fmt.Sprintf("printf '%%s' '%s' > /mnt/auth/data && sync && sleep 86400", marker))
	pod.Create(ctx)
	ginkgo.DeferCleanup(pod.CleanupAndWait)
	pod.WaitForRunning(ctx)
	authenticationExpectIO(ctx, f, pod.pod, t.mountCheck()+" && test -s /mnt/auth/data")
	handle := t.volumeHandle(ctx, f.ClientSet, pvc)
	if t.AfterMount != nil {
		t.AfterMount(ctx, handle)
	}
	since := time.Now()
	framework.ExpectNoError(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := f.ClientSet.CoreV1().PersistentVolumeClaims(namespace.Name).Get(ctx, pvc.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		current.Spec.Resources.Requests[v1.ResourceStorage] = resource.MustParse("101Gi")
		_, err = f.ClientSet.CoreV1().PersistentVolumeClaims(namespace.Name).Update(ctx, current, metav1.UpdateOptions{})
		return err
	}))
	AuthenticationRPCObserved(ctx, f.ClientSet, "controller", "", since, map[string]string{"volume_id": handle}, true, "capacity_range")
	gomega.Eventually(ctx, func(ctx context.Context) bool {
		claim, err := f.ClientSet.CoreV1().PersistentVolumeClaims(namespace.Name).Get(ctx, pvc.Name, metav1.GetOptions{})
		if err != nil || claim.Status.Capacity.Storage().Cmp(resource.MustParse("101Gi")) != 0 {
			return false
		}
		pv, err := f.ClientSet.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
		return err == nil && pv.Spec.Capacity.Storage().Cmp(resource.MustParse("101Gi")) == 0
	}, 10*time.Minute, 5*time.Second).Should(gomega.BeTrue(), "PVC status and PV capacity must both reflect expansion")
	gomega.Eventually(ctx, func(ctx context.Context) error {
		return verifyShareQuota(ctx, 101)
	}, 5*time.Minute, 5*time.Second).Should(gomega.Succeed(), "Azure must report the actual expanded share quota")
	authenticationExpectIO(ctx, f, pod.pod,
		fmt.Sprintf("%s && test \"$(cat /mnt/auth/data)\" = '%s' && echo expanded > /mnt/auth/after-expand && sync", t.mountCheck(), marker))
	t.expectAccountKeySecrets(ctx, f.ClientSet, namespace)
}

func (t *AuthenticationMountTest) RunSASClone(ctx context.Context, f *framework.Framework, namespace *v1.Namespace) {
	gomega.Expect(t.RequestSecrets.Provisioner).NotTo(gomega.BeEmpty())
	source := t.prepare(ctx, f.ClientSet, namespace)
	marker := uuid.NewString()
	writer := t.newPod(f.ClientSet, namespace, source,
		fmt.Sprintf("%s && printf '%%s' '%s' > /mnt/auth/data && sync", t.mountCheck(), marker))
	writer.Create(ctx)
	cleaned := false
	ginkgo.DeferCleanup(func(ctx context.Context) {
		if !cleaned {
			writer.CleanupAndWait(ctx)
		}
	})
	writer.WaitForSuccess(ctx)
	writer.CleanupAndWait(ctx)
	cleaned = true
	clone := *t
	clone.Name = "auth-clone"
	clone.ShareName = "clone-" + uuid.NewString()
	clone.VolumeAttributes = make(map[string]string, len(t.VolumeAttributes))
	for key, value := range t.VolumeAttributes {
		clone.VolumeAttributes[key] = value
	}
	clone.VolumeAttributes["shareName"] = clone.ShareName
	clone.CloneSource = source.Name
	since := time.Now()
	destination := clone.prepare(ctx, f.ClientSet, namespace)
	gomega.Expect(destination.Spec.VolumeName).NotTo(gomega.Equal(source.Spec.VolumeName))
	AuthenticationRPCObserved(ctx, f.ClientSet, "controller", "", since,
		map[string]string{"name": "pvc-" + string(destination.UID)}, true, "volume_content_source")
	AuthenticationLogObserved(ctx, f.ClientSet, "controller", "", since, "generate sas token for account("+t.AccountName+")")
	AuthenticationLogObserved(ctx, f.ClientSet, "controller", "", since,
		fmt.Sprintf("copy fileshare %s:%s to %s:%s", t.AccountName, t.ShareName, clone.AccountName, clone.ShareName))
	for _, volume := range []struct {
		test  *AuthenticationMountTest
		claim *v1.PersistentVolumeClaim
	}{{t, source}, {&clone, destination}} {
		reader := volume.test.newPod(f.ClientSet, namespace, volume.claim,
			fmt.Sprintf("%s && test \"$(cat /mnt/auth/data)\" = '%s'", volume.test.mountCheck(), marker))
		reader.Create(ctx)
		deleted := false
		ginkgo.DeferCleanup(func(ctx context.Context) {
			if !deleted {
				reader.CleanupAndWait(ctx)
			}
		})
		reader.WaitForSuccess(ctx)
		reader.CleanupAndWait(ctx)
		deleted = true
	}
	t.expectAccountKeySecrets(ctx, f.ClientSet, namespace)
}
