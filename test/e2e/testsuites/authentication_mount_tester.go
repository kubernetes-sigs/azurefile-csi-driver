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
	Name                  string
	RequestSecrets        AuthenticationRequestSecrets
	PrivateEndpointIP     string
	AfterProvision        func(context.Context, *v1.PersistentVolumeClaim)
	AfterMount            func(context.Context, string)
	BetweenMounts         func(context.Context, string)
	CloneSource           string
}

// AuthenticationRequestSecrets explicitly separates sidecar/kubelet credentials.
// These are not driver-read secretName attributes.
type AuthenticationRequestSecrets struct {
	Provisioner      string
	NodeStage        string
	ControllerExpand string
}

func (t *AuthenticationMountTest) claimName() string {
	if t.Name != "" {
		return t.Name
	}
	return "auth"
}

func (t *AuthenticationMountTest) Run(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) {
	RunAuthenticationRemounts(ctx, client, namespace, []*AuthenticationMountTest{t}, nil)
}

// RunAuthenticationRemounts mounts and removes every test's pod, runs each
// BetweenMounts hook and then the shared between hook once, and mounts every
// test again to verify the persisted data.
func RunAuthenticationRemounts(ctx context.Context, client clientset.Interface, namespace *v1.Namespace, tests []*AuthenticationMountTest, between func(context.Context)) {
	pvcs := make([]*v1.PersistentVolumeClaim, len(tests))
	markers := make([]string, len(tests))
	for i, t := range tests {
		pvcs[i] = t.prepare(ctx, client, namespace)
		markers[i] = uuid.NewString()
	}
	for round := 0; round < 2; round++ {
		for i, t := range tests {
			command := fmt.Sprintf("%s && test \"$(cat /mnt/auth/data)\" = '%s'", t.mountCheck(), markers[i])
			if round == 0 {
				command = fmt.Sprintf("%s && printf '%%s' '%s' > /mnt/auth/data && sync && test \"$(cat /mnt/auth/data)\" = '%s'", t.mountCheck(), markers[i], markers[i])
			}
			ginkgo.By(fmt.Sprintf("deploying pod %d to verify the SMB mount and persisted data", round+1))
			pod := t.newPod(client, namespace, pvcs[i], command)
			pod.Create(ctx)
			cleaned := false
			ginkgo.DeferCleanup(func(ctx context.Context) {
				if !cleaned {
					pod.CleanupAndWait(ctx)
				}
			})
			ginkgo.By("checking that the mount authentication and I/O commands succeed")
			pod.WaitForSuccess(ctx)
			if t.AfterMount != nil {
				t.AfterMount(ctx, t.volumeHandle(ctx, client, pvcs[i]))
			}
			ginkgo.By("deleting the pod before testing the next mount")
			pod.CleanupAndWait(ctx)
			cleaned = true
		}
		if round == 0 {
			for i, t := range tests {
				if t.BetweenMounts != nil {
					t.BetweenMounts(ctx, t.volumeHandle(ctx, client, pvcs[i]))
				}
			}
			if between != nil {
				between(ctx)
			}
		}
	}
	ginkgo.By("checking that mounting did not create an unexpected account-key Secret")
	for _, t := range tests {
		t.expectAccountKeySecrets(ctx, client, namespace)
	}
}

func (t *AuthenticationMountTest) volumeHandle(ctx context.Context, client clientset.Interface, pvc *v1.PersistentVolumeClaim) string {
	if pvc == nil {
		return ""
	}
	pv, err := client.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
	framework.ExpectNoError(err)
	return pv.Spec.CSI.VolumeHandle
}

func (t *AuthenticationMountTest) prepare(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) *v1.PersistentVolumeClaim {
	var pvc *v1.PersistentVolumeClaim
	if t.EntryPoint == "dynamic" {
		pvc = t.createPersistentVolumeClaim(ctx, client, namespace, true)
		pv, err := client.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
		framework.ExpectNoError(err)
		_, account, share, _, _, _, err := azurefile.GetFileShareInfo(pv.Spec.CSI.VolumeHandle)
		framework.ExpectNoError(err)
		gomega.Expect(account).To(gomega.Equal(t.AccountName), "the controller must select the fresh account")
		gomega.Expect(share).To(gomega.Equal(t.ShareName))
		if t.VolumeAttributes["storeAccountKey"] != "false" && t.RequestSecrets.Provisioner == "" {
			ginkgo.By("verifying and removing the generated key Secret so node authentication is exercised")
			removeGeneratedAccountKey(ctx, client, namespace, t.AccountName)
		}
	} else if t.EntryPoint == "static" {
		pvc = t.createPersistentVolumeClaim(ctx, client, namespace, false)
	}

	ginkgo.By("checking that no account-key Secret can mask the authentication route")
	t.expectAccountKeySecrets(ctx, client, namespace)
	if t.AfterProvision != nil {
		t.AfterProvision(ctx, pvc)
	}
	return pvc
}

