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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armstorage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	authv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2enode "k8s.io/kubernetes/test/e2e/framework/node"
	admissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/testsuites"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/azure"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
)

type mountAuthentication string

const (
	nodeAccountKey         mountAuthentication = "node ListKeys"
	workloadKey            mountAuthentication = "workload identity account key"
	managedToken           mountAuthentication = "explicit managed identity Kerberos"
	configuredManagedToken mountAuthentication = "configured managed identity Kerberos"
)

const (
	wiStorageFileDataSMBMIAdmin = "a235d3ee-5935-4cfb-8cc5-a3303ad5995e"
	wiStorageAccountContributor = "17d1049b-9a84-46fb-8f53-869881c3d3ab"
)

// storageRole is the role a dedicated test identity needs for the route.
func (a mountAuthentication) storageRole() string {
	if a == workloadKey {
		return wiStorageAccountContributor
	}
	return wiStorageFileDataSMBMIAdmin
}

type volumeKind string

const (
	dynamicVolume volumeKind = "dynamically provisioned"
	staticVolume  volumeKind = "pre-provisioned"
	inlineVolume  volumeKind = "CSI inline"
)

// authenticationScenario is one path through the driver: how the volume is
// provisioned, which useDataPlaneAPI the controller uses to create a dynamic
// share, and which credential the node mounts with.
type authenticationScenario struct {
	volume       volumeKind
	dataPlaneAPI string
	auth         mountAuthentication
}

func (s authenticationScenario) String() string {
	name := fmt.Sprintf("a %s volume with %s", s.volume, s.auth)
	if s.volume != dynamicVolume {
		return name
	}
	switch s.dataPlaneAPI {
	case "true":
		return name + " after key REST provisioning"
	case "oauth":
		return name + " after OAuth REST provisioning"
	}
	return name + " after ARM provisioning"
}

// hasNegativeTwin reports whether withholding the route's credential is
// expected to fail the mount. Dynamic routes would fail at provisioning, and
// configured identities are shared by the suite.
func (s authenticationScenario) hasNegativeTwin() bool {
	return s.volume != dynamicVolume && s.auth != configuredManagedToken
}

func authenticationEntryDescription(s authenticationScenario) string {
	return "on " + s.String() + " [file.csi.azure.com]"
}

var authenticationScenarios = []authenticationScenario{
	{dynamicVolume, "", nodeAccountKey},
	{dynamicVolume, "", workloadKey},
	{dynamicVolume, "", managedToken},

	{dynamicVolume, "true", nodeAccountKey},
	{dynamicVolume, "true", workloadKey},
	{dynamicVolume, "true", managedToken},
	{dynamicVolume, "true", configuredManagedToken},

	{dynamicVolume, "oauth", nodeAccountKey},
	{dynamicVolume, "oauth", workloadKey},
	{dynamicVolume, "oauth", managedToken},
	{dynamicVolume, "oauth", configuredManagedToken},

	{staticVolume, "", nodeAccountKey},
	{staticVolume, "", workloadKey},
	{staticVolume, "", managedToken},
	{staticVolume, "", configuredManagedToken},

	// The inline node-key route needs the opt-in
	// --allow-inline-volume-key-access-with-identity=true lane.
	{inlineVolume, "", nodeAccountKey},
	{inlineVolume, "", workloadKey},
}

func authenticationEntries(keep func(authenticationScenario) bool) []ginkgo.TableEntry {
	entries := []ginkgo.TableEntry{}
	for _, s := range authenticationScenarios {
		if keep(s) {
			entries = append(entries, ginkgo.Entry(nil, s))
		}
	}
	return entries
}

