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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"
)

var _ = ginkgo.Describe("Authentication credential renewal on an existing mount", ginkgo.Serial, func() {
	f := framework.NewDefaultFramework("azurefile-auth-renewal")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged
	scenarios := []authenticationScenario{
		{"G05", "static", "", workloadToken}, {"G06", "inline", "", workloadToken}, {"G07", "static", "", oauthToken},
	}
	framework.It("should observe unchanged and renewed credentials without replacing each volume's pod [G05] [G06] [G07] [auth-additional] [file.csi.azure.com]",
		framework.WithSlow(), framework.WithDisruptive(), func(ctx ginkgo.SpecContext) {
			requireDisruptiveAuthenticationLane()
			// One cluster-wide CSIDriver change covers every fixture; per-fixture save/restore would race.
			configureAuthenticationRepublish(ctx, f)
			runAuthenticationScenariosConcurrently(ctx, scenarios, func(ctx ginkgo.SpecContext, _ int, scenario authenticationScenario) {
				var source authenticationTokenSource
				runAuthenticationScenarioWithOptions(ctx, f, scenario, authenticationFixtureOptions{
					tokenSource: &source,
					configureTest: func(test *testsuites.AuthenticationMountTest) {
						test.Name = "auth-" + strings.ToLower(scenario.id)
					},
					run: func(ctx context.Context, test *testsuites.AuthenticationMountTest, f *framework.Framework, namespace *v1.Namespace) {
						test.RunCredentialRenewal(ctx, f, namespace, func(ctx context.Context) error {
							secret, err := f.ClientSet.CoreV1().Secrets(namespace.Name).Get(ctx, test.VolumeAttributes["secretName"], metav1.GetOptions{})
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
								token, err := source(ctx)
								if err != nil || token.Token == "" || token.Token == original || !token.ExpiresOn.After(expiry.Add(5*time.Minute)) {
									return false
								}
								replacement = token.Token
								return true
							}, 20*time.Minute, 15*time.Second).Should(gomega.BeTrue(), "AAD must issue a different storage access token valid beyond the original expiry/grace period; a changed Secret resourceVersion is insufficient")
							secret.Data["oauthtoken"] = []byte(replacement)
							_, err = f.ClientSet.CoreV1().Secrets(namespace.Name).Update(ctx, secret, metav1.UpdateOptions{})
							return err
						})
					},
				})
			})
		}, ginkgo.SpecTimeout(3*time.Hour))
})

var _ = ginkgo.Describe("Authentication recovery after a node plugin restart", ginkgo.Serial, func() {
	f := framework.NewDefaultFramework("azurefile-auth-restart")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged
	scenarios := []authenticationScenario{
		{"G22", "static", "", managedToken}, {"G23", "static", "", workloadToken}, {"G24", "static", "", oauthToken},
	}
	framework.It("should reacquire Kerberos credentials and freshly mount every share after one node plugin restart [G22] [G23] [G24] [auth-additional] [file.csi.azure.com]",
		framework.WithDisruptive(), func(ctx ginkgo.SpecContext) {
			requireDisruptiveAuthenticationLane()
			since := time.Now()
			tests := make([]*testsuites.AuthenticationMountTest, len(scenarios))
			nodes := make([]*v1.Node, len(scenarios))
			runAuthenticationScenariosConcurrently(ctx, scenarios, func(ctx ginkgo.SpecContext, i int, scenario authenticationScenario) {
				runAuthenticationScenarioWithOptions(ctx, f, scenario, authenticationFixtureOptions{
					selectedNode: &nodes[i],
					configureTest: func(test *testsuites.AuthenticationMountTest) {
						test.Name = "auth-" + strings.ToLower(scenario.id)
					},
					run: func(_ context.Context, test *testsuites.AuthenticationMountTest, _ *framework.Framework, _ *v1.Namespace) {
						tests[i] = test
					},
				})
			})
			gomega.Expect(tests).NotTo(gomega.ContainElement(gomega.BeNil()), "every fixture must be prepared before the shared restart")
			node := nodes[0]
			restarted := false
			sources := make([]string, len(tests))
			for i, test := range tests {
				gomega.Expect(nodes[i].Name).To(gomega.Equal(node.Name), "one node plugin restart must precede every fixture's fresh mount")
				sources[i] = fmt.Sprintf("//%s.file.%s/%s", test.AccountName, test.StorageEndpointSuffix, test.ShareName)
				test.AfterMount = func(ctx context.Context, handle string) {
					observeAdditionalMount(ctx, f, test, node.Name, since, handle)
					if restarted {
						testsuites.AuthenticationLogObserved(ctx, f.ClientSet, "node", node.Name, since,
							"volume("+handle+") mount", "succeeded")
					}
				}
			}
			testsuites.RunAuthenticationRemounts(ctx, f.ClientSet, f.Namespace, tests, func(ctx context.Context) {
				since = testsuites.RestartAuthenticationNodePlugin(ctx, f, node.Name, sources...)
				restarted = true
			})
		})
})

// runAuthenticationScenariosConcurrently overlaps independent fixtures inside one
// spec; the first failure cancels the remaining fixtures.
func runAuthenticationScenariosConcurrently(ctx ginkgo.SpecContext, scenarios []authenticationScenario, run func(ginkgo.SpecContext, int, authenticationScenario)) {
	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	group := authenticationGroupContext{SpecContext: ctx, ctx: groupCtx}
	var wg sync.WaitGroup
	for i, scenario := range scenarios {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer ginkgo.GinkgoRecover()
			defer func() {
				if r := recover(); r != nil {
					cancel()
					panic(r)
				}
			}()
			run(group, i, scenario)
		}()
	}
	wg.Wait()
}

// authenticationGroupContext keeps Ginkgo's spec reporting while adding group cancellation.
type authenticationGroupContext struct {
	ginkgo.SpecContext
	ctx context.Context
}

func (c authenticationGroupContext) Deadline() (time.Time, bool) { return c.ctx.Deadline() }
func (c authenticationGroupContext) Done() <-chan struct{}       { return c.ctx.Done() }
func (c authenticationGroupContext) Err() error                  { return c.ctx.Err() }
func (c authenticationGroupContext) Value(key any) any           { return c.ctx.Value(key) }

func requireDisruptiveAuthenticationLane() {
	requireAdditionalAuthenticationLane()
	if os.Getenv("AZUREFILE_AUTH_DISRUPTIVE") != "true" {
		ginkgo.Skip("requires AZUREFILE_AUTH_DISRUPTIVE=true on an isolated CAPZ cluster; modifies CSIDriver renewal settings or restarts a node plugin")
	}
}

func configureAuthenticationRepublish(ctx ginkgo.SpecContext, f *framework.Framework) {
	ds, err := f.ClientSet.AppsV1().DaemonSets("kube-system").Get(ctx, "csi-azurefile-node", metav1.GetOptions{})
	framework.ExpectNoError(err)
	verbose := false
	for _, container := range ds.Spec.Template.Spec.Containers {
		if container.Name != "azurefile" {
			continue
		}
		for _, arg := range container.Args {
			if strings.HasPrefix(arg, "--v=") || strings.HasPrefix(arg, "-v=") {
				_, value, _ := strings.Cut(arg, "=")
				level, err := strconv.Atoi(value)
				verbose = err == nil && level >= 4
			}
		}
	}
	gomega.Expect(verbose).To(gomega.BeTrue(), "renewal evidence requires an isolated lane deployed with driver --v=4 or higher")
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
