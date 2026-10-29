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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"
)

var _ = ginkgo.Describe("Authentication account policy and operation credentials", func() {
	f := framework.NewDefaultFramework("azurefile-auth-account")
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
			node := selectAuthenticationNode(ctx, cs)
			account := createAuthenticationAccount(ctx, &scenario.shared)
			scParameters := map[string]string{
				"resourceGroup":   suiteCreds.ResourceGroup,
				"storageAccount":  account,
				"secretNamespace": ns.Name,
				"skuName":         "Premium_LRS",
				"storeAccountKey": "false",
				"useDataPlaneAPI": "oauth",
			}
			serviceAccount := configureMountIdentity(ctx, f, scenario.auth, node, scParameters, account, "", nil, true)
			pods := []testsuites.PodDetails{
				{
					Cmd:                authenticationMountCheck(ctx, scenario.auth, account, scParameters) + " && echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
					Volumes:            []testsuites.VolumeDetails{authenticationVolume("dynamic", nil)},
					NodeSelector:       map[string]string{testsuites.HostNameLabel: node.Labels[testsuites.HostNameLabel]},
					ServiceAccountName: serviceAccount,
				},
			}
			test := testsuites.DynamicallyProvisionedCmdVolumeTest{
				CSIDriver:              testDriver,
				Pods:                   pods,
				StorageClassParameters: scParameters,
			}
			test.Run(ctx, cs, ns)
		})
	}

	// G18-G20 and G25 hand the account key to one CSI operation through a
	// csi.storage.k8s.io/<operation>-secret, which the driver must use instead of
	// looking the key up itself; a wrong key must fail that operation.
	for _, scenario := range []struct {
		id         string
		operations []string
		wrongKey   bool
	}{
		{"G18", []string{"provisioner"}, false},
		{"G18", []string{"provisioner"}, true},
		{"G19", []string{"node-stage"}, false},
		{"G19", []string{"node-stage"}, true},
		{"G20", []string{"provisioner", "node-stage", "controller-expand"}, false},
		{"G25", []string{"provisioner", "node-stage"}, false},
	} {
		title := "should deliver operation-specific key credentials and verify native volume operations"
		if scenario.wrongKey {
			title = "should reject a wrong operation-specific key"
		}
		ginkgo.It(fmt.Sprintf("%s [%s] [auth-additional] [file.csi.azure.com]", title, scenario.id), func(ctx ginkgo.SpecContext) {
			requireAdditionalAuthenticationLane()
			node := selectAuthenticationNode(ctx, cs)
			account := createAuthenticationAccount(ctx, nil)
			key, err := suiteAzureClient.GetAuthenticationAccountKey(ctx, suiteCreds.ResourceGroup, account)
			framework.ExpectNoError(err)
			if scenario.wrongKey {
				wrong := make([]byte, 64)
				_, err = rand.Read(wrong)
				framework.ExpectNoError(err)
				key = base64.StdEncoding.EncodeToString(wrong)
			}
			secret, err := cs.CoreV1().Secrets(ns.Name).Create(ctx, &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "auth-key-"},
				StringData: map[string]string{"azurestorageaccountname": account, "azurestorageaccountkey": key},
			}, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			scParameters := map[string]string{
				"resourceGroup":   suiteCreds.ResourceGroup,
				"storageAccount":  account,
				"skuName":         "Premium_LRS",
				"storeAccountKey": "false",
			}
			if scenario.id != "G19" {
				scParameters["useDataPlaneAPI"] = "true"
			}
			for _, operation := range scenario.operations {
				scParameters["csi.storage.k8s.io/"+operation+"-secret-name"] = secret.Name
				scParameters["csi.storage.k8s.io/"+operation+"-secret-namespace"] = ns.Name
			}
			pods := []testsuites.PodDetails{
				{
					Cmd:          authenticationMountCheck(ctx, nodeAccountKey, account, scParameters) + " && echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
					Volumes:      []testsuites.VolumeDetails{authenticationVolume("dynamic", nil)},
					NodeSelector: map[string]string{testsuites.HostNameLabel: node.Labels[testsuites.HostNameLabel]},
				},
			}
			switch {
			case scenario.wrongKey && scenario.id == "G18":
				test := testsuites.DynamicallyProvisionedProvisioningFailureTest{
					CSIDriver:              testDriver,
					Volumes:                pods[0].Volumes,
					StorageClassParameters: scParameters,
				}
				test.Run(ctx, cs, ns)
			case scenario.wrongKey:
				pods[0].Cmd = "true"
				test := testsuites.DynamicallyProvisionedInvalidMountOptions{
					CSIDriver:              testDriver,
					Pods:                   pods,
					StorageClassParameters: scParameters,
				}
				test.Run(ctx, cs, ns)
			case scenario.id == "G20":
				test := testsuites.DynamicallyProvisionedResizeVolumeTest{
					CSIDriver:              testDriver,
					Pods:                   pods,
					StorageClassParameters: scParameters,
				}
				test.Run(ctx, cs, ns)
			case scenario.id == "G25":
				since := time.Now()
				podWithClonedVolume := testsuites.PodDetails{
					Cmd:          authenticationMountCheck(ctx, nodeAccountKey, account, scParameters) + " && grep 'hello world' /mnt/test-1/data",
					NodeSelector: pods[0].NodeSelector,
				}
				test := testsuites.DynamicallyProvisionedVolumeCloningTest{
					CSIDriver:              testDriver,
					Pod:                    pods[0],
					PodWithClonedVolume:    podWithClonedVolume,
					StorageClassParameters: scParameters,
				}
				test.Run(ctx, cs, ns)
				// The cloned data proves the copy; only the controller log shows the supplied key signed its SAS.
				testsuites.AuthenticationLogObserved(ctx, cs, "controller", "", since, "generate sas token for account("+account+")")
			default:
				test := testsuites.DynamicallyProvisionedCmdVolumeTest{
					CSIDriver:              testDriver,
					Pods:                   pods,
					StorageClassParameters: scParameters,
				}
				test.Run(ctx, cs, ns)
			}
		})
	}

	// Each positive matrix route has a twin whose only change withholds that
	// route's credential, so the positive case cannot pass through another route.
	for _, scenario := range []struct {
		id   string
		twin authenticationScenario
	}{
		{"D01", authenticationScenario{"M01", "static", "", nodeAccountKey}},
		{"D02", authenticationScenario{"N03", "static", "", workloadKey}},
		{"D03", authenticationScenario{"N04", "inline", "", workloadKey}},
		{"D04", authenticationScenario{"N06", "static", "", managedToken}},
		{"D05", authenticationScenario{"N07", "static", "", workloadToken}},
		{"D06", authenticationScenario{"N09", "static", "", oauthToken}},
	} {
		ginkgo.It(fmt.Sprintf("should reject %s %s without its credential, unlike %s [%s] [auth-additional] [file.csi.azure.com]",
			scenario.twin.entryPoint, scenario.twin.auth, scenario.twin.id, scenario.id), func(ctx ginkgo.SpecContext) {
			requireAdditionalAuthenticationLane()
			node := selectAuthenticationNode(ctx, cs)
			// Key routes lose shared-key authorization; identity routes get no storage role.
			var allowSharedKey *bool
			if scenario.twin.auth == nodeAccountKey {
				allowSharedKey = new(bool)
			}
			account := createAuthenticationAccount(ctx, allowSharedKey)
			attributes := map[string]string{
				"resourceGroup":   suiteCreds.ResourceGroup,
				"storageAccount":  account,
				"shareName":       createAuthenticationShare(ctx, account),
				"secretNamespace": ns.Name,
			}
			serviceAccount := configureMountIdentity(ctx, f, scenario.twin.auth, node, attributes, account, "", nil, false)
			pods := []testsuites.PodDetails{
				{
					Cmd:                "true",
					Volumes:            []testsuites.VolumeDetails{authenticationVolume(scenario.twin.entryPoint, attributes)},
					NodeSelector:       map[string]string{testsuites.HostNameLabel: node.Labels[testsuites.HostNameLabel]},
					ServiceAccountName: serviceAccount,
				},
			}
			test := testsuites.PreProvisionedMountFailureTest{
				CSIDriver:       testDriver,
				Pods:            pods,
				CSIInlineVolume: scenario.twin.entryPoint == "inline",
			}
			test.Run(ctx, cs, ns)
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
