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

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
)

// RunConcurrentAuthentication keeps both principals mounted during each peer's
// remount. Kerberos callers must grant each identity access ONLY to its own share.
func RunConcurrentAuthentication(ctx context.Context, f *framework.Framework, namespace *v1.Namespace, tests [2]*AuthenticationMountTest) {
	gomega.Expect(tests[0].AccountName).To(gomega.Equal(tests[1].AccountName))
	gomega.Expect(tests[0].NodeName).To(gomega.Equal(tests[1].NodeName))
	gomega.Expect(tests[0].ShareName).NotTo(gomega.Equal(tests[1].ShareName))
	var claims [2]*v1.PersistentVolumeClaim
	var pods [2]*TestPod
	var markers [2]string
	for i, test := range tests {
		claims[i] = test.prepare(ctx, f.ClientSet, namespace)
		markers[i] = uuid.NewString()
	}
	for i, test := range tests {
		pods[i] = test.newPod(f.ClientSet, namespace, claims[i],
			fmt.Sprintf("printf '%%s' '%s' > /mnt/auth/data && sync && sleep 86400", markers[i]))
		pods[i].Create(ctx)
		index := i
		ginkgo.DeferCleanup(func(ctx context.Context) {
			if pods[index] != nil {
				pods[index].CleanupAndWait(ctx)
			}
		})
	}
	check := func(i int) {
		pods[i].WaitForRunning(ctx)
		authenticationExpectIO(ctx, f, pods[i].pod,
			fmt.Sprintf("%s && test \"$(cat /mnt/auth/data)\" = '%s' && printf '%%s' '%s' > /mnt/auth/check && sync && test \"$(cat /mnt/auth/check)\" = '%s'",
				tests[i].mountCheck(), markers[i], markers[i], markers[i]))
		if tests[i].AfterMount != nil {
			tests[i].AfterMount(ctx, tests[i].volumeHandle(ctx, f.ClientSet, claims[i]))
		}
	}
	check(0)
	check(1)
	for i, test := range tests {
		ginkgo.By(fmt.Sprintf("remounting principal %d while its peer remains mounted", i+1))
		current, err := f.ClientSet.CoreV1().Pods(namespace.Name).Get(ctx, pods[i].pod.Name, metav1.GetOptions{})
		framework.ExpectNoError(err)
		pods[i].CleanupAndWait(ctx)
		pods[i] = nil
		authenticationWaitForShareUnmount(ctx, f, current.Spec.NodeName,
			fmt.Sprintf("//%s.file.%s/%s", test.AccountName, test.StorageEndpointSuffix, test.ShareName))
		if test.BetweenMounts != nil {
			test.BetweenMounts(ctx, test.volumeHandle(ctx, f.ClientSet, claims[i]))
		}
		pods[i] = test.newPod(f.ClientSet, namespace, claims[i], "sleep 86400")
		pods[i].Create(ctx)
		check(i)
		check(1 - i)
	}
}
