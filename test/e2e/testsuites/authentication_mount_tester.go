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

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epv "k8s.io/kubernetes/test/e2e/framework/pv"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
)

// AuthenticationMountTest verifies the SMB authentication route and data persistence across pods.
// Azure account, share, and identity fixtures are configured by the calling spec.
type AuthenticationMountTest struct {
	CSIDriver             driver.PVTestDriver
	VolumeAttributes      map[string]string
	EntryPoint            string
	AccountName           string
	ShareName             string
	StorageEndpointSuffix string
	Security              string
	Username              string
	ServiceAccountName    string
	NodeName              string
}

func (t *AuthenticationMountTest) Run(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) {
	var pvc *v1.PersistentVolumeClaim
	if t.EntryPoint == "dynamic" {
		pvc = t.createPersistentVolumeClaim(ctx, client, namespace, true)
		pv, err := client.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
		framework.ExpectNoError(err)
		_, account, share, _, _, _, err := azurefile.GetFileShareInfo(pv.Spec.CSI.VolumeHandle)
		framework.ExpectNoError(err)
		gomega.Expect(account).To(gomega.Equal(t.AccountName), "the controller must select the fresh account")
		gomega.Expect(share).To(gomega.Equal(t.ShareName))
		if t.VolumeAttributes["storeAccountKey"] != "false" {
			ginkgo.By("verifying and removing the generated key Secret so node authentication is exercised")
			removeGeneratedAccountKey(ctx, client, namespace, t.AccountName)
		}
	} else if t.EntryPoint == "static" {
		pvc = t.createPersistentVolumeClaim(ctx, client, namespace, false)
	}

	ginkgo.By("checking that no account-key Secret can mask the authentication route")
	expectNoAccountKeySecret(ctx, client, namespace)
	source := fmt.Sprintf("//%s.file.%s/%s", t.AccountName, t.StorageEndpointSuffix, t.ShareName)
	checkMount := smbMountCheckCommand(source, t.Security, t.Username)
	marker := uuid.NewString()
	commands := []string{
		fmt.Sprintf("%s && printf '%%s' '%s' > /mnt/auth/data && sync && test \"$(cat /mnt/auth/data)\" = '%s'", checkMount, marker, marker),
		fmt.Sprintf("%s && test \"$(cat /mnt/auth/data)\" = '%s'", checkMount, marker),
	}
	for i, command := range commands {
		ginkgo.By(fmt.Sprintf("deploying pod %d to verify the SMB mount and persisted data", i+1))
		pod := NewTestPod(client, namespace, command, false, "")
		pod.SetNodeSelector(map[string]string{HostNameLabel: t.NodeName})
		if t.ServiceAccountName != "" {
			pod.SetServiceAccountName(t.ServiceAccountName)
		}
		if pvc == nil {
			t.VolumeAttributes["mountOptions"] = "nosharesock"
			pod.SetupCSIInlineVolumeWithAttributes("auth", "/mnt/auth", t.VolumeAttributes, false)
		} else {
			pod.SetupVolume(pvc, "auth", "/mnt/auth", false)
		}
		pod.Create(ctx)
		cleaned := false
		ginkgo.DeferCleanup(func(ctx context.Context) {
			if !cleaned {
				pod.CleanupAndWait(ctx)
			}
		})
		ginkgo.By("checking that the mount authentication and I/O commands succeed")
		pod.WaitForSuccess(ctx)
		ginkgo.By("deleting the pod before testing the next mount")
		pod.CleanupAndWait(ctx)
		cleaned = true
	}
	ginkgo.By("checking that mounting did not create an account-key Secret")
	expectNoAccountKeySecret(ctx, client, namespace)
}

// Some kernels omit the default NTLMSSP option; the account username must still match.
const smbMountCheckScript = `
$1 == source && $2 == "/mnt/auth" && $3 == "cifs" {
	mounted = 1
	n = split($4, opts, ",")
	for (i = 1; i <= n; i++) {
		if (opts[i] ~ /^sec=/) specified = 1
		if (opts[i] == security || opts[i] == security "i") found = 1
		if (opts[i] == username) identity = 1
	}
}
END {
	exit !(mounted && (found || (security == "sec=ntlmssp" && !specified)) &&
		(username == "" || identity))
}`

func smbMountCheckCommand(source, security, username string) string {
	return fmt.Sprintf("awk -v source='%s' -v security='sec=%s' -v username='%s' '%s' /proc/mounts",
		source, security, username, smbMountCheckScript)
}

