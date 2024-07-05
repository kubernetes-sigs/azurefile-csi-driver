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
	"fmt"

	"github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"

	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
)

// PreProvisionedProvidedCredentiasTest will provision required PV(s), PVC(s) and Pod(s)
// Testing that the Pod(s) can be created successfully with provided storage account name and key(or sastoken)
type PreProvisionedProvidedCredentiasTest struct {
	CSIDriver driver.PreProvisionedVolumeTestDriver
	Pods      []PodDetails
	Azurefile *azurefile.Driver
}

func (t *PreProvisionedProvidedCredentiasTest) Run(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) {
	for _, pod := range t.Pods {
		for n, volume := range pod.Volumes {
			_, accountName, accountKey, fileShareName, _, _, err := t.Azurefile.GetAccountInfo(ctx, volume.VolumeID, nil, nil)
			framework.ExpectNoError(err, fmt.Sprintf("Error GetAccountInfo from volumeID(%s): %v", volume.VolumeID, err))
			var secretData map[string]string
			var i int
			var run = func() {
				// add suffix to fileshare name to force kubelet to NodeStageVolume every time,
				// otherwise it will skip NodeStageVolume for the same fileshare(VolumeHanlde)
				pod.Volumes[n].ShareName = fmt.Sprintf("%s-%d", fileShareName, i)
				i++

				tsecret := NewTestSecret(client, namespace, volume.NodeStageSecretRef, secretData)
				tsecret.Create(ctx)
				defer tsecret.Cleanup(ctx)

				tpod, cleanup := pod.SetupWithPreProvisionedVolumes(ctx, client, namespace, t.CSIDriver)
				// defer must be called here for resources not get removed before using them
				for i := range cleanup {
					defer cleanup[i](ctx)
				}

				ginkgo.By("deploying the pod")
				tpod.Create(ctx)
				defer func() {
					tpod.Cleanup(ctx)
				}()

				ginkgo.By("checking that the pods command exits with no error")
				tpod.WaitForSuccess(ctx)
			}

			// test for storage account key
			ginkgo.By("Run for storage account key")
			secretData = map[string]string{
				"azurestorageaccountname": accountName,
				"azurestorageaccountkey":  accountKey,
			}
			run()
		}
	}
}
