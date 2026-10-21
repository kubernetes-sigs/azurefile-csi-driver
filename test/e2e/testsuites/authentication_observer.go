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
	"time"

	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
)

// AuthenticationLogObserved checks driver evidence that has no behavioral or node-state signal.
func AuthenticationLogObserved(ctx context.Context, client clientset.Interface, component, node string, since time.Time, fragments ...string) {
	gomega.Eventually(ctx, func(ctx context.Context) (bool, error) {
		return authenticationLogsMatch(ctx, client, component, node, since, func(logs string) bool {
			for _, line := range strings.Split(logs, "\n") {
				matches := true
				for _, fragment := range fragments {
					if !strings.Contains(line, fragment) {
						matches = false
						break
					}
				}
				if matches {
					return true
				}
			}
			return false
		})
	}, 5*time.Minute, 5*time.Second).Should(gomega.BeTrue(), "expected %s authentication evidence for this fixture", component)
}

func authenticationLogsMatch(ctx context.Context, client clientset.Interface, component, node string, since time.Time, match func(string) bool) (bool, error) {
	options := metav1.ListOptions{LabelSelector: "app=csi-azurefile-" + component}
	if node != "" {
		options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", node).String()
	}
	pods, err := client.CoreV1().Pods("kube-system").List(ctx, options)
	if err != nil {
		return false, err
	}
	for _, pod := range pods.Items {
		logs, err := client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &v1.PodLogOptions{
			Container: "azurefile", SinceTime: &metav1.Time{Time: since},
		}).DoRaw(ctx)
		if err != nil {
			return false, err
		}
		if match(string(logs)) {
			return true, nil
		}
	}
	return false, nil
}

func authenticationNodePod(ctx context.Context, client clientset.Interface, node string) (*v1.Pod, error) {
	pods, err := client.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
		LabelSelector: "app=csi-azurefile-node", FieldSelector: fields.OneTermEqualSelector("spec.nodeName", node).String(),
	})
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		if pods.Items[i].DeletionTimestamp == nil {
			return &pods.Items[i], nil
		}
	}
	return nil, fmt.Errorf("no azurefile node plugin on %s", node)
}

func authenticationExec(ctx context.Context, f *framework.Framework, pod *v1.Pod, container, command string) (string, error) {
	stdout, _, err := e2epod.ExecWithOptionsContext(ctx, f, e2epod.ExecOptions{
		Command: []string{"sh", "-c", command}, Namespace: pod.Namespace, PodName: pod.Name,
		ContainerName: container, CaptureStdout: true, CaptureStderr: true, Quiet: true,
	})
	return stdout, err
}

func authenticationExpectIO(ctx context.Context, f *framework.Framework, pod *v1.Pod, command string) {
	gomega.Eventually(ctx, func(ctx context.Context) error {
		_, err := authenticationExec(ctx, f, pod, "volume-tester", command)
		return err
	}, 2*time.Minute, 2*time.Second).Should(gomega.Succeed(), "the original mounted volume must retain authenticated data access")
}

// RestartAuthenticationNodePlugin only runs after kubelet has removed every mount
// of this fixture's share. It never force-unmounts other volumes or destroys shared sessions.
func RestartAuthenticationNodePlugin(ctx context.Context, f *framework.Framework, node, source string) {
	old := authenticationWaitForShareUnmount(ctx, f, node, source)
	framework.ExpectNoError(f.ClientSet.CoreV1().Pods(old.Namespace).Delete(ctx, old.Name, metav1.DeleteOptions{}))
	gomega.Eventually(ctx, func(ctx context.Context) bool {
		pod, err := authenticationNodePod(ctx, f.ClientSet, node)
		if err != nil || pod.UID == old.UID {
			return false
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == v1.PodReady && condition.Status == v1.ConditionTrue {
				return true
			}
		}
		return false
	}, 10*time.Minute, 5*time.Second).Should(gomega.BeTrue(), "a replacement node plugin must be ready")
}

func authenticationWaitForShareUnmount(ctx context.Context, f *framework.Framework, node, source string) *v1.Pod {
	pod, err := authenticationNodePod(ctx, f.ClientSet, node)
	framework.ExpectNoError(err)
	gomega.Eventually(ctx, func(ctx context.Context) error {
		_, err := authenticationExec(ctx, f, pod, "azurefile",
			fmt.Sprintf("awk -v source='%s' '$1 == source {found=1} END {exit found}' /proc/mounts", source))
		return err
	}, 5*time.Minute, 5*time.Second).Should(gomega.Succeed(), "the test share must be fully unstaged before a fresh authentication attempt")
	return pod
}