func (t *AuthenticationMountTest) createPersistentVolumeClaim(ctx context.Context, client clientset.Interface, namespace *v1.Namespace, dynamic bool) *v1.PersistentVolumeClaim {
	className, pvName := "", ""
	var pv *v1.PersistentVolume
	if dynamic {
		ginkgo.By("setting up the StorageClass")
		sc := t.CSIDriver.GetDynamicProvisionStorageClass(t.VolumeAttributes, []string{"nosharesock"}, nil, nil, nil, namespace.Name)
		created, err := client.StorageV1().StorageClasses().Create(ctx, sc, metav1.CreateOptions{})
		framework.ExpectNoError(err)
		className = created.Name
		ginkgo.DeferCleanup(func(ctx context.Context) error {
			return client.StorageV1().StorageClasses().Delete(ctx, className, metav1.DeleteOptions{})
		})
	} else {
		ginkgo.By("setting up the pre-provisioned PV")
		// An opaque handle requires account resolution from attributes rather than the handle.
		pv = t.CSIDriver.GetPersistentVolume("auth-"+uuid.NewString(), "", "100Gi", nil, namespace.Name, t.VolumeAttributes, "")
		pv.Spec.MountOptions = []string{"nosharesock"}
		pv.Spec.ClaimRef = &v1.ObjectReference{Namespace: namespace.Name, Name: "auth"}
		var err error
		pv, err = client.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
		framework.ExpectNoError(err)
		pvName = pv.Name
		ginkgo.DeferCleanup(func(ctx context.Context) error {
			if err := client.CoreV1().PersistentVolumes().Delete(ctx, pvName, metav1.DeleteOptions{}); err != nil {
				return err
			}
			return e2epv.WaitForPersistentVolumeDeleted(ctx, client, pvName, 2*time.Second, 5*time.Minute)
		})
	}
	ginkgo.By("setting up the PVC")
	pvc, err := client.CoreV1().PersistentVolumeClaims(namespace.Name).Create(ctx, &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "auth"},
		Spec: v1.PersistentVolumeClaimSpec{
			StorageClassName: &className,
			VolumeName:       pvName,
			AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			Resources:        v1.VolumeResourceRequirements{Requests: v1.ResourceList{v1.ResourceStorage: resource.MustParse("100Gi")}},
		},
	}, metav1.CreateOptions{})
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(func(ctx context.Context) error {
		if err := client.CoreV1().PersistentVolumeClaims(namespace.Name).Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
		if err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
			_, err := client.CoreV1().PersistentVolumeClaims(namespace.Name).Get(ctx, pvc.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}); err != nil {
			return err
		}
		if dynamic && pvName != "" {
			return e2epv.WaitForPersistentVolumeDeleted(ctx, client, pvName, 2*time.Second, 10*time.Minute)
		}
		return nil
	})
	ginkgo.By("waiting for the PVC to bind and checking that no node-stage key Secret is supplied")
	framework.ExpectNoError(e2epv.WaitForPersistentVolumeClaimPhase(ctx, v1.ClaimBound, client, namespace.Name, pvc.Name, framework.Poll, framework.ClaimProvisionTimeout))
	pvc, err = client.CoreV1().PersistentVolumeClaims(namespace.Name).Get(ctx, pvc.Name, metav1.GetOptions{})
	framework.ExpectNoError(err)
	pvName = pvc.Spec.VolumeName
	pv, err = client.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
	framework.ExpectNoError(err)
	gomega.Expect(pv.Spec.CSI).NotTo(gomega.BeNil())
	gomega.Expect(pv.Spec.CSI.NodeStageSecretRef).To(gomega.BeNil(), "the mount must not receive an account-key Secret from the sidecar")
	return pvc
}

func expectNoAccountKeySecret(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) {
	secrets, err := client.CoreV1().Secrets(namespace.Name).List(ctx, metav1.ListOptions{})
	framework.ExpectNoError(err)
	for _, secret := range secrets.Items {
		for key := range secret.Data {
			gomega.Expect(strings.ToLower(key)).NotTo(gomega.BeElementOf("azurestorageaccountkey", "accountkey"),
				"account-key Secret %s/%s would mask the node authentication route", secret.Namespace, secret.Name)
		}
	}
}

func removeGeneratedAccountKey(ctx context.Context, client clientset.Interface, namespace *v1.Namespace, account string) {
	secrets, err := client.CoreV1().Secrets(namespace.Name).List(ctx, metav1.ListOptions{})
	framework.ExpectNoError(err)
	removed := 0
	for _, secret := range secrets.Items {
		if string(secret.Data["azurestorageaccountname"]) == account && len(secret.Data["azurestorageaccountkey"]) > 0 {
			framework.ExpectNoError(client.CoreV1().Secrets(namespace.Name).Delete(ctx, secret.Name, metav1.DeleteOptions{}))
			removed++
		}
	}
	gomega.Expect(removed).To(gomega.Equal(1), "ARM WI-key provisioning must retain default account-key Secret creation")
}
