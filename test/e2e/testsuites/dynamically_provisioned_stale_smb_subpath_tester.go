/*
Copyright 2024 The Kubernetes Authors.

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

	"github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
)

// staleSMBChaosCommand removes every node-global SMB mapping on the node, simulating a
// stale/invalidated SMB session (credential rotation, idle/NAT teardown, storage
// maintenance, etc.) without replacing the node. It is best-effort and always exits 0.
const staleSMBChaosCommand = `try { Get-SmbGlobalMapping | Remove-SmbGlobalMapping -Force -ErrorAction SilentlyContinue } catch {}; exit 0`

// DynamicallyProvisionedStaleSMBSubpathTester verifies the Windows self-heal for stale
// node-global SMB sessions on the per-pod publish path.
//
// Scenario:
//  1. Pod A mounts a subPath of an SMB share and keeps running. This establishes the
//     node-global SMB mapping and keeps the volume staged (so kubelet does not unstage
//     when a single pod is replaced).
//  2. A HostProcess "chaos" pod on the same node tears down the node-global SMB
//     mapping. Pod A survives on its already-open handles.
//  3. Pod B is created on the same node and mounts a (different) subPath of the same
//     share. Before the fix, Pod B fails with "failed to prepare subPath for
//     volumeMount" because NodePublishVolume binds against the dead session and never
//     re-validates it; only node replacement recovers. With the fix, NodePublishVolume
//     detects the missing/stale mapping, remaps it, and Pod B reaches Running.
type DynamicallyProvisionedStaleSMBSubpathTester struct {
	CSIDriver              driver.DynamicPVTestDriver
	Pod                    PodDetails
	StorageClassParameters map[string]string
	WinServerVer           string
}

func (t *DynamicallyProvisionedStaleSMBSubpathTester) Run(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) {
	if len(t.Pod.Volumes) == 0 {
		framework.Failf("DynamicallyProvisionedStaleSMBSubpathTester requires at least one volume")
	}
	volume := t.Pod.Volumes[0]

	// Provision a single shared PVC up front so both pods reference the same staged volume.
	tpvc, cleanupFuncs := volume.SetupDynamicPersistentVolumeClaim(ctx, client, namespace, t.CSIDriver, t.StorageClassParameters)
	for i := range cleanupFuncs {
		defer cleanupFuncs[i](ctx)
	}
	pvc := tpvc.persistentVolumeClaim

	ginkgo.By("deploying pod A which establishes the node-global SMB mount")
	podA := NewTestPod(client, namespace, t.Pod.Cmd, t.Pod.IsWindows, t.WinServerVer)
	podA.SetupVolumeMountWithSubpath(pvc, "stale-smb-volume", "/mnt/test-1", "stalesubpath-a", false)
	podA.Create(ctx)
	defer podA.Cleanup(ctx)
	podA.WaitForRunning(ctx)

	nodeName := podA.GetNodeName(ctx)
	gomegaExpectNonEmptyNode(nodeName)

	ginkgo.By("invalidating the node-global SMB session on node " + nodeName + " via a HostProcess chaos pod")
	chaos := NewTestPod(client, namespace, staleSMBChaosCommand, true, t.WinServerVer)
	chaos.SetHostProcess()
	chaos.SetNodeName(nodeName)
	chaos.Create(ctx)
	defer chaos.Cleanup(ctx)
	chaos.WaitForSuccess(ctx)

	ginkgo.By("deploying pod B on the same node after the SMB session was invalidated; it must self-heal and reach Running")
	podB := NewTestPod(client, namespace, t.Pod.Cmd, t.Pod.IsWindows, t.WinServerVer)
	podB.SetupVolumeMountWithSubpath(pvc, "stale-smb-volume", "/mnt/test-1", "stalesubpath-b", false)
	podB.SetNodeName(nodeName)
	podB.Create(ctx)
	defer podB.Cleanup(ctx)
	podB.WaitForRunning(ctx)
}

func gomegaExpectNonEmptyNode(nodeName string) {
	if nodeName == "" {
		framework.Failf("pod A was not scheduled to any node; cannot co-locate chaos and pod B")
	}
}
