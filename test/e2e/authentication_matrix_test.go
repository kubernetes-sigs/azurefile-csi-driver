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
	"k8s.io/kubernetes/test/e2e/framework"
	admissionapi "k8s.io/pod-security-admission/api"
)

var authenticationMatrixScenarios = []authenticationScenario{
	{"M01", "static", "", nodeAccountKey},
	{"M02", "dynamic", "true", configuredManagedToken},
	{"M03", "dynamic", "oauth", configuredManagedToken},
	{"M04", "static", "", configuredManagedToken},
	{"M05", "dynamic", "true", configuredWorkloadToken},
	{"M06", "dynamic", "oauth", configuredWorkloadToken},
	{"M07", "static", "", configuredWorkloadToken},
	{"M08", "inline", "", configuredWorkloadToken},
	{"M09", "inline", "", nodeAccountKey},
	{"G01", "dynamic", "", workloadToken},
}

var _ = ginkgo.Describe("Authentication matrix completion", func() {
	f := framework.NewDefaultFramework("azurefile-auth-matrix")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	for _, scenario := range authenticationMatrixScenarios {
		ginkgo.It(fmt.Sprintf("should write and read with %s %s and controller API %q [%s] [auth-mount-matrix] [file.csi.azure.com]",
			scenario.entryPoint, scenario.auth, scenario.controllerAPI, scenario.id), func(ctx ginkgo.SpecContext) {
			runAuthenticationScenario(ctx, f, scenario)
		})
	}
})
