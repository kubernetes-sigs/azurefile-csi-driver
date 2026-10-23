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

package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armstorage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epv "k8s.io/kubernetes/test/e2e/framework/pv"
	admissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"
)

// The private-endpoint cases share one account, which supports one Kerberos
// identity per node, so they never run concurrently.
var _ = ginkgo.Describe("Authentication private connectivity", ginkgo.Ordered, ginkgo.ContinueOnFailure, func() {
	f := framework.NewDefaultFramework("azurefile-auth-private")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged
	var fixture privateAuthenticationFixture
	// Cleanup registered in BeforeAll runs after every case's own DeferCleanup,
	// unlike AfterAll, so per-case shares are deleted before the account.
	ginkgo.BeforeAll(func() {
		ginkgo.DeferCleanup(fixture.cleanup)
	})
	for _, scenario := range []authenticationScenario{
		{"G02", "static", "", managedToken}, {"G03", "static", "", workloadToken}, {"G04", "static", "", oauthToken},
	} {
		ginkgo.It(fmt.Sprintf("should mount the canonical hostname over a verified private endpoint with %s [%s] [auth-additional] [file.csi.azure.com]",
			scenario.auth, scenario.id), func(ctx ginkgo.SpecContext) {
			requireAdditionalAuthenticationLane()
			account, address := os.Getenv("AZUREFILE_AUTH_PRIVATE_ACCOUNT"), os.Getenv("AZUREFILE_AUTH_PRIVATE_IP")
			if (account == "") != (address == "") {
				ginkgo.Fail("set both AZUREFILE_AUTH_PRIVATE_ACCOUNT and AZUREFILE_AUTH_PRIVATE_IP, or neither to provision a private account through the driver")
			}
			if account == "" {
				account, address = fixture.ensure(ctx, f)
			}
			ip := net.ParseIP(address)
			gomega.Expect(ip).NotTo(gomega.BeNil())
			gomega.Expect(ip.IsPrivate() && ip.To4() != nil).To(gomega.BeTrue(), "this fixture requires a private IPv4 CIFS peer")
			properties, err := suiteAzureClient.GetStorageAccount(ctx, suiteCreds.ResourceGroup, account)
			framework.ExpectNoError(err)
			gomega.Expect(properties.Properties).NotTo(gomega.BeNil())
			gomega.Expect(properties.Properties.PublicNetworkAccess).To(gomega.Equal(to.Ptr(armstorage.PublicNetworkAccessDisabled)))
			identitySettings := properties.Properties.AzureFilesIdentityBasedAuthentication
			gomega.Expect(identitySettings).NotTo(gomega.BeNil())
			gomega.Expect(identitySettings.SmbOAuthSettings).NotTo(gomega.BeNil())
			gomega.Expect(identitySettings.SmbOAuthSettings.IsSmbOAuthEnabled).To(gomega.Equal(to.Ptr(true)))
			approved := false
			for _, connection := range properties.Properties.PrivateEndpointConnections {
				if connection != nil && connection.Properties != nil && connection.Properties.PrivateEndpoint != nil &&
					connection.Properties.PrivateEndpoint.ID != nil && connection.Properties.PrivateLinkServiceConnectionState != nil &&
					connection.Properties.PrivateLinkServiceConnectionState.Status != nil &&
					*connection.Properties.PrivateLinkServiceConnectionState.Status == armstorage.PrivateEndpointServiceConnectionStatusApproved {
					approved = true
				}
			}
			gomega.Expect(approved).To(gomega.BeTrue(), "a real approved private endpoint connection is required")
			since := time.Now()
			var node *v1.Node
			runAuthenticationScenarioWithOptions(ctx, f, scenario, authenticationFixtureOptions{
				preparedAccount: account, selectedNode: &node,
				configureTest: func(test *testsuites.AuthenticationMountTest) {
					test.PrivateEndpointIP = address
					test.AfterMount = func(ctx context.Context, handle string) {
						observeAdditionalMount(ctx, f, test, node.Name, since, handle)
					}
				},
			})
		})
	}
})

