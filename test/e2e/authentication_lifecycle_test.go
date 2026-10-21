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
	"os"
	"reflect"
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
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
)

var _ = ginkgo.Describe("Authentication credential renewal on an existing mount", ginkgo.Serial, func() {
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

	for _, scenario := range []authenticationScenario{
		{"G05", "static", "", workloadToken}, {"G06", "inline", "", workloadToken}, {"G07", "static", "", oauthToken},
	} {
		framework.It(fmt.Sprintf("should keep the %s %s mount through unchanged and renewed credentials without replacing its pod [%s] [auth-additional] [file.csi.azure.com]",
			scenario.entryPoint, scenario.auth, scenario.id), framework.WithSlow(), framework.WithDisruptive(), func(ctx ginkgo.SpecContext) {
			requireDisruptiveAuthenticationLane()
			configureAuthenticationRepublish(ctx, f)
			node := selectAuthenticationNode(ctx, f.ClientSet)
			account := createAuthenticationAccount(ctx, nil)
			attributes := map[string]string{
				"resourceGroup":   suiteCreds.ResourceGroup,
				"storageAccount":  account,
				"shareName":       createAuthenticationShare(ctx, account),
				"secretNamespace": ns.Name,
			}
			var tokenSource authenticationTokenSource
			serviceAccount := configureMountIdentity(ctx, f, scenario.auth, node, attributes, account, "", &tokenSource, true)
			_, env, err := azclient.GetAzureCloudConfigAndEnvConfig(&azclient.ARMClientConfig{Cloud: suiteCreds.Cloud, TenantID: suiteCreds.TenantID})
			framework.ExpectNoError(err)
			test := testsuites.PreProvisionedCredentialRenewalTest{
				CSIDriver: testDriver,
				Pod: testsuites.PodDetails{
					Cmd:                authenticationMountCheck(ctx, scenario.auth, account, attributes),
					Volumes:            []testsuites.VolumeDetails{authenticationVolume(scenario.entryPoint, attributes)},
					NodeSelector:       map[string]string{testsuites.HostNameLabel: node.Labels[testsuites.HostNameLabel]},
					ServiceAccountName: serviceAccount,
				},
				CSIInlineVolume: scenario.entryPoint == "inline",
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
						token, err := tokenSource(ctx)
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
		}, ginkgo.SpecTimeout(3*time.Hour))
	}
})

var _ = ginkgo.Describe("Authentication recovery after a node plugin restart", ginkgo.Serial, func() {
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

	for _, scenario := range []authenticationScenario{
		{"G22", "static", "", managedToken}, {"G23", "static", "", workloadToken}, {"G24", "static", "", oauthToken},
	} {
		framework.It(fmt.Sprintf("should reacquire %s credentials and freshly mount after a node plugin restart [%s] [auth-additional] [file.csi.azure.com]",
			scenario.auth, scenario.id), framework.WithDisruptive(), func(ctx ginkgo.SpecContext) {
			requireDisruptiveAuthenticationLane()
			node := selectAuthenticationNode(ctx, cs)
			account := createAuthenticationAccount(ctx, nil)
			attributes := map[string]string{
				"resourceGroup":   suiteCreds.ResourceGroup,
				"storageAccount":  account,
				"shareName":       createAuthenticationShare(ctx, account),
				"secretNamespace": ns.Name,
			}
			serviceAccount := configureMountIdentity(ctx, f, scenario.auth, node, attributes, account, "", nil, true)
			pods := []testsuites.PodDetails{
				{
					Cmd:                authenticationMountCheck(ctx, scenario.auth, account, attributes) + " && echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
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

			_, env, err := azclient.GetAzureCloudConfigAndEnvConfig(&azclient.ARMClientConfig{Cloud: suiteCreds.Cloud, TenantID: suiteCreds.TenantID})
			framework.ExpectNoError(err)
			// The restart waits for the share to unmount, so the next mount authenticates again.
			testsuites.RestartAuthenticationNodePlugin(ctx, f, node.Name,
				fmt.Sprintf("//%s.file.%s/%s", account, env.StorageEndpointSuffix, attributes["shareName"]))
			test.Run(ctx, cs, ns)
		})
	}
})

func requireDisruptiveAuthenticationLane() {
	requireAdditionalAuthenticationLane()
	if os.Getenv("AZUREFILE_AUTH_DISRUPTIVE") != "true" {
		ginkgo.Skip("requires AZUREFILE_AUTH_DISRUPTIVE=true on an isolated CAPZ cluster; modifies CSIDriver renewal settings or restarts a node plugin")
	}
}

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
	configured := current.DeepCopy()
	_, err = f.ClientSet.StorageV1().CSIDrivers().Update(ctx, current, metav1.UpdateOptions{})
	framework.ExpectNoError(err, "the isolated cluster must permit CSIDriver tokenRequests/requiresRepublish updates; the test will not delete/recreate shared driver configuration")
	ginkgo.DeferCleanup(func(ctx context.Context) error {
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := f.ClientSet.StorageV1().CSIDrivers().Get(ctx, original.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(current.Spec.TokenRequests, configured.Spec.TokenRequests) ||
				!reflect.DeepEqual(current.Spec.RequiresRepublish, configured.Spec.RequiresRepublish) {
				return fmt.Errorf("CSIDriver renewal fields changed concurrently; refusing to overwrite another actor's update")
			}
			current.Spec.TokenRequests = original.Spec.TokenRequests
			current.Spec.RequiresRepublish = original.Spec.RequiresRepublish
			_, err = f.ClientSet.StorageV1().CSIDrivers().Update(ctx, current, metav1.UpdateOptions{})
			return err
		})
	})
}
