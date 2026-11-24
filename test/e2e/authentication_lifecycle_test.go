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

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"
)

// Restart deletes a node plugin pod, so it runs serially and is skipped by
// --label-filter='!Disruptive'.
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
		)
	})
