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

package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	storage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/accountclient/mock_accountclient"
)

type identityTestCredential struct{}

func (identityTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestAuthenticationAccountSetsActualSharedKeyPolicy(t *testing.T) {
	for _, allow := range []*bool{nil, to.Ptr(false), to.Ptr(true)} {
		t.Run(fmt.Sprint(allow), func(t *testing.T) {
			accounts := mock_accountclient.NewMockInterface(gomock.NewController(t))
			accounts.EXPECT().Create(gomock.Any(), "rg", "account", gomock.Any()).DoAndReturn(
				func(_ context.Context, _, _ string, parameters *storage.AccountCreateParameters) (*storage.Account, error) {
					if parameters.Properties == nil {
						t.Fatal("missing ARM account properties")
					}
					if !reflect.DeepEqual(parameters.Properties.AllowSharedKeyAccess, allow) {
						t.Error("the shared-key policy must be an ARM account property")
					}
					settings := parameters.Properties.AzureFilesIdentityBasedAuthentication
					if settings == nil || settings.SmbOAuthSettings == nil || !ptr.Deref(settings.SmbOAuthSettings.IsSmbOAuthEnabled, false) {
						t.Error("SMB OAuth must remain enabled for either policy")
					}
					return &storage.Account{}, nil
				})
			client := &Client{accountsClient: accounts}
			if err := client.CreateSMBOAuthAccountWithSharedKeyPolicy(context.Background(), "rg", "account", "eastus", nil, allow); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthenticationOwnedRoleUsesShareScopeAndExactCleanupID(t *testing.T) {
	const scope = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/account/fileServices/default/shares/owned-share"
	var assignments, deletions []string
	transport := identityTestTransport(func(request *http.Request) (*http.Response, error) {
		prefix := scope + "/providers/Microsoft.Authorization/roleAssignments/"
		if !strings.HasPrefix(request.URL.Path, prefix) {
			t.Errorf("role request escaped its share scope: %s", request.URL.Path)
		}
		id := strings.TrimPrefix(request.URL.Path, prefix)
		if request.Method == http.MethodPut {
			assignments = append(assignments, id)
			var body armauthorization.RoleAssignmentCreateParameters
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Properties == nil || ptr.Deref(body.Properties.PrincipalID, "") != "principal" ||
				ptr.Deref(body.Properties.RoleDefinitionID, "") != "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/role" {
				t.Error("role assignment did not preserve its intended principal and definition")
			}
		} else if request.Method == http.MethodDelete {
			deletions = append(deletions, id)
		} else {
			t.Errorf("unexpected role operation %s", request.Method)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})
	roles, err := armauthorization.NewRoleAssignmentsClient("sub", identityTestCredential{},
		&arm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{subscriptionID: "sub", roleClient: roles}
	for i := 0; i < 2; i++ {
		id, err := client.AssignOwnedRoleAtScope(context.Background(), scope, "principal", "role")
		if err != nil {
			t.Fatal(err)
		}
		if err := client.DeleteOwnedRoleAtScope(context.Background(), scope, id); err != nil {
			t.Fatal(err)
		}
	}
	if len(assignments) != 2 || assignments[0] == assignments[1] || !reflect.DeepEqual(assignments, deletions) {
		t.Error("each owned assignment must be unique and cleaned by exactly its returned ID")
	}
	if _, err := client.AssignOwnedRoleAtScope(context.Background(), strings.Replace(scope, "/sub/", "/another-sub/", 1), "principal", "role"); err == nil {
		t.Error("cross-subscription role scope accepted")
	}
}

func TestAuthenticationAccountKeyUsesOnlyNamedFixtureAccount(t *testing.T) {
	accounts := mock_accountclient.NewMockInterface(gomock.NewController(t))
	accounts.EXPECT().ListKeys(gomock.Any(), "rg", "owned-account").Return([]*storage.AccountKey{
		nil, {}, {Value: to.Ptr("")}, {Value: to.Ptr("valid-fixture-key")},
	}, nil)
	client := &Client{accountsClient: accounts}
	key, err := client.GetAuthenticationAccountKey(context.Background(), "rg", "owned-account")
	if err != nil || key != "valid-fixture-key" {
		t.Fatal("a nonempty key for the named account must be returned")
	}
	accounts.EXPECT().ListKeys(gomock.Any(), "rg", "empty-account").Return([]*storage.AccountKey{}, nil)
	if _, err := client.GetAuthenticationAccountKey(context.Background(), "rg", "empty-account"); err == nil {
		t.Error("an account without a usable key must fail explicitly")
	}
}

type identityTestTransport func(*http.Request) (*http.Response, error)

func (transport identityTestTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestSetNodeUserAssignedIdentityPatchesOnlySelectedNode(t *testing.T) {
	const (
		existingID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/default"
		ownedID    = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test"
	)
	for _, resource := range []string{"virtualMachines/worker", "virtualMachineScaleSets/workers/virtualMachines/0"} {
		for _, attach := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/attach=%t", resource, attach), func(t *testing.T) {
				patches := 0
				nodeID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/" + resource
				updateMethod := http.MethodPatch
				if strings.HasPrefix(resource, "virtualMachineScaleSets/") {
					updateMethod = http.MethodPut
				}
				transport := identityTestTransport(func(request *http.Request) (*http.Response, error) {
					if request.URL.Path != nodeID {
						t.Errorf("request modified an unexpected resource: %s", request.URL.Path)
					}
					if request.Method == updateMethod {
						patches++
						if request.Header.Get("If-Match") != `"original"` {
							t.Error("identity patch must reject concurrent VM changes")
						}
						var body map[string]json.RawMessage
						if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						for field := range body {
							if field != "identity" && field != "location" {
								t.Errorf("identity patch also changed %q", field)
							}
						}
						var identity struct {
							Type                   string
							UserAssignedIdentities map[string]*struct{}
						}
						if err := json.Unmarshal(body["identity"], &identity); err != nil {
							t.Fatal(err)
						}
						if identity.Type != string(armcompute.ResourceIdentityTypeSystemAssignedUserAssigned) || identity.UserAssignedIdentities[existingID] == nil {
							t.Error("patch must preserve the system and existing user identity")
						}
						value, present := identity.UserAssignedIdentities[ownedID]
						if !present || (value != nil) != attach {
							t.Error("owned identity must be added as an object or removed as an explicit null")
						}
					} else if request.Method != http.MethodGet {
						t.Errorf("unexpected request method %s", request.Method)
					}
					identities := map[string]map[string]string{existingID: {}}
					if !attach {
						identities[ownedID] = map[string]string{}
					}
					body, err := json.Marshal(map[string]interface{}{
						"id": nodeID, "location": "eastus2", "etag": `"original"`,
						"properties": map[string]string{"provisioningState": "Succeeded"},
						"identity": map[string]interface{}{
							"type": "SystemAssigned, UserAssigned", "userAssignedIdentities": identities,
						},
					})
					if err != nil {
						t.Fatal(err)
					}
					return &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
						Body: io.NopCloser(strings.NewReader(string(body))), Request: request,
					}, nil
				})
				options := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}}
				vmClient, err := armcompute.NewVirtualMachinesClient("sub", identityTestCredential{}, options)
				if err != nil {
					t.Fatal(err)
				}
				vmssVMClient, err := armcompute.NewVirtualMachineScaleSetVMsClient("sub", identityTestCredential{}, options)
				if err != nil {
					t.Fatal(err)
				}
				client := &Client{subscriptionID: "sub", vmClient: vmClient, vmssVMClient: vmssVMClient}
				if err := client.SetNodeUserAssignedIdentity(context.Background(), "azure://"+nodeID, ownedID, attach); err != nil {
					t.Fatal(err)
				}
				if patches != 1 {
					t.Errorf("got %d identity patches, want exactly one", patches)
				}
			})
		}
	}
}

func TestUpdatedVMIdentityPreservesExistingAssignments(t *testing.T) {
	existing := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/default"
	testIdentity := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test"
	tests := []struct {
		name      string
		current   *armcompute.VirtualMachineIdentity
		attach    bool
		wantType  armcompute.ResourceIdentityType
		wantIDs   []string
		removedID string
	}{
		{
			name: "attach to a VM without an identity", attach: true,
			wantType: armcompute.ResourceIdentityTypeUserAssigned, wantIDs: []string{testIdentity},
		},
		{
			name: "retain system identity while attaching", attach: true,
			current:  &armcompute.VirtualMachineIdentity{Type: to.Ptr(armcompute.ResourceIdentityTypeSystemAssigned)},
			wantType: armcompute.ResourceIdentityTypeSystemAssignedUserAssigned, wantIDs: []string{testIdentity},
		},
		{
			name: "retain other user identities while attaching", attach: true,
			current: &armcompute.VirtualMachineIdentity{
				Type: to.Ptr(armcompute.ResourceIdentityTypeUserAssigned),
				UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
					existing: {ClientID: to.Ptr("original-client"), PrincipalID: to.Ptr("original-principal")},
				},
			},
			wantType: armcompute.ResourceIdentityTypeUserAssigned, wantIDs: []string{existing, testIdentity},
		},
		{
			name: "detach only the owned identity, matching ARM IDs case insensitively",
			current: &armcompute.VirtualMachineIdentity{
				Type: to.Ptr(armcompute.ResourceIdentityTypeSystemAssignedUserAssigned),
				UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
					existing: {}, "/SUBSCRIPTIONS/sub/RESOURCEGROUPS/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/TEST": {},
				},
			},
			wantType: armcompute.ResourceIdentityTypeSystemAssignedUserAssigned, wantIDs: []string{existing},
			removedID: "/SUBSCRIPTIONS/sub/RESOURCEGROUPS/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/TEST",
		},
		{
			name: "retain the system identity after detaching the last user identity",
			current: &armcompute.VirtualMachineIdentity{
				Type:                   to.Ptr(armcompute.ResourceIdentityTypeSystemAssignedUserAssigned),
				UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{testIdentity: {}},
			},
			wantType:  armcompute.ResourceIdentityTypeSystemAssigned,
			removedID: testIdentity,
		},
		{
			name: "clear user-assigned type after detaching the only identity",
			current: &armcompute.VirtualMachineIdentity{
				Type:                   to.Ptr(armcompute.ResourceIdentityTypeUserAssigned),
				UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{testIdentity: {}},
			},
			wantType:  armcompute.ResourceIdentityTypeNone,
			removedID: testIdentity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var before *armcompute.VirtualMachineIdentity
			if tt.current != nil {
				snapshot := *tt.current
				snapshot.UserAssignedIdentities = make(map[string]*armcompute.UserAssignedIdentitiesValue)
				for id, value := range tt.current.UserAssignedIdentities {
					snapshot.UserAssignedIdentities[id] = value
				}
				if tt.current.UserAssignedIdentities == nil {
					snapshot.UserAssignedIdentities = nil
				}
				before = &snapshot
			}
			got := updatedVMIdentity(tt.current, testIdentity, tt.attach)
			if got.Type == nil || *got.Type != tt.wantType {
				t.Fatalf("identity type = %v, want %s", got.Type, tt.wantType)
			}
			wantCount := len(tt.wantIDs)
			if tt.removedID != "" {
				wantCount++
				if value, ok := got.UserAssignedIdentities[tt.removedID]; !ok || value != nil {
					t.Errorf("removed identity %q must have an explicit null PATCH entry", tt.removedID)
				}
			}
			if len(got.UserAssignedIdentities) != wantCount {
				t.Fatalf("identity IDs = %v, want %v", got.UserAssignedIdentities, tt.wantIDs)
			}
			for _, id := range tt.wantIDs {
				value, ok := got.UserAssignedIdentities[id]
				if !ok || value == nil || value.ClientID != nil || value.PrincipalID != nil {
					t.Errorf("identity %q must be preserved without read-only fields, got %v", id, value)
				}
			}
			if !reflect.DeepEqual(tt.current, before) {
				t.Error("identity patch mutated the original VM response")
			}
		})
	}
}

