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
	"maps"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
)

type mountAuthentication string

const (
	nodeAccountKey          mountAuthentication = "node ListKeys"
	workloadKey             mountAuthentication = "workload identity account key"
	managedToken            mountAuthentication = "explicit managed identity Kerberos"
	workloadToken           mountAuthentication = "explicit workload identity Kerberos"
	oauthToken              mountAuthentication = "OAuth-token Secret Kerberos"
	configuredManagedToken  mountAuthentication = "configured managed identity Kerberos"
	configuredWorkloadToken mountAuthentication = "configured workload identity Kerberos"
)

type authenticationScenario struct {
	id            string
	entryPoint    string
	controllerAPI string
	auth          mountAuthentication
}

var authenticationScenarios = []authenticationScenario{
	{"N01", "dynamic", "", nodeAccountKey},
	{"N02", "dynamic", "", workloadKey},
	{"N03", "static", "", workloadKey},
	{"N04", "inline", "", workloadKey},
	{"N05", "dynamic", "", managedToken},
	{"N06", "static", "", managedToken},
	{"N07", "static", "", workloadToken},
	{"N08", "inline", "", workloadToken},
	{"N09", "static", "", oauthToken},
	{"N10", "dynamic", "true", nodeAccountKey},
	{"N11", "dynamic", "true", workloadKey},
	{"N12", "dynamic", "true", managedToken},
	{"N13", "dynamic", "true", workloadToken},
	{"N14", "dynamic", "true", oauthToken},
	{"N15", "dynamic", "oauth", nodeAccountKey},
	{"N16", "dynamic", "oauth", workloadKey},
	{"N17", "dynamic", "oauth", managedToken},
	{"N18", "dynamic", "oauth", workloadToken},
	{"N19", "dynamic", "oauth", oauthToken},
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

var _ = ginkgo.Describe("Authentication and mounting", func() {
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

	for _, scenario := range authenticationScenarios {
		provisioning := "an existing share"
		if scenario.entryPoint == "dynamic" {
			switch scenario.controllerAPI {
			case "":
				provisioning = "ARM provisioning"
			case "true":
				provisioning = "key REST provisioning"
			case "oauth":
				provisioning = "OAuth REST provisioning"
			}
		}
		ginkgo.It(fmt.Sprintf("should write and read with %s %s using %s [%s] [auth-mount-matrix] [file.csi.azure.com]",
			scenario.entryPoint, scenario.auth, provisioning, scenario.id), func(ctx ginkgo.SpecContext) {
			requireAuthenticationLane(ctx, cs, scenario)
			namespace := ns
			if scenario.auth == configuredWorkloadToken {
				// The suite's configured-identity FIC is bound to this ServiceAccount.
				namespace = &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: wiServiceAccountNamespace}}
			}
			node := selectAuthenticationNode(ctx, cs)
			account := createAuthenticationAccount(ctx, nil)
			attributes := map[string]string{
				"resourceGroup":   suiteCreds.ResourceGroup,
				"storageAccount":  account,
				"secretNamespace": namespace.Name,
			}
			if scenario.entryPoint != "dynamic" {
				attributes["shareName"] = createAuthenticationShare(ctx, account)
			}
			serviceAccount := configureMountIdentity(ctx, f, scenario.auth, node, attributes, account, "", nil, true)
			if scenario.entryPoint == "dynamic" {
				// Keep the normal account lookup while matching only this spec's account.
				delete(attributes, "storageAccount")
				attributes["matchTags"] = "true"
				attributes["tags"] = "csi-e2e-auth=" + account
				attributes["skuName"] = "Premium_LRS"
				attributes["storeAccountKey"] = "false"
				if scenario.controllerAPI != "" {
					attributes["useDataPlaneAPI"] = scenario.controllerAPI
				}
			}

			pods := []testsuites.PodDetails{
				{
					Cmd:                authenticationMountCheck(ctx, scenario.auth, account, attributes) + " && echo 'hello world' > /mnt/test-1/data && grep 'hello world' /mnt/test-1/data",
					Volumes:            []testsuites.VolumeDetails{authenticationVolume(scenario.entryPoint, attributes)},
					NodeSelector:       map[string]string{testsuites.HostNameLabel: node.Labels[testsuites.HostNameLabel]},
					ServiceAccountName: serviceAccount,
				},
			}
			switch scenario.entryPoint {
			case "dynamic":
				test := testsuites.DynamicallyProvisionedCmdVolumeTest{
					CSIDriver:              testDriver,
					Pods:                   pods,
					StorageClassParameters: attributes,
				}
				test.Run(ctx, cs, namespace)
			case "static":
				test := testsuites.PreProvisionedMultiplePods{
					CSIDriver: testDriver,
					Pods:      pods,
				}
				test.Run(ctx, cs, namespace)
			case "inline":
				test := testsuites.DynamicallyProvisionedInlineVolumeTest{
					CSIDriver:       testDriver,
					Pods:            pods,
					CSIInlineVolume: true,
				}
				test.Run(ctx, cs, namespace)
			}
		})
	}
})

