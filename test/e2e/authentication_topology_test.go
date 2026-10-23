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
	"fmt"
	"net"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armstorage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"
)

// The private-endpoint cases share one preconfigured account, which supports one
// Kerberos identity per node, so they never run concurrently.
var _ = ginkgo.Describe("Authentication private connectivity", ginkgo.Ordered, ginkgo.ContinueOnFailure, func() {
	f := framework.NewDefaultFramework("azurefile-auth-private")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var (
		cs         clientset.Interface
		ns         *v1.Namespace
		testDriver driver.PVTestDriver
	)

	ginkgo.BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace
		testDriver = driver.InitAzureFileDriver()
	})

	for _, scenario := range []authenticationScenario{
		{"G02", "static", "", managedToken}, {"G03", "static", "", workloadToken}, {"G04", "static", "", oauthToken},
	} {
		ginkgo.It(fmt.Sprintf("should mount the canonical hostname over a verified private endpoint with %s [%s] [auth-additional] [file.csi.azure.com]",
			scenario.auth, scenario.id), func(ctx ginkgo.SpecContext) {
			requireAdditionalAuthenticationLane()
			account, address := os.Getenv("AZUREFILE_AUTH_PRIVATE_ACCOUNT"), os.Getenv("AZUREFILE_AUTH_PRIVATE_IP")
			if account == "" || address == "" {
				ginkgo.Skip("requires a preconfigured reachable SMB OAuth private account and its private endpoint IP: AZUREFILE_AUTH_PRIVATE_ACCOUNT/AZUREFILE_AUTH_PRIVATE_IP")
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

			node := selectAuthenticationNode(ctx, cs)
			attributes := map[string]string{
				"resourceGroup":   suiteCreds.ResourceGroup,
				"storageAccount":  account,
				"shareName":       createAuthenticationShare(ctx, account),
				"secretNamespace": ns.Name,
			}
			serviceAccount := configureMountIdentity(ctx, f, scenario.auth, node, attributes, account, "", nil, true)
			// /proc/mounts records the peer CIFS connected to, unlike a DNS query made after mounting.
			peerCheck := fmt.Sprintf(`awk -v peer='addr=%s' '$2 == "/mnt/test-1" {n = split($4, opts, ","); for (i = 1; i <= n; i++) if (opts[i] == peer) found = 1} END {exit !found}' /proc/mounts`, address)
			pods := []testsuites.PodDetails{
				{
					Cmd:                authenticationMountCheck(ctx, scenario.auth, account, attributes) + " && " + peerCheck + " && echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
					Volumes:            []testsuites.VolumeDetails{authenticationVolume(scenario.entryPoint, attributes)},
					NodeSelector:       map[string]string{testsuites.HostNameLabel: node.Labels[testsuites.HostNameLabel]},
					ServiceAccountName: serviceAccount,
				},
			}
			test := testsuites.PreProvisionedMultiplePods{
				CSIDriver: testDriver,
				Pods:      pods,
			}
			test.Run(ctx, cs, ns)
		})
	}
})

var _ = ginkgo.Describe("Authentication principal sharing", func() {
	f := framework.NewDefaultFramework("azurefile-auth-sharing")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var (
		cs         clientset.Interface
		ns         *v1.Namespace
		testDriver driver.PVTestDriver
	)

	ginkgo.BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace
		testDriver = driver.InitAzureFileDriver()
	})

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
			node := selectAuthenticationNode(ctx, cs)
			account := createAuthenticationAccount(ctx, nil)
			// principal adds an identity on the account and node with its own share.
			// Kerberos roles are share-scoped, so each pod's I/O proves its own identity.
			principal := func(grantRole bool) testsuites.PodDetails {
				share := createAuthenticationShare(ctx, account)
				attributes := map[string]string{
					"resourceGroup":   suiteCreds.ResourceGroup,
					"storageAccount":  account,
					"shareName":       share,
					"secretNamespace": ns.Name,
				}
				scope := suiteAzureClient.StorageAccountScope(suiteCreds.ResourceGroup, account)
				if scenario.auth != workloadKey {
					scope += "/fileServices/default/shares/" + share
				}
				serviceAccount := configureMountIdentity(ctx, f, scenario.auth, node, attributes, account, scope, nil, grantRole)
				return testsuites.PodDetails{
					Cmd:                authenticationMountCheck(ctx, scenario.auth, account, attributes) + " && echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
					Volumes:            []testsuites.VolumeDetails{authenticationVolume(scenario.entryPoint, attributes)},
					NodeSelector:       map[string]string{testsuites.HostNameLabel: node.Labels[testsuites.HostNameLabel]},
					ServiceAccountName: serviceAccount,
				}
			}
			test := testsuites.PreProvisionedConcurrentMountsTest{
				CSIDriver: testDriver,
				Pods:      []testsuites.PodDetails{principal(true), principal(true)},
			}
			if scenario.auth == workloadKey {
				// Both WI-key principals can list the account's keys, so a roleless
				// third principal must be rejected while its peers stay mounted.
				denied := principal(false)
				denied.Cmd = "true"
				test.DeniedPods = []testsuites.PodDetails{denied}
			}
			test.Run(ctx, f, ns)
		})
	}
})
