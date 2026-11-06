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
	"os"

	"github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
)

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
		{"G22", "static", "", managedToken},
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
			serviceAccount := configureMountIdentity(ctx, f, scenario.auth, node, attributes, "", true)
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
		ginkgo.Skip("requires AZUREFILE_AUTH_DISRUPTIVE=true on an isolated CAPZ cluster; restarts a node plugin")
	}
}