func TestSetNodeUserAssignedIdentityRetriesConcurrentUpdates(t *testing.T) {
	const (
		nodeID   = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/worker"
		peerID   = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/peer"
		identity = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test"
	)
	original := identityUpdateRetryDelay
	identityUpdateRetryDelay = time.Millisecond
	t.Cleanup(func() { identityUpdateRetryDelay = original })
	missing := func(id string) string {
		return `{"error":{"code":"FailedIdentityOperation","message":"Resource '` + strings.ToLower(id) + `' was not found."}}`
	}
	for _, test := range []struct {
		name        string
		firstStatus int
		firstBody   string
		wantPatches int
		wantErr     bool
	}{
		{"stale etag re-reads the peer's update", http.StatusPreconditionFailed, "", 2, false},
		{"in-progress update", http.StatusConflict, "", 2, false},
		{"stale read lists a deleted peer identity", http.StatusBadRequest, missing(peerID + "-deleted"), 2, false},
		{"own identity is missing", http.StatusBadRequest, missing(identity), 1, true},
		{"authorization failure", http.StatusForbidden, "", 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			patches := 0
			transport := identityTestTransport(func(request *http.Request) (*http.Response, error) {
				// A peer's identity lands between the first read and the first patch.
				etag, identities := `"original"`, map[string]map[string]string{}
				if patches > 0 {
					etag, identities = `"peer"`, map[string]map[string]string{peerID: {}}
				}
				status := http.StatusOK
				if request.Method == http.MethodPatch {
					patches++
					var body struct {
						Identity struct{ UserAssignedIdentities map[string]*struct{} }
					}
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if patches == 1 {
						status = test.firstStatus
					} else if request.Header.Get("If-Match") != `"peer"` || body.Identity.UserAssignedIdentities[peerID] == nil {
						t.Error("a retry must use the re-read ETag and preserve the peer's identity")
					}
				}
				payload, err := json.Marshal(map[string]interface{}{
					"id": nodeID, "location": "eastus2", "etag": etag,
					"properties": map[string]string{"provisioningState": "Succeeded"},
					"identity":   map[string]interface{}{"type": "UserAssigned", "userAssignedIdentities": identities},
				})
				if err != nil {
					t.Fatal(err)
				}
				if status != http.StatusOK {
					payload = []byte(`{"error":{"code":"Rejected","message":"rejected"}}`)
					if test.firstBody != "" {
						payload = []byte(test.firstBody)
					}
				}
				return &http.Response{
					StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(string(payload))), Request: request,
				}, nil
			})
			options := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}}
			vmClient, err := armcompute.NewVirtualMachinesClient("sub", identityTestCredential{}, options)
			if err != nil {
				t.Fatal(err)
			}
			client := &Client{subscriptionID: "sub", vmClient: vmClient}
			err = client.SetNodeUserAssignedIdentity(context.Background(), "azure://"+nodeID, identity, true)
			if (err != nil) != test.wantErr {
				t.Fatalf("got error %v, want error %t", err, test.wantErr)
			}
			if patches != test.wantPatches {
				t.Errorf("got %d identity patches, want %d", patches, test.wantPatches)
			}
		})
	}
}
