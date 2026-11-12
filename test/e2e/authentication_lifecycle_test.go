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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"
)

// Renewal changes the CSIDriver's token settings and restart deletes a node
// plugin pod, so both run serially and are skipped by --label-filter='!Disruptive'.
var _ = framework.Describe("Authentication credential renewal on an existing mount", ginkgo.Label("auth"),
	framework.WithSerial(), framework.WithDisruptive(), framework.WithSlow(), func() {
		f := framework.NewDefaultFramework("azurefile-auth-renewal")
		f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

		var (
			ns         *v1.Namespace
			testDriver driver.PVTestDriver
		)

		ginkgo.BeforeEach(func() {
			ns = f.Namespace
			testDriver = driver.InitAzureFileDriver()
		})

		ginkgo.DescribeTable("should keep the mount through unchanged and renewed credentials without replacing its pod",
			func(ctx ginkgo.SpecContext, scenario authenticationScenario) {
				requireAuthenticationFixtures()
				configureAuthenticationRepublish(ctx, f)
				node := selectAuthenticationNode(ctx, f.ClientSet)
				account := createAuthenticationAccount(ctx, nil)
				attributes := authenticationAttributes(account, ns.Name)
				attributes["shareName"] = createAuthenticationShare(ctx, account)
				mount := newAuthenticationMount(ctx, f, scenario.auth, node, account, attributes, resourceGroupRole)
				pod := mount.pod(ctx, scenario.volume)
				pod.Cmd = mount.mountCheck(ctx)
				_, env := suiteCloud()
				test := testsuites.PreProvisionedCredentialRenewalTest{
					CSIDriver:       testDriver,
					Pod:             pod,
					CSIInlineVolume: scenario.volume == inlineVolume,
					Server:          fmt.Sprintf("%s.file.%s", account, env.StorageEndpointSuffix),
					TokenCacheFile:  attributes["clientID"] + "-" + account,
				}
				if scenario.auth == oauthToken {
					test.OAuthSecretName = attributes["secretName"]
					test.RotateOAuthSecret = func(ctx context.Context) error {
						secret, err := f.ClientSet.CoreV1().Secrets(ns.Name).Get(ctx, test.OAuthSecretName, metav1.GetOptions{})
						if err != nil {
							return err
						}
						original := string(secret.Data["oauthtoken"])
						expiry, err := testsuites.AuthenticationCredentialExpiry(original)
						if err != nil {
							return err
						}
						var replacement string
						gomega.Eventually(ctx, func(ctx context.Context) bool {
							token, err := mount.storageToken(ctx)
							if err != nil || token.Token == "" || token.Token == original || !token.ExpiresOn.After(expiry.Add(5*time.Minute)) {
								return false
							}
							replacement = token.Token
							return true
						}, 20*time.Minute, 15*time.Second).Should(gomega.BeTrue(), "AAD must issue a different storage access token valid beyond the original expiry/grace period; a changed Secret resourceVersion is insufficient")
						secret.Data["oauthtoken"] = []byte(replacement)
						_, err = f.ClientSet.CoreV1().Secrets(ns.Name).Update(ctx, secret, metav1.UpdateOptions{})
						return err
					}
				}
				test.Run(ctx, f, ns)
			},
			authenticationEntryDescription,
			// Bounded by the tester's own waits (about 45 minutes) and below the suite timeout.
			ginkgo.Entry(nil, authenticationScenario{staticVolume, "", workloadToken}, ginkgo.SpecTimeout(time.Hour)),
			ginkgo.Entry(nil, authenticationScenario{inlineVolume, "", workloadToken}, ginkgo.SpecTimeout(time.Hour)),
			ginkgo.Entry(nil, authenticationScenario{staticVolume, "", oauthToken}, ginkgo.SpecTimeout(time.Hour)),
		)
	})

var _ = framework.Describe("Authentication recovery after a node plugin restart", ginkgo.Label("auth"),
	framework.WithSerial(), framework.WithDisruptive(), func() {
		f := framework.NewDefaultFramework("azurefile-auth-restart")
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

		ginkgo.DescribeTable("should reacquire credentials and freshly mount",
			func(ctx ginkgo.SpecContext, scenario authenticationScenario) {
				requireAuthenticationFixtures()
				node := selectAuthenticationNode(ctx, cs)
				account := createAuthenticationAccount(ctx, nil)
				attributes := authenticationAttributes(account, ns.Name)
				attributes["shareName"] = createAuthenticationShare(ctx, account)
				mount := newAuthenticationMount(ctx, f, scenario.auth, node, account, attributes, resourceGroupRole)
				test := testsuites.PreProvisionedMultiplePods{
					CSIDriver: testDriver,
					Pods:      []testsuites.PodDetails{mount.pod(ctx, scenario.volume)},
				}
				test.Run(ctx, cs, ns)

				_, env := suiteCloud()
				server := fmt.Sprintf("%s.file.%s", account, env.StorageEndpointSuffix)
				before := testsuites.AuthenticationTicketStart(ctx, f, node.Name, server)
				gomega.Expect(before).To(gomega.BeNumerically(">", 0), "the first mount must leave a Kerberos ticket for this account")
				// The restart waits for the share to unmount, so the next mount authenticates again.
				testsuites.RestartAuthenticationNodePlugin(ctx, f, node.Name, fmt.Sprintf("//%s/%s", server, attributes["shareName"]))
				test.Run(ctx, cs, ns)
				// The host ticket cache survives the restart; a newer ticket proves the remount did not reuse it.
				gomega.Expect(testsuites.AuthenticationTicketStart(ctx, f, node.Name, server)).To(gomega.BeNumerically(">", before),
					"the remount after a node plugin restart must acquire a new Kerberos ticket")
			},
			authenticationEntryDescription,
			ginkgo.Entry(nil, authenticationScenario{staticVolume, "", managedToken}),
			ginkgo.Entry(nil, authenticationScenario{staticVolume, "", workloadToken}),
			ginkgo.Entry(nil, authenticationScenario{staticVolume, "", oauthToken}),
		)
	})

func configureAuthenticationRepublish(ctx ginkgo.SpecContext, f *framework.Framework) {
	current, err := f.ClientSet.StorageV1().CSIDrivers().Get(ctx, "file.csi.azure.com", metav1.GetOptions{})
	framework.ExpectNoError(err)
	original := current.DeepCopy()
	current.Spec.RequiresRepublish = to.Ptr(true)
	found := false
	for i := range current.Spec.TokenRequests {
		if current.Spec.TokenRequests[i].Audience == "api://AzureADTokenExchange" {
			current.Spec.TokenRequests[i].ExpirationSeconds = to.Ptr(int64(600))
			found = true
		}
	}
	if !found {
		current.Spec.TokenRequests = append(current.Spec.TokenRequests, storagev1.TokenRequest{Audience: "api://AzureADTokenExchange", ExpirationSeconds: to.Ptr(int64(600))})
	}
	_, err = f.ClientSet.StorageV1().CSIDrivers().Update(ctx, current, metav1.UpdateOptions{})
	framework.ExpectNoError(err, "the isolated cluster must permit CSIDriver tokenRequests/requiresRepublish updates; the test will not delete/recreate shared driver configuration")
	ginkgo.DeferCleanup(func(ctx context.Context) error {
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := f.ClientSet.StorageV1().CSIDrivers().Get(ctx, original.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			current.Spec.TokenRequests = original.Spec.TokenRequests
			current.Spec.RequiresRepublish = original.Spec.RequiresRepublish
			_, err = f.ClientSet.StorageV1().CSIDrivers().Update(ctx, current, metav1.UpdateOptions{})
			return err
		})
	})
}