// requireAuthenticationLane skips routes the cluster cannot run and checks the
// inline key-access guard the inline key routes depend on.
func requireAuthenticationLane(ctx context.Context, cs clientset.Interface, scenario authenticationScenario) {
	skipIfUsingInTreeVolumePlugin()
	skipIfTestingInWindowsCluster()
	skipIfTestingInMigrationCluster()
	if scenario.auth != nodeAccountKey && !isCapzTest {
		ginkgo.Skip("identity authentication fixtures require CAPZ")
	}
	if scenario.entryPoint != "inline" || (scenario.auth != nodeAccountKey && scenario.auth != workloadKey) {
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
	if scenario.auth == workloadKey {
		gomega.Expect(enabled).To(gomega.BeFalse(), "the inline WI-key regression must run with the default inline key-access guard enabled")
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
	framework.ExpectNoError(suiteAzureClient.CreateSMBOAuthAccountWithSharedKeyPolicy(ctx, suiteCreds.ResourceGroup, account, suiteCreds.Location,
		map[string]*string{"csi-e2e-auth": to.Ptr(account), "k8s-azure-created-by": to.Ptr("azure")}, allowSharedKey))
	ginkgo.DeferCleanup(func(ctx context.Context) error {
		return suiteAzureClient.DeleteStorageAccount(ctx, suiteCreds.ResourceGroup, account)
	})
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

// authenticationVolume returns the volume every authentication pod mounts at /mnt/test-1.
// nosharesock gives each mount its own SMB session, so it authenticates on its own.
func authenticationVolume(entryPoint string, attributes map[string]string) testsuites.VolumeDetails {
	volume := testsuites.VolumeDetails{
		ClaimSize:    "100Gi",
		MountOptions: []string{"nosharesock"},
		VolumeMount: testsuites.VolumeMountDetails{
			NameGenerate:      "test-volume-",
			MountPathGenerate: "/mnt/test-",
		},
	}
	switch entryPoint {
	case "static":
		// An opaque handle makes the driver resolve the account from the attributes.
		volume.VolumeID = "auth-" + uuid.NewString()
		volume.VolumeAttributes = attributes
	case "inline":
		volume.VolumeAttributes = maps.Clone(attributes)
		volume.VolumeAttributes["mountOptions"] = "nosharesock"
	}
	return volume
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

// authenticationMountCheck returns a pod command that fails unless /mnt/test-1 is
// a CIFS mount of the account that authenticated with the route's security mode
// and, where the kernel records it, the route's identity.
func authenticationMountCheck(ctx context.Context, auth mountAuthentication, account string, attributes map[string]string) string {
	_, env, err := azclient.GetAzureCloudConfigAndEnvConfig(&azclient.ARMClientConfig{Cloud: suiteCreds.Cloud, TenantID: suiteCreds.TenantID})
	framework.ExpectNoError(err)
	security, username := "krb5", ""
	switch auth {
	case nodeAccountKey, workloadKey:
		security, username = "ntlmssp", "username="+account
	case managedToken:
		username = "username=" + attributes["clientID"]
	case configuredManagedToken:
		identity, err := suiteAzureClient.GetNodeIdentityInfo(ctx, suiteCreds.ResourceGroup)
		framework.ExpectNoError(err)
		username = "username=" + identity.ClientID
	}
	return fmt.Sprintf("awk -v prefix='//%s.file.%s/' -v security='sec=%s' -v username='%s' '%s' /proc/mounts",
		account, env.StorageEndpointSuffix, security, username, authenticationMountScript)
}

type authenticationTokenSource func(context.Context) (azcore.AccessToken, error)

func configureMountIdentity(ctx ginkgo.SpecContext, f *framework.Framework, auth mountAuthentication, node *v1.Node, attributes map[string]string, account, roleScope string, tokenSource *authenticationTokenSource, grantRole bool) string {
	if auth == nodeAccountKey {
		return ""
	}
	if auth == configuredManagedToken {
		gomega.Expect(miRoleSetupSucceeded).To(gomega.BeTrue(), "configured node identity role setup must succeed")
		attributes["mountWithManagedIdentity"] = "true"
		return ""
	}
	if auth == configuredWorkloadToken {
		select {
		case <-wiReady:
		case <-ctx.Done():
			framework.Failf("waiting for configured workload identity setup: %v", ctx.Err())
		}
		framework.ExpectNoError(errWISetup)
		gomega.Expect(wiSetupSucceeded).To(gomega.BeTrue())
		gomega.Expect(wiClientID).NotTo(gomega.BeEmpty())
		attributes["mountWithWorkloadIdentityToken"] = "true"
		return wiServiceAccountName
	}
	ginkgo.By("creating a dedicated identity distinct from the controller and default node identities")
	identityName := "azurefile-auth-" + uuid.NewString()
	identity, err := suiteAzureClient.CreateUserAssignedIdentity(ctx, suiteCreds.ResourceGroup, identityName, suiteCreds.Location)
	framework.ExpectNoError(err)
	providerID := ""
	ginkgo.DeferCleanup(func(ctx context.Context) error {
		if providerID != "" {
			if err := suiteAzureClient.SetNodeUserAssignedIdentity(ctx, providerID, identity.ResourceID, false); err != nil {
				return fmt.Errorf("retaining identity %s because detaching it from %s failed: %w", identityName, providerID, err)
			}
		}
		return suiteAzureClient.DeleteUserAssignedIdentity(ctx, suiteCreds.ResourceGroup, identityName)
	})
	gomega.Expect(identity.ClientID).NotTo(gomega.Equal(suiteCreds.AADClientID), "the workload identity must differ from the controller credential")
	defaultIdentity, err := suiteAzureClient.GetNodeIdentityInfo(ctx, suiteCreds.ResourceGroup)
	framework.ExpectNoError(err)
	gomega.Expect(identity.ClientID).NotTo(gomega.Equal(defaultIdentity.ClientID), "the test identity must differ from the default node identity")

	role := wiStorageFileDataSMBMIAdmin
	if auth == workloadKey {
		role = wiStorageAccountContributor
	}
	switch {
	case !grantRole:
		ginkgo.By("withholding the storage role for a negative control")
	case roleScope == "":
		ginkgo.By("assigning the authentication-specific storage role")
		framework.ExpectNoError(suiteAzureClient.AssignRoleToIdentity(ctx, suiteCreds.ResourceGroup, identity.PrincipalID, role))
		ginkgo.DeferCleanup(func(ctx context.Context) error {
			return suiteAzureClient.RemoveRoleFromIdentity(ctx, suiteCreds.ResourceGroup, identity.PrincipalID, role)
		})
	default:
		ginkgo.By("assigning the authentication-specific storage role")
		assignment, err := suiteAzureClient.AssignOwnedRoleAtScope(ctx, roleScope, identity.PrincipalID, role)
		framework.ExpectNoError(err)
		ginkgo.DeferCleanup(func(ctx context.Context) error {
			return suiteAzureClient.DeleteOwnedRoleAtScope(ctx, roleScope, assignment)
		})
	}
	if auth == managedToken {
		ginkgo.By("attaching the dedicated managed identity to the selected worker")
		providerID = node.Spec.ProviderID
		framework.ExpectNoError(suiteAzureClient.SetNodeUserAssignedIdentity(ctx, node.Spec.ProviderID, identity.ResourceID, true))
		attributes["mountWithManagedIdentity"] = "true"
		attributes["clientID"] = identity.ClientID
		return ""
	}

	ginkgo.By("waiting for suite OIDC setup")
	select {
	case <-wiReady:
	case <-ctx.Done():
		framework.Failf("waiting for OIDC setup: %v", ctx.Err())
	}
	gomega.Expect(wiSetupSucceeded).To(gomega.BeTrue(), "suite OIDC setup must succeed")
	framework.ExpectNoError(errWISetup)
	issuer, err := discoverOIDCIssuer(ctx, f.ClientSet)
	framework.ExpectNoError(err)
	ginkgo.By("creating a namespace-local service account and federated identity credential")
	saName := "auth-" + uuid.NewString()
	_, err = f.ClientSet.CoreV1().ServiceAccounts(f.Namespace.Name).Create(ctx, &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: saName,
			Annotations: map[string]string{
				"azure.workload.identity/client-id": identity.ClientID,
				"azure.workload.identity/tenant-id": suiteCreds.TenantID,
			},
		},
	}, metav1.CreateOptions{})
	framework.ExpectNoError(err)
	subject := fmt.Sprintf("system:serviceaccount:%s:%s", f.Namespace.Name, saName)
	framework.ExpectNoError(suiteAzureClient.CreateFederatedIdentityCredential(ctx, suiteCreds.ResourceGroup, identityName, "auth", issuer, subject))
	cloudConfig, env, err := azclient.GetAzureCloudConfigAndEnvConfig(&azclient.ARMClientConfig{Cloud: suiteCreds.Cloud, TenantID: suiteCreds.TenantID})
	framework.ExpectNoError(err)
	credential, err := azidentity.NewClientAssertionCredential(suiteCreds.TenantID, identity.ClientID, func(ctx context.Context) (string, error) {
		token, err := f.ClientSet.CoreV1().ServiceAccounts(f.Namespace.Name).CreateToken(ctx, saName, &authv1.TokenRequest{
			Spec: authv1.TokenRequestSpec{Audiences: []string{"api://AzureADTokenExchange"}, ExpirationSeconds: to.Ptr(int64(3600))},
		}, metav1.CreateOptions{})
		if err != nil {
			return "", err
		}
		if token.Status.Token == "" {
			return "", fmt.Errorf("service account %s/%s returned an empty token", f.Namespace.Name, saName)
		}
		return token.Status.Token, nil
	}, &azidentity.ClientAssertionCredentialOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
	framework.ExpectNoError(err)
	scope := "https://storage.azure.com/.default"
	if auth == workloadKey {
		scope = strings.TrimRight(env.ResourceManagerEndpoint, "/") + "/.default"
	}
	ginkgo.By("waiting for the new federated identity to exchange its service account token")
	var accessToken azcore.AccessToken
	gomega.Eventually(ctx, func(ctx context.Context) error {
		var err error
		accessToken, err = credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
		return err
	}, 10*time.Minute, 10*time.Second).Should(gomega.Succeed(), "new federated identity must exchange its service account token")
	if auth == oauthToken {
		ginkgo.By("creating an OAuth-token Secret without an account key")
		secretName := "auth-oauth-" + uuid.NewString()
		_, err = f.ClientSet.CoreV1().Secrets(f.Namespace.Name).Create(ctx, &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName},
			StringData: map[string]string{"azurestorageaccountname": account, "oauthtoken": accessToken.Token},
		}, metav1.CreateOptions{})
		framework.ExpectNoError(err)
		attributes["mountWithOAuthToken"] = "true"
		attributes["secretName"] = secretName
		if tokenSource != nil {
			// A new credential instance avoids the SDK access-token cache on renewal.
			*tokenSource = func(ctx context.Context) (azcore.AccessToken, error) {
				fresh, err := azidentity.NewClientAssertionCredential(suiteCreds.TenantID, identity.ClientID, func(ctx context.Context) (string, error) {
					token, err := f.ClientSet.CoreV1().ServiceAccounts(f.Namespace.Name).CreateToken(ctx, saName, &authv1.TokenRequest{
						Spec: authv1.TokenRequestSpec{Audiences: []string{"api://AzureADTokenExchange"}, ExpirationSeconds: to.Ptr(int64(3600))},
					}, metav1.CreateOptions{})
					if err != nil {
						return "", err
					}
					return token.Status.Token, nil
				}, &azidentity.ClientAssertionCredentialOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
				if err != nil {
					return azcore.AccessToken{}, err
				}
				return fresh.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
			}
		}
		return ""
	}
	attributes["clientID"] = identity.ClientID
	if auth == workloadToken {
		attributes["mountWithWorkloadIdentityToken"] = "true"
	}
	return saName
}