var _ = ginkgo.Describe("Authentication principal sharing", func() {
	f := framework.NewDefaultFramework("azurefile-auth-sharing")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged
	for _, scenario := range []authenticationScenario{
		{"G08", "static", "", workloadKey}, {"G09", "static", "", managedToken},
		{"G10", "static", "", workloadToken}, {"G11", "static", "", oauthToken},
	} {
		labels := ginkgo.Label("auth-principal-sharing")
		if scenario.auth != workloadKey {
			labels = ginkgo.Label("auth-investigation")
		}
		ginkgo.It(fmt.Sprintf("should retain both principals' concurrent access to separate shares on one account and node using %s [%s] [auth-additional] [file.csi.azure.com]",
			scenario.auth, scenario.id), labels, func(ctx ginkgo.SpecContext) {
			requireAdditionalAuthenticationLane()
			if scenario.auth != workloadKey && os.Getenv("AZUREFILE_MULTI_IDENTITY_KERBEROS") != "true" {
				ginkgo.Skip("requires AZUREFILE_MULTI_IDENTITY_KERBEROS=true to investigate the unsupported multi-principal Kerberos target")
			}
			since := time.Now()
			var node *v1.Node
			runAuthenticationScenarioWithOptions(ctx, f, scenario, authenticationFixtureOptions{
				shareScoped: true, selectedNode: &node,
				run: func(_ context.Context, first *testsuites.AuthenticationMountTest, f *framework.Framework, namespace *v1.Namespace) {
					first.Name = "auth-a"
					second := *first
					second.Name = "auth-b"
					second.ShareName = "auth-" + uuid.NewString()
					second.VolumeAttributes = map[string]string{
						"resourceGroup": suiteCreds.ResourceGroup, "storageAccount": first.AccountName,
						"shareName": second.ShareName, "secretNamespace": namespace.Name,
					}
					client, err := suiteAzureClient.GetAzureFilesClient()
					framework.ExpectNoError(err)
					_, err = client.Create(ctx, suiteCreds.ResourceGroup, second.AccountName, second.ShareName, armstorage.FileShare{
						FileShareProperties: &armstorage.FileShareProperties{ShareQuota: to.Ptr(int32(100))},
					}, nil)
					framework.ExpectNoError(err)
					scope := suiteAzureClient.StorageAccountScope(suiteCreds.ResourceGroup, second.AccountName)
					if scenario.auth != workloadKey {
						scope += "/fileServices/default/shares/" + second.ShareName
					}
					second.ServiceAccountName = configureMountIdentity(ctx, f, scenario.auth, node, second.VolumeAttributes, second.AccountName, scope, nil)
					if scenario.auth == managedToken {
						second.Username = "username=" + second.VolumeAttributes["clientID"]
					}
					for _, test := range []*testsuites.AuthenticationMountTest{first, &second} {
						observedSince := since
						test.AfterMount = func(ctx context.Context, handle string) {
							observeAdditionalMount(ctx, f, test, node.Name, observedSince, handle)
						}
						test.BetweenMounts = func(context.Context, string) {
							// Node log timestamps can lead the test clock, and sinceTime has
							// second precision; the pod delete and unmount wait keep the
							// previous mount's evidence well outside this margin.
							observedSince = time.Now().Add(-5 * time.Second)
						}
					}
					testsuites.RunConcurrentAuthentication(ctx, f, namespace, [2]*testsuites.AuthenticationMountTest{first, &second})
				},
			})
		})
	}
})

// privateAuthenticationFixture asks the driver to create an SMB OAuth account
// with public network access disabled and a private endpoint, private DNS zone
// and VNet link, so CI needs no preconfigured network fixture. The shared DNS
// zone and link are left to the cluster resource group teardown.
type privateAuthenticationFixture struct {
	account, address, endpointID string
}