var _ = ginkgo.Describe("Authentication and mounting", ginkgo.Label("auth"), func() {
	f := framework.NewDefaultFramework("azurefile-auth")
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

	ginkgo.DescribeTable("should write and read",
		func(ctx ginkgo.SpecContext, scenario authenticationScenario) {
			requireAuthenticationLane(ctx, cs, scenario)
			node := selectAuthenticationNode(ctx, cs)
			account := createAuthenticationAccount(ctx, nil)
			attributes := authenticationAttributes(account, ns.Name)
			if scenario.volume != dynamicVolume {
				attributes["shareName"] = createAuthenticationShare(ctx, account)
			}
			mount := newAuthenticationMount(ctx, f, scenario.auth, node, account, attributes, resourceGroupRole)
			if scenario.volume == dynamicVolume {
				// Keep the normal account lookup while matching only this spec's account.
				delete(attributes, "storageAccount")
				attributes["matchTags"] = "true"
				attributes["tags"] = "csi-e2e-auth=" + account
				// Matches only our accounts; see CreateSMBOAuthAccountWithSharedKeyPolicy.
				attributes["requireInfraEncryption"] = "true"
				attributes["skuName"] = "Premium_LRS"
				attributes["storeAccountKey"] = "false"
				if scenario.dataPlaneAPI != "" {
					attributes["useDataPlaneAPI"] = scenario.dataPlaneAPI
				}
			}
			pods := []testsuites.PodDetails{mount.pod(ctx, scenario.volume)}

			switch scenario.volume {
			case dynamicVolume:
				test := testsuites.DynamicallyProvisionedCmdVolumeTest{
					CSIDriver:              testDriver,
					Pods:                   pods,
					StorageClassParameters: attributes,
				}
				test.Run(ctx, cs, ns)
			case staticVolume:
				test := testsuites.PreProvisionedMultiplePods{
					CSIDriver: testDriver,
					Pods:      pods,
				}
				test.Run(ctx, cs, ns)
			case inlineVolume:
				test := testsuites.DynamicallyProvisionedInlineVolumeTest{
					CSIDriver:       testDriver,
					Pods:            pods,
					CSIInlineVolume: true,
				}
				test.Run(ctx, cs, ns)
			}
		},
		authenticationEntryDescription,
		authenticationEntries(func(authenticationScenario) bool { return true }),
	)

	// Each twin changes only the route's credential: key routes lose shared-key
	// authorization and identity routes get no storage role, so the positive
	// case cannot pass through another route.
	ginkgo.DescribeTable("should fail to mount without the route's credential",
		func(ctx ginkgo.SpecContext, scenario authenticationScenario) {
			requireAuthenticationLane(ctx, cs, scenario)
			node := selectAuthenticationNode(ctx, cs)
			var allowSharedKey *bool
			if scenario.auth == nodeAccountKey {
				allowSharedKey = new(bool)
			}
			account := createAuthenticationAccount(ctx, allowSharedKey)
			attributes := authenticationAttributes(account, ns.Name)
			attributes["shareName"] = createAuthenticationShare(ctx, account)
			mount := newAuthenticationMount(ctx, f, scenario.auth, node, account, attributes, noRole)
			pod := mount.pod(ctx, scenario.volume)
			pod.Cmd = "true"
			test := testsuites.PreProvisionedMountFailureTest{
				CSIDriver:       testDriver,
				Pods:            []testsuites.PodDetails{pod},
				CSIInlineVolume: scenario.volume == inlineVolume,
			}
			test.Run(ctx, cs, ns)
		},
		authenticationEntryDescription,
		authenticationEntries(authenticationScenario.hasNegativeTwin),
	)

	ginkgo.DescribeTable("should mount from an explicitly selected OAuth REST account",
		func(ctx ginkgo.SpecContext, auth mountAuthentication, allowSharedKey bool) {
			requireAuthenticationFixtures()
			node := selectAuthenticationNode(ctx, cs)
			account := createAuthenticationAccount(ctx, &allowSharedKey)
			scParameters := authenticationAttributes(account, ns.Name)
			scParameters["skuName"] = "Premium_LRS"
			scParameters["storeAccountKey"] = "false"
			scParameters["useDataPlaneAPI"] = "oauth"
			mount := newAuthenticationMount(ctx, f, auth, node, account, scParameters, resourceGroupRole)
			test := testsuites.DynamicallyProvisionedCmdVolumeTest{
				CSIDriver:              testDriver,
				Pods:                   []testsuites.PodDetails{mount.pod(ctx, dynamicVolume)},
				StorageClassParameters: scParameters,
			}
			test.Run(ctx, cs, ns)
		},
		func(auth mountAuthentication, allowSharedKey bool) string {
			return fmt.Sprintf("with %s and shared-key access %t [file.csi.azure.com]", auth, allowSharedKey)
		},
		ginkgo.Entry(nil, managedToken, false),
		ginkgo.Entry(nil, managedToken, true),
	)

	// The operation-key specs hand the account key to one CSI operation through
	// a csi.storage.k8s.io/<operation>-secret, which the driver must use instead
	// of looking the key up itself.
	ginkgo.It("should provision with a provisioner-secret key [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		requireAuthenticationFixtures()
		mount := operationKeyMount(ctx, cs, ns, false, "provisioner")
		mount.attributes["useDataPlaneAPI"] = "true"
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   []testsuites.PodDetails{mount.pod(ctx, dynamicVolume)},
			StorageClassParameters: mount.attributes,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should fail to provision with a wrong provisioner-secret key [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		requireAuthenticationFixtures()
		mount := operationKeyMount(ctx, cs, ns, true, "provisioner")
		mount.attributes["useDataPlaneAPI"] = "true"
		test := testsuites.DynamicallyProvisionedProvisioningFailureTest{
			CSIDriver:              testDriver,
			Volumes:                mount.pod(ctx, dynamicVolume).Volumes,
			StorageClassParameters: mount.attributes,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should stage with a node-stage-secret key [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		requireAuthenticationFixtures()
		mount := operationKeyMount(ctx, cs, ns, false, "node-stage")
		test := testsuites.DynamicallyProvisionedCmdVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   []testsuites.PodDetails{mount.pod(ctx, dynamicVolume)},
			StorageClassParameters: mount.attributes,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should fail to stage with a wrong node-stage-secret key [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		requireAuthenticationFixtures()
		mount := operationKeyMount(ctx, cs, ns, true, "node-stage")
		pod := mount.pod(ctx, dynamicVolume)
		pod.Cmd = "true"
		test := testsuites.DynamicallyProvisionedInvalidMountOptions{
			CSIDriver:              testDriver,
			Pods:                   []testsuites.PodDetails{pod},
			StorageClassParameters: mount.attributes,
			ExpectFailureToPersist: true,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should expand with provisioner, node-stage and controller-expand secret keys [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		requireAuthenticationFixtures()
		mount := operationKeyMount(ctx, cs, ns, false, "provisioner", "node-stage", "controller-expand")
		mount.attributes["useDataPlaneAPI"] = "true"
		test := testsuites.DynamicallyProvisionedResizeVolumeTest{
			CSIDriver:              testDriver,
			Pods:                   []testsuites.PodDetails{mount.pod(ctx, dynamicVolume)},
			StorageClassParameters: mount.attributes,
		}
		test.Run(ctx, cs, ns)
	})

	ginkgo.It("should clone with provisioner and node-stage secret keys [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		requireAuthenticationFixtures()
		mount := operationKeyMount(ctx, cs, ns, false, "provisioner", "node-stage")
		mount.attributes["useDataPlaneAPI"] = "true"
		pod := mount.pod(ctx, dynamicVolume)
		since := time.Now()
		test := testsuites.DynamicallyProvisionedVolumeCloningTest{
			CSIDriver: testDriver,
			Pod:       pod,
			PodWithClonedVolume: testsuites.PodDetails{
				Cmd:          mount.mountCheck(ctx) + " && grep 'hello world' /mnt/test-1/data",
				NodeSelector: pod.NodeSelector,
			},
			StorageClassParameters: mount.attributes,
		}
		test.Run(ctx, cs, ns)
		// The cloned data proves the copy; only the controller log shows the supplied key signed its SAS.
		testsuites.AuthenticationLogObserved(ctx, cs, "controller", "", since, "generate sas token for account("+mount.account+")")
	})

	ginkgo.It("should keep two workload identity account key principals mounted on one account and node and reject one without a role [file.csi.azure.com]", func(ctx ginkgo.SpecContext) {
		requireAuthenticationFixtures()
		node := selectAuthenticationNode(ctx, cs)
		account := createAuthenticationAccount(ctx, nil)
		principal := func(grant roleScope) testsuites.PodDetails {
			attributes := authenticationAttributes(account, ns.Name)
			attributes["shareName"] = createAuthenticationShare(ctx, account)
			return newAuthenticationMount(ctx, f, workloadKey, node, account, attributes, grant).pod(ctx, staticVolume)
		}
		first, second, denied := principal(accountRole), principal(accountRole), principal(noRole)
		denied.Cmd = "true"
		test := testsuites.PreProvisionedConcurrentMountsTest{
			CSIDriver:  testDriver,
			Pods:       []testsuites.PodDetails{first, second},
			DeniedPods: []testsuites.PodDetails{denied},
		}
		test.Run(ctx, f, ns)
	})
})

// requireAuthenticationFixtures skips lanes that cannot create the Azure
// identities and accounts the authentication specs use.
func requireAuthenticationFixtures() {
	skipIfUsingInTreeVolumePlugin()
	skipIfTestingInWindowsCluster()
	skipIfTestingInMigrationCluster()
	if !isCapzTest {
		ginkgo.Skip("authentication Azure fixtures require CAPZ")
	}
}

// requireAuthenticationLane skips routes the cluster cannot run and checks the
// inline key-access guard the inline key routes depend on.
func requireAuthenticationLane(ctx context.Context, cs clientset.Interface, scenario authenticationScenario) {
	skipIfUsingInTreeVolumePlugin()
	skipIfTestingInWindowsCluster()
	skipIfTestingInMigrationCluster()
	if scenario.auth != nodeAccountKey && !isCapzTest {
		ginkgo.Skip("identity authentication fixtures require CAPZ")
	}
	if scenario.volume != inlineVolume || (scenario.auth != nodeAccountKey && scenario.auth != workloadKey) {
		return
	}
	ginkgo.By("checking the separately configured inline identity key-access lane")
	ds, err := cs.AppsV1().DaemonSets("kube-system").Get(ctx, "csi-azurefile-node", metav1.GetOptions{})
	framework.ExpectNoError(err)
	enabled := false
	for _, container := range ds.Spec.Template.Spec.Containers {
		if container.Name != "azurefile" {
			continue
		}
		for _, arg := range container.Args {
			switch arg {
			case "--allow-inline-volume-key-access-with-identity", "--allow-inline-volume-key-access-with-identity=true":
				enabled = true
			case "--allow-inline-volume-key-access-with-identity=false":
				enabled = false
			}
		}
	}
	if scenario.auth == nodeAccountKey && !enabled {
		ginkgo.Skip("requires the opt-in --allow-inline-volume-key-access-with-identity=true lane; the test does not change driver flags")
	}
	if scenario.auth == workloadKey && enabled {
		ginkgo.Skip("the inline WI-key regression needs the default inline key-access guard; this lane disables it")
	}
}

// selectAuthenticationNode pins a spec to one Linux worker so node identities
// and node-side checks apply to the node that mounts.
func selectAuthenticationNode(ctx context.Context, cs clientset.Interface) *v1.Node {
	nodes, err := e2enode.GetReadySchedulableNodes(ctx, cs)
	framework.ExpectNoError(err)
	for i := range nodes.Items {
		node := &nodes.Items[i]
		_, controlPlane := node.Labels["node-role.kubernetes.io/control-plane"]
		_, master := node.Labels["node-role.kubernetes.io/master"]
		if !controlPlane && !master && node.Labels[v1.LabelOSStable] == "linux" {
			return node
		}
	}
	framework.Failf("a Linux worker is required")
	return nil
}

// createAuthenticationAccount creates a never-mounted SMB OAuth account, so
// node key and Kerberos caches cannot hide the route under test.
func createAuthenticationAccount(ctx context.Context, allowSharedKey *bool) string {
	gomega.Expect(suiteCreds).NotTo(gomega.BeNil())
	gomega.Expect(suiteAzureClient).NotTo(gomega.BeNil())
	ginkgo.By("creating an isolated storage account")
	account := "afauth" + strings.ReplaceAll(uuid.NewString(), "-", "")[:18]
	// Registered first: a create that fails after Azure accepted it still leaves an account.
	ginkgo.DeferCleanup(func(ctx context.Context) error {
		return suiteAzureClient.DeleteStorageAccount(ctx, suiteCreds.ResourceGroup, account)
	})
	framework.ExpectNoError(suiteAzureClient.CreateSMBOAuthAccountWithSharedKeyPolicy(ctx, suiteCreds.ResourceGroup, account, suiteCreds.Location,
		map[string]*string{"csi-e2e-auth": to.Ptr(account), "k8s-azure-created-by": to.Ptr("azure")}, allowSharedKey))
	if allowSharedKey != nil {
		properties, err := suiteAzureClient.GetStorageAccount(ctx, suiteCreds.ResourceGroup, account)
		framework.ExpectNoError(err)
		gomega.Expect(properties.Properties).NotTo(gomega.BeNil())
		gomega.Expect(properties.Properties.AllowSharedKeyAccess).To(gomega.Equal(allowSharedKey), "verify the actual Azure account policy")
	}
	return account
}

func createAuthenticationShare(ctx context.Context, account string) string {
	ginkgo.By("creating the pre-provisioned file share")
	share := "auth-" + uuid.NewString()
	client, err := suiteAzureClient.GetAzureFilesClient()
	framework.ExpectNoError(err)
	_, err = client.Create(ctx, suiteCreds.ResourceGroup, account, share, armstorage.FileShare{
		FileShareProperties: &armstorage.FileShareProperties{ShareQuota: to.Ptr(int32(100))},
	}, nil)
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(func(ctx context.Context) error {
		return client.Delete(ctx, suiteCreds.ResourceGroup, account, share, nil)
	})
	return share
}

func authenticationAttributes(account, namespace string) map[string]string {
	return map[string]string{
		"resourceGroup":   suiteCreds.ResourceGroup,
		"storageAccount":  account,
		"secretNamespace": namespace,
	}
}

// authenticationVolume returns the volume every authentication pod mounts at /mnt/test-1.
// nosharesock gives each mount its own SMB session, so it authenticates on its own.
func authenticationVolume(volume volumeKind, attributes map[string]string) testsuites.VolumeDetails {
	details := testsuites.VolumeDetails{
		ClaimSize:    "100Gi",
		MountOptions: []string{"nosharesock"},
		VolumeMount: testsuites.VolumeMountDetails{
			NameGenerate:      "test-volume-",
			MountPathGenerate: "/mnt/test-",
		},
	}
	switch volume {
	case staticVolume:
		// An opaque handle makes the driver resolve the account from the attributes.
		details.VolumeID = "auth-" + uuid.NewString()
		details.VolumeAttributes = attributes
	case inlineVolume:
		details.VolumeAttributes = maps.Clone(attributes)
		details.VolumeAttributes["mountOptions"] = "nosharesock"
	}
	return details
}

// Some kernels omit the default NTLMSSP option; the username must still match.
const authenticationMountScript = `
index($1, prefix) == 1 && $2 == "/mnt/test-1" && $3 == "cifs" {
	mounted = 1
	n = split($4, opts, ",")
	for (i = 1; i <= n; i++) {
		if (opts[i] ~ /^sec=/) specified = 1
		if (opts[i] == security || opts[i] == security "i") found = 1
		if (opts[i] == username) identity = 1
	}
}
END {
	exit !(mounted && (found || (security == "sec=ntlmssp" && !specified)) &&
		(username == "" || identity))
}`

func suiteCloud() (cloud.Configuration, *azclient.Environment) {
	cloudConfig, env, err := azclient.GetAzureCloudConfigAndEnvConfig(&azclient.ARMClientConfig{Cloud: suiteCreds.Cloud, TenantID: suiteCreds.TenantID})
	framework.ExpectNoError(err)
	return cloudConfig, env
}

// authenticationMountCheck returns a pod command that fails unless /mnt/test-1 is
// a CIFS mount of the account that authenticated with the route's security mode
// and, where the kernel records it, the route's identity.
func authenticationMountCheck(ctx context.Context, auth mountAuthentication, account string, attributes map[string]string) string {
	_, env := suiteCloud()
	security, username := "krb5", ""
	switch auth {
	case nodeAccountKey, workloadKey:
		security, username = "ntlmssp", "username="+account
	case managedToken:
		username = "username=" + attributes["clientID"]
	case configuredManagedToken:
		gomega.Expect(nodeIdentityClientID).NotTo(gomega.BeEmpty(), "suite setup must record the node identity")
		username = "username=" + nodeIdentityClientID
	}
	return fmt.Sprintf("awk -v prefix='//%s.file.%s/' -v security='sec=%s' -v username='%s' '%s' /proc/mounts",
		account, env.StorageEndpointSuffix, security, username, authenticationMountScript)
}

// authenticationMount is a volume's attributes configured for one route, with
// the node and ServiceAccount the mounting pod must use.
type authenticationMount struct {
	auth           mountAuthentication
	account        string
	attributes     map[string]string
	node           *v1.Node
	serviceAccount string
}

func (m *authenticationMount) mountCheck(ctx context.Context) string {
	return authenticationMountCheck(ctx, m.auth, m.account, m.attributes)
}

func (m *authenticationMount) pod(ctx context.Context, volume volumeKind) testsuites.PodDetails {
	return testsuites.PodDetails{
		Cmd:                m.mountCheck(ctx) + " && echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
		Volumes:            []testsuites.VolumeDetails{authenticationVolume(volume, m.attributes)},
		NodeSelector:       map[string]string{testsuites.HostNameLabel: m.node.Labels[testsuites.HostNameLabel]},
		ServiceAccountName: m.serviceAccount,
	}
}

// newAuthenticationMount configures attributes for the route. Explicit routes get
// a dedicated identity whose storage role is granted at the given scope.
func newAuthenticationMount(ctx context.Context, f *framework.Framework, auth mountAuthentication, node *v1.Node, account string, attributes map[string]string, grant roleScope) *authenticationMount {
	m := &authenticationMount{auth: auth, account: account, attributes: attributes, node: node}
	switch auth {
	case configuredManagedToken:
		gomega.Expect(miRoleSetupSucceeded).To(gomega.BeTrue(), "configured node identity role setup must succeed")
		attributes["mountWithManagedIdentity"] = "true"
	case managedToken:
		identity := createTestIdentity(ctx, auth.storageRole(), grant, account)
		identity.attachTo(ctx, node)
		attributes["mountWithManagedIdentity"] = "true"
		attributes["clientID"] = identity.ClientID
	case workloadKey:
		identity := createTestIdentity(ctx, auth.storageRole(), grant, account)
		_, env := suiteCloud()
		m.serviceAccount = federateServiceAccount(ctx, f, identity, strings.TrimRight(env.ResourceManagerEndpoint, "/")+"/.default")
		attributes["clientID"] = identity.ClientID
	}
	return m
}

// roleScope is where a test identity's storage role is assigned.
type roleScope int

const (
	noRole roleScope = iota
	resourceGroupRole
	// accountRole follows the static-volume guidance to grant only the target account.
	accountRole
)

// testIdentity is a user-assigned identity owned by one spec.
type testIdentity struct {
	*azure.NodeIdentityInfo
	name       string
	attachedTo string
}

// createTestIdentity creates an identity distinct from the controller and
// default node identities and assigns it role at grant. Cleanup detaches the
// identity before deleting it.
func createTestIdentity(ctx context.Context, role string, grant roleScope, account string) *testIdentity {
	ginkgo.By("creating a dedicated identity distinct from the controller and default node identities")
	identity := &testIdentity{name: "azurefile-auth-" + uuid.NewString()}
	info, err := suiteAzureClient.EnsureUserAssignedIdentity(ctx, suiteCreds.ResourceGroup, identity.name, suiteCreds.Location)
	framework.ExpectNoError(err)
	identity.NodeIdentityInfo = info
	ginkgo.DeferCleanup(func(ctx context.Context) error {
		if identity.attachedTo != "" {
			// A dangling VMSS identity reference would fail later identity updates.
			if err := suiteAzureClient.SetNodeUserAssignedIdentity(ctx, identity.attachedTo, identity.ResourceID, false); err != nil {
				return fmt.Errorf("retaining identity %s because detaching it from %s failed: %w", identity.name, identity.attachedTo, err)
			}
		}
		return suiteAzureClient.DeleteUserAssignedIdentity(ctx, suiteCreds.ResourceGroup, identity.name)
	})
	gomega.Expect(identity.ClientID).NotTo(gomega.Equal(suiteCreds.AADClientID), "the test identity must differ from the controller credential")
	gomega.Expect(identity.ClientID).NotTo(gomega.Equal(nodeIdentityClientID), "the test identity must differ from the default node identity")

	scope := suiteAzureClient.ResourceGroupScope(suiteCreds.ResourceGroup)
	switch grant {
	case noRole:
		ginkgo.By("withholding the storage role for a negative control")
		return identity
	case accountRole:
		scope = suiteAzureClient.StorageAccountScope(suiteCreds.ResourceGroup, account)
	}
	ginkgo.By("assigning the authentication-specific storage role at " + scope)
	framework.ExpectNoError(suiteAzureClient.AssignRoleAtScope(ctx, scope, identity.PrincipalID, role))
	ginkgo.DeferCleanup(func(ctx context.Context) error {
		return suiteAzureClient.RemoveRoleAtScope(ctx, scope, identity.PrincipalID, role)
	})
	return identity
}

func (i *testIdentity) attachTo(ctx context.Context, node *v1.Node) {
	ginkgo.By("attaching the dedicated managed identity to the selected worker")
	i.attachedTo = node.Spec.ProviderID
	framework.ExpectNoError(suiteAzureClient.SetNodeUserAssignedIdentity(ctx, node.Spec.ProviderID, i.ResourceID, true))
}

// federateServiceAccount creates a namespace-local ServiceAccount federated with
// the identity and waits until its token exchanges for scope.
func federateServiceAccount(ctx context.Context, f *framework.Framework, identity *testIdentity, scope string) string {
	ginkgo.By("preparing the suite OIDC issuer")
	framework.ExpectNoError(prepareAuthenticationOIDC(ctx, f.ClientSet, suiteCreds))
	issuer, err := discoverOIDCIssuer(ctx, f.ClientSet)
	framework.ExpectNoError(err)
	ginkgo.By("creating a namespace-local service account and federated identity credential")
	serviceAccount := "auth-" + uuid.NewString()
	_, err = f.ClientSet.CoreV1().ServiceAccounts(f.Namespace.Name).Create(ctx, &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: serviceAccount,
			Annotations: map[string]string{
				"azure.workload.identity/client-id": identity.ClientID,
				"azure.workload.identity/tenant-id": suiteCreds.TenantID,
			},
		},
	}, metav1.CreateOptions{})
	framework.ExpectNoError(err)
	subject := fmt.Sprintf("system:serviceaccount:%s:%s", f.Namespace.Name, serviceAccount)
	framework.ExpectNoError(suiteAzureClient.CreateFederatedIdentityCredential(ctx, suiteCreds.ResourceGroup, identity.name, "auth", issuer, subject))

	cloudConfig, _ := suiteCloud()
	assertion := func(ctx context.Context) (string, error) {
		request, err := f.ClientSet.CoreV1().ServiceAccounts(f.Namespace.Name).CreateToken(ctx, serviceAccount, &authv1.TokenRequest{
			Spec: authv1.TokenRequestSpec{Audiences: []string{"api://AzureADTokenExchange"}, ExpirationSeconds: to.Ptr(int64(3600))},
		}, metav1.CreateOptions{})
		if err != nil {
			return "", err
		}
		if request.Status.Token == "" {
			return "", fmt.Errorf("service account %s/%s returned an empty token", f.Namespace.Name, serviceAccount)
		}
		return request.Status.Token, nil
	}
	ginkgo.By("waiting for the new federated identity to exchange its service account token")
	gomega.Eventually(ctx, func(ctx context.Context) error {
		// A new credential instance avoids the SDK access-token cache.
		credential, err := azidentity.NewClientAssertionCredential(suiteCreds.TenantID, identity.ClientID, assertion,
			&azidentity.ClientAssertionCredentialOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
		if err != nil {
			return err
		}
		_, err = credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
		return err
	}, 10*time.Minute, 10*time.Second).Should(gomega.Succeed(), "new federated identity must exchange its service account token")
	return serviceAccount
}

// operationKeyMount creates an account and a Secret holding its key, or a wrong
// key, referenced as each operation's csi.storage.k8s.io/<operation>-secret.
// Its attributes are StorageClass parameters.
func operationKeyMount(ctx context.Context, cs clientset.Interface, ns *v1.Namespace, wrongKey bool, operations ...string) *authenticationMount {
	node := selectAuthenticationNode(ctx, cs)
	account := createAuthenticationAccount(ctx, nil)
	key, err := suiteAzureClient.GetAuthenticationAccountKey(ctx, suiteCreds.ResourceGroup, account)
	framework.ExpectNoError(err)
	if wrongKey {
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
	for _, operation := range operations {
		scParameters["csi.storage.k8s.io/"+operation+"-secret-name"] = secret.Name
		scParameters["csi.storage.k8s.io/"+operation+"-secret-namespace"] = ns.Name
	}
	return &authenticationMount{auth: nodeAccountKey, account: account, attributes: scParameters, node: node}
}
