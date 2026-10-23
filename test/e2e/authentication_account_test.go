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

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"
)

var _ = ginkgo.Describe("Authentication account policy and operation credentials", func() {
	f := framework.NewDefaultFramework("azurefile-auth-account")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	for _, scenario := range []struct {
		id     string
		auth   mountAuthentication
		shared bool
	}{
		{"G12", managedToken, false}, {"G13", workloadToken, false}, {"G14", oauthToken, false},
		{"G15", managedToken, true}, {"G16", workloadToken, true}, {"G17", oauthToken, true},
	} {
		ginkgo.It(fmt.Sprintf("should explicitly select an OAuth REST account with shared-key access %t and %s [%s] [auth-additional] [file.csi.azure.com]",
			scenario.shared, scenario.auth, scenario.id), func(ctx ginkgo.SpecContext) {
			requireAdditionalAuthenticationLane()
			since := time.Now()
			var node *v1.Node
			runAuthenticationScenarioWithOptions(ctx, f, authenticationScenario{scenario.id, "dynamic", "oauth", scenario.auth}, authenticationFixtureOptions{
				explicitAccount: true, allowSharedKey: &scenario.shared, selectedNode: &node,
				configureTest: func(test *testsuites.AuthenticationMountTest) {
					gomega.Expect(test.VolumeAttributes["storageAccount"]).To(gomega.Equal(test.AccountName))
					gomega.Expect(test.VolumeAttributes).NotTo(gomega.HaveKey("matchTags"))
					test.AfterProvision = func(ctx context.Context, pvc *v1.PersistentVolumeClaim) {
						testsuites.AuthenticationRPCObserved(ctx, f.ClientSet, "controller", "", since,
							map[string]string{"name": "pvc-" + string(pvc.UID)}, false, "capacity_range")
					}
					test.AfterMount = func(ctx context.Context, handle string) {
						observeAdditionalMount(ctx, f, test, node.Name, since, handle)
					}
				},
			})
		})
	}

	for _, id := range []string{"G18", "G19", "G20", "G25"} {
		ginkgo.It(fmt.Sprintf("should deliver operation-specific key credentials and verify native volume operations [%s] [auth-additional] [file.csi.azure.com]", id), func(ctx ginkgo.SpecContext) {
			requireAdditionalAuthenticationLane()
			since := time.Now()
			var node *v1.Node
			controllerAPI := ""
			if id != "G19" {
				controllerAPI = "true"
			}
			options := authenticationFixtureOptions{explicitAccount: true, selectedNode: &node}
			options.configureTest = func(test *testsuites.AuthenticationMountTest) {
				key, err := suiteAzureClient.GetAuthenticationAccountKey(ctx, suiteCreds.ResourceGroup, test.AccountName)
				framework.ExpectNoError(err)
				newSecret := func(name string) string {
					_, err := f.ClientSet.CoreV1().Secrets(f.Namespace.Name).Create(ctx, &v1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: name},
						StringData: map[string]string{"azurestorageaccountname": test.AccountName, "azurestorageaccountkey": key},
					}, metav1.CreateOptions{})
					framework.ExpectNoError(err)
					return name
				}
				if id == "G18" || id == "G20" || id == "G25" {
					test.RequestSecrets.Provisioner = newSecret("auth-provisioner-key")
				}
				if id == "G19" || id == "G20" || id == "G25" {
					test.RequestSecrets.NodeStage = newSecret("auth-node-stage-key")
				}
				if id == "G20" {
					test.RequestSecrets.ControllerExpand = newSecret("auth-expand-key")
				}
				test.AfterProvision = func(ctx context.Context, pvc *v1.PersistentVolumeClaim) {
					testsuites.AuthenticationRPCObserved(ctx, f.ClientSet, "controller", "", since,
						map[string]string{"name": "pvc-" + string(pvc.UID)}, test.RequestSecrets.Provisioner != "", "capacity_range")
				}
				test.AfterMount = func(ctx context.Context, handle string) {
					testsuites.AuthenticationRPCObserved(ctx, f.ClientSet, "node", node.Name, since,
						map[string]string{"volume_id": handle}, test.RequestSecrets.NodeStage != "", "staging_target_path")
					if id == "G18" {
						testsuites.AuthenticationLogObserved(ctx, f.ClientSet, "node", node.Name, since,
							"use cluster identity to get account key from", test.AccountName)
					}
				}
			}
			switch id {
			case "G20":
				options.run = func(ctx context.Context, test *testsuites.AuthenticationMountTest, f *framework.Framework, namespace *v1.Namespace) {
					test.RunExpansion(ctx, f, namespace, func(ctx context.Context, quota int32) error {
						client, err := suiteAzureClient.GetAzureFilesClient()
						if err != nil {
							return err
						}
						share, err := client.Get(ctx, suiteCreds.ResourceGroup, test.AccountName, test.ShareName, nil)
						if err != nil {
							return err
						}
						if share.FileShareProperties == nil || share.FileShareProperties.ShareQuota == nil || *share.FileShareProperties.ShareQuota != quota {
							return fmt.Errorf("share quota does not match requested %d GiB", quota)
						}
						return nil
					})
				}
			case "G25":
				options.run = func(ctx context.Context, test *testsuites.AuthenticationMountTest, f *framework.Framework, namespace *v1.Namespace) {
					test.RunSASClone(ctx, f, namespace)
				}
			}
			runAuthenticationScenarioWithOptions(ctx, f, authenticationScenario{id, "dynamic", controllerAPI, nodeAccountKey}, options)
		})
	}
})

func requireAdditionalAuthenticationLane() {
	skipIfUsingInTreeVolumePlugin()
	skipIfTestingInWindowsCluster()
	skipIfTestingInMigrationCluster()
	if !isCapzTest {
		ginkgo.Skip("additional authentication Azure/cluster fixtures require CAPZ")
	}
}

func observeAdditionalMount(ctx context.Context, f *framework.Framework, test *testsuites.AuthenticationMountTest, node string, since time.Time, handle string) {
	switch {
	case test.VolumeAttributes["mountWithManagedIdentity"] == "true":
		testsuites.AuthenticationLogObserved(ctx, f.ClientSet, "node", node, since,
			"using managed identity "+test.VolumeAttributes["clientID"], "for volume "+handle)
	case test.VolumeAttributes["mountWithWorkloadIdentityToken"] == "true":
		testsuites.AuthenticationLogObserved(ctx, f.ClientSet, "node", node, since,
			"NodePublishVolume: volume("+handle+")", "clientID: "+test.VolumeAttributes["clientID"])
		testsuites.AuthenticationLogObserved(ctx, f.ClientSet, "node", node, since, "using workload identity token for volume "+handle)
	case test.VolumeAttributes["mountWithOAuthToken"] == "true":
		testsuites.AuthenticationLogObserved(ctx, f.ClientSet, "node", node, since,
			"using OAuth token from secret("+f.Namespace.Name+"/"+test.VolumeAttributes["secretName"]+")", "for volume "+handle)
	default:
		testsuites.AuthenticationLogObserved(ctx, f.ClientSet, "node", node, since,
			"clientID("+test.VolumeAttributes["clientID"]+") is specified, use service account token to get account key")
	}
}