func (t *AuthenticationMountTest) mountCheck() string {
	source := fmt.Sprintf("//%s.file.%s/%s", t.AccountName, t.StorageEndpointSuffix, t.ShareName)
	checkMount := smbMountCheckCommand(source, t.Security, t.Username)
	if t.PrivateEndpointIP != "" {
		// Mountinfo records the peer used by CIFS, unlike a DNS query made after mounting.
		checkMount += fmt.Sprintf(" && awk -v source='%s' -v peer='addr=%s' '$1 == source && $2 == \"/mnt/auth\" {n=split($4,a,\",\"); for(i=1;i<=n;i++) if(a[i]==peer) found=1} END {exit !found}' /proc/mounts", source, t.PrivateEndpointIP)
	}
	return checkMount
}

func (t *AuthenticationMountTest) newPod(client clientset.Interface, namespace *v1.Namespace, pvc *v1.PersistentVolumeClaim, command string) *TestPod {
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
	return pod
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
		if t.RequestSecrets.ControllerExpand != "" {
			sc.AllowVolumeExpansion = new(bool)
			*sc.AllowVolumeExpansion = true
		}
		for operation, name := range map[string]string{
			"provisioner": t.RequestSecrets.Provisioner, "node-stage": t.RequestSecrets.NodeStage, "controller-expand": t.RequestSecrets.ControllerExpand,
		} {
			if name != "" {
				sc.Parameters["csi.storage.k8s.io/"+operation+"-secret-name"] = name
				sc.Parameters["csi.storage.k8s.io/"+operation+"-secret-namespace"] = namespace.Name
			}
		}
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
		pv.Spec.ClaimRef = &v1.ObjectReference{Namespace: namespace.Name, Name: t.claimName()}
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
	claim := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: t.claimName()},
		Spec: v1.PersistentVolumeClaimSpec{
			StorageClassName: &className,
			VolumeName:       pvName,
			AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			Resources:        v1.VolumeResourceRequirements{Requests: v1.ResourceList{v1.ResourceStorage: resource.MustParse("100Gi")}},
		},
	}
	if t.CloneSource != "" {
		claim.Spec.DataSource = &v1.TypedLocalObjectReference{Kind: "PersistentVolumeClaim", Name: t.CloneSource}
	}
	pvc, err := client.CoreV1().PersistentVolumeClaims(namespace.Name).Create(ctx, claim, metav1.CreateOptions{})
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
	expectAuthenticationSecretRef(pv.Spec.CSI.NodeStageSecretRef, t.RequestSecrets.NodeStage, namespace.Name)
	expectAuthenticationSecretRef(pv.Spec.CSI.ControllerExpandSecretRef, t.RequestSecrets.ControllerExpand, namespace.Name)
	return pvc
}

func expectAuthenticationSecretRef(ref *v1.SecretReference, name, namespace string) {
	if name == "" {
		gomega.Expect(ref).To(gomega.BeNil(), "this operation must not receive an account-key Secret")
		return
	}
	gomega.Expect(ref).To(gomega.Equal(&v1.SecretReference{Name: name, Namespace: namespace}))
}

func (t *AuthenticationMountTest) expectAccountKeySecrets(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) {
	secrets, err := client.CoreV1().Secrets(namespace.Name).List(ctx, metav1.ListOptions{})
	framework.ExpectNoError(err)
	if t.RequestSecrets == (AuthenticationRequestSecrets{}) {
		framework.ExpectNoError(validateNoAuthenticationAccountKeySecrets(secrets.Items, t.AccountName, t.VolumeAttributes["secretName"]))
		return
	}
	framework.ExpectNoError(validateAuthenticationAccountKeySecrets(secrets.Items, t.RequestSecrets, t.AccountName))
}

func validateAuthenticationAccountKeySecrets(secrets []v1.Secret, refs AuthenticationRequestSecrets, account string) error {
	allowed := map[string]bool{}
	for _, name := range []string{refs.Provisioner, refs.NodeStage, refs.ControllerExpand} {
		if name != "" {
			allowed[name] = false
		}
	}
	for _, secret := range secrets {
		for key := range secret.Data {
			if strings.EqualFold(key, "azurestorageaccountkey") || strings.EqualFold(key, "accountkey") {
				_, expected := allowed[secret.Name]
				if !expected {
					return fmt.Errorf("unexpected account-key Secret %s", secret.Name)
				}
				if string(secret.Data["azurestorageaccountname"]) != account || len(secret.Data[key]) == 0 {
					return fmt.Errorf("operation Secret %s must contain a usable key for this account", secret.Name)
				}
				allowed[secret.Name] = true
			}
		}
	}
	for name, found := range allowed {
		if !found {
			return fmt.Errorf("operation Secret %s must contain a usable account key", name)
		}
	}
	return nil
}

func validateNoAuthenticationAccountKeySecrets(secrets []v1.Secret, account, secretName string) error {
	for _, secret := range secrets {
		relevant := secret.Name == fmt.Sprintf("azure-storage-account-%s-secret", account) ||
			(secretName != "" && secret.Name == secretName)
		for key, value := range secret.Data {
			if (strings.EqualFold(key, "azurestorageaccountname") || strings.EqualFold(key, "accountname")) &&
				strings.EqualFold(string(value), account) {
				relevant = true
			}
		}
		if !relevant {
			continue
		}
		for key := range secret.Data {
			if strings.EqualFold(key, "azurestorageaccountkey") || strings.EqualFold(key, "accountkey") {
				return fmt.Errorf("account-key Secret %s/%s would mask the node authentication route", secret.Namespace, secret.Name)
			}
		}
	}
	return nil
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