func (p *privateAuthenticationFixture) ensure(ctx ginkgo.SpecContext, f *framework.Framework) (string, string) {
	if p.address != "" {
		return p.account, p.address
	}
	gomega.Expect(p.account).To(gomega.BeEmpty(), "a previous private account provisioning attempt failed")
	cs := f.ClientSet
	ginkgo.By("provisioning a private-endpoint SMB OAuth account through the driver")
	reclaim := v1.PersistentVolumeReclaimDelete
	binding := storagev1.VolumeBindingImmediate
	class, err := cs.StorageV1().StorageClasses().Create(ctx, &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{GenerateName: "azurefile-auth-private-"},
		Provisioner: azurefile.DefaultDriverName,
		Parameters: map[string]string{
			"resourceGroup":            suiteCreds.ResourceGroup,
			"skuName":                  "Standard_LRS",
			"createAccount":            "true",
			"networkEndpointType":      "privateEndpoint",
			"publicNetworkAccess":      "Disabled",
			"mountWithManagedIdentity": "true",
			// The fixture deletes this account, so keep other StorageClasses,
			// which default to shared-key access, from placing shares on it.
			"allowSharedKeyAccess": "false",
		},
		ReclaimPolicy:     &reclaim,
		VolumeBindingMode: &binding,
	}, metav1.CreateOptions{})
	framework.ExpectNoError(err)
	defer func() {
		framework.ExpectNoError(cs.StorageV1().StorageClasses().Delete(context.Background(), class.Name, metav1.DeleteOptions{}))
	}()
	claim, err := cs.CoreV1().PersistentVolumeClaims(f.Namespace.Name).Create(ctx, &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "azurefile-auth-private-"},
		Spec: v1.PersistentVolumeClaimSpec{
			StorageClassName: &class.Name,
			AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
			Resources:        v1.VolumeResourceRequirements{Requests: v1.ResourceList{v1.ResourceStorage: resource.MustParse("100Gi")}},
		},
	}, metav1.CreateOptions{})
	framework.ExpectNoError(err)
	framework.ExpectNoError(e2epv.WaitForPersistentVolumeClaimPhase(ctx, v1.ClaimBound, cs, claim.Namespace, claim.Name, framework.Poll, framework.ClaimProvisionTimeout))
	claim, err = cs.CoreV1().PersistentVolumeClaims(claim.Namespace).Get(ctx, claim.Name, metav1.GetOptions{})
	framework.ExpectNoError(err)
	pv, err := cs.CoreV1().PersistentVolumes().Get(ctx, claim.Spec.VolumeName, metav1.GetOptions{})
	framework.ExpectNoError(err)
	gomega.Expect(pv.Spec.CSI).NotTo(gomega.BeNil())
	resourceGroup, account, _, _, _, _, err := azurefile.GetFileShareInfo(pv.Spec.CSI.VolumeHandle)
	framework.ExpectNoError(err)
	p.account = account
	gomega.Expect(resourceGroup).To(gomega.Equal(suiteCreds.ResourceGroup))

	// The driver never deletes accounts, so dropping the claim removes only its share.
	framework.ExpectNoError(e2epv.DeletePersistentVolumeClaim(ctx, cs, claim.Name, claim.Namespace))
	framework.ExpectNoError(e2epv.WaitForPersistentVolumeDeleted(ctx, cs, pv.Name, 2*time.Second, 10*time.Minute))

	p.endpointID, p.address, err = suiteAzureClient.GetApprovedPrivateEndpoint(ctx, suiteCreds.ResourceGroup, account)
	framework.ExpectNoError(err)
	return p.account, p.address
}

func (p *privateAuthenticationFixture) cleanup(ctx context.Context) {
	if p.endpointID != "" {
		framework.ExpectNoError(suiteAzureClient.DeletePrivateEndpoint(ctx, p.endpointID))
	}
	if p.account != "" {
		framework.ExpectNoError(suiteAzureClient.DeleteStorageAccount(ctx, suiteCreds.ResourceGroup, p.account))
	}
}
