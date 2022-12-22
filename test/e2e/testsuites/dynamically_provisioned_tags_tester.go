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
	"github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"

	azurefile "sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	azureUtils "sigs.k8s.io/azurefile-csi-driver/test/utils/azure"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/credentials"
)

// DynamicallyProvisionedAccountWithTags will provision required StorageClass(es), PVC(s) and Pod(s)
// Testing if the storage account contains tags
type DynamicallyProvisionedAccountWithTags struct {
	CSIDriver              driver.DynamicPVTestDriver
	Volumes                []VolumeDetails
	StorageClassParameters map[string]string
	Tags                   string
}

func (t *DynamicallyProvisionedAccountWithTags) Run(client clientset.Interface, namespace *v1.Namespace) {
	for _, volume := range t.Volumes {
		tpvc, _ := volume.SetupDynamicPersistentVolumeClaim(client, namespace, t.CSIDriver, t.StorageClassParameters)
		defer tpvc.Cleanup()

		ginkgo.By("checking whether the storage account contains tags")

		pvName := tpvc.persistentVolume.ObjectMeta.Name
		pv, err := client.CoreV1().PersistentVolumes().Get(context.TODO(), pvName, metav1.GetOptions{})
		framework.ExpectNoError(err, fmt.Sprintf("failed to get pv(%s): %v", pvName, err))

		volumeID := pv.Spec.PersistentVolumeSource.CSI.VolumeHandle
		resourceGroupName, accountName, _, _, _, _, err := azurefile.GetFileShareInfo(volumeID)
		framework.ExpectNoError(err, fmt.Sprintf("failed to get fileShare(%s) info: %v", volumeID, err))

		creds, err := credentials.CreateAzureCredentialFile(false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		azureClient, err := azureUtils.GetAzureClient(creds.Cloud, creds.SubscriptionID, creds.AADClientID, creds.TenantID, creds.AADClientSecret)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		account, err := azureClient.GetStorageAccount(context.TODO(), resourceGroupName, accountName)
		framework.ExpectNoError(err, fmt.Sprintf("failed to get storage account(%s): %v", accountName, err))

		resultTags := account.Tags

		specifiedTags, err := azurefile.ConvertTagsToMap(t.Tags)
		framework.ExpectNoError(err, fmt.Sprintf("failed to convert tags(%s) %v", t.Tags, err))
		specifiedTags["k8s-azure-created-by"] = "azure"

		for k, v := range specifiedTags {
			_, ok := resultTags[k]
			if ok {
				framework.ExpectEqual(*resultTags[k], v)
			} else {
				framework.Failf("the specified key(%s) does exist in result tags", k)
			}
		}
	}
}
