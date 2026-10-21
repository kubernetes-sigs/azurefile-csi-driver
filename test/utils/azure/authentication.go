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
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	armmsi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	storage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"github.com/google/uuid"
)

func (az *Client) CreateSMBOAuthAccount(ctx context.Context, resourceGroup, name, location string, tags map[string]*string) error {
	return az.CreateSMBOAuthAccountWithSharedKeyPolicy(ctx, resourceGroup, name, location, tags, nil)
}

// CreateSMBOAuthAccountWithSharedKeyPolicy sets the service policy, not a CSI volume parameter.
func (az *Client) CreateSMBOAuthAccountWithSharedKeyPolicy(ctx context.Context, resourceGroup, name, location string, tags map[string]*string, allowSharedKey *bool) error {
	_, err := az.accountsClient.Create(ctx, resourceGroup, name, &storage.AccountCreateParameters{
		Location: to.Ptr(location),
		Kind:     to.Ptr(storage.KindFileStorage),
		SKU:      &storage.SKU{Name: to.Ptr(storage.SKUNamePremiumLRS)},
		Tags:     tags,
		Properties: &storage.AccountPropertiesCreateParameters{
			EnableHTTPSTrafficOnly: to.Ptr(true),
			AllowSharedKeyAccess:   allowSharedKey,
			AzureFilesIdentityBasedAuthentication: &storage.AzureFilesIdentityBasedAuthentication{
				DirectoryServiceOptions: to.Ptr(storage.DirectoryServiceOptionsNone),
				SmbOAuthSettings:        &storage.SmbOAuthSettings{IsSmbOAuthEnabled: to.Ptr(true)},
			},
		},
	})
	return err
}

func (az *Client) StorageAccountScope(resourceGroup, account string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s", az.subscriptionID, resourceGroup, account)
}

// AssignOwnedRoleAtScope creates a unique assignment and returns its exact ID for cleanup.
// It does not adopt or delete assignments created by another fixture.
func (az *Client) AssignOwnedRoleAtScope(ctx context.Context, scope, principalID, roleDefinitionID string) (string, error) {
	id, err := arm.ParseResourceID(scope)
	if err != nil || !strings.EqualFold(id.SubscriptionID, az.subscriptionID) {
		return "", fmt.Errorf("role scope must be a resource in the test subscription")
	}
	assignment := uuid.NewString()
	_, err = az.roleClient.Create(ctx, scope, assignment, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID: to.Ptr(principalID), PrincipalType: to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
			RoleDefinitionID: to.Ptr(fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", az.subscriptionID, roleDefinitionID)),
		},
	}, nil)
	if err != nil {
		return "", err
	}
	return assignment, nil
}

func (az *Client) DeleteOwnedRoleAtScope(ctx context.Context, scope, assignment string) error {
	_, err := az.roleClient.Delete(ctx, scope, assignment, nil)
	return err
}

func (az *Client) GetAuthenticationAccountKey(ctx context.Context, resourceGroup, account string) (string, error) {
	result, err := az.accountsClient.ListKeys(ctx, resourceGroup, account)
	if err != nil {
		return "", err
	}
	for _, key := range result {
		if key != nil && key.Value != nil && *key.Value != "" {
			return *key.Value, nil
		}
	}
	return "", fmt.Errorf("account %s returned no usable account key", account)
}

func (az *Client) DeleteStorageAccount(ctx context.Context, resourceGroup, name string) error {
	return az.accountsClient.Delete(ctx, resourceGroup, name)
}

func (az *Client) CreateUserAssignedIdentity(ctx context.Context, resourceGroup, name, location string) (*NodeIdentityInfo, error) {
	response, err := az.identitiesClient.CreateOrUpdate(ctx, resourceGroup, name, armmsi.Identity{Location: to.Ptr(location)}, nil)
	if err != nil {
		return nil, err
	}
	if response.Properties == nil || response.Properties.ClientID == nil || response.Properties.PrincipalID == nil || response.ID == nil {
		return nil, fmt.Errorf("managed identity %s has incomplete identity properties", name)
	}
	return &NodeIdentityInfo{
		ClientID:    *response.Properties.ClientID,
		PrincipalID: *response.Properties.PrincipalID,
		ResourceID:  *response.ID,
	}, nil
}

func (az *Client) DeleteUserAssignedIdentity(ctx context.Context, resourceGroup, name string) error {
	_, err := az.identitiesClient.Delete(ctx, resourceGroup, name, nil)
	return err
}

// RemoveRoleFromIdentity removes the deterministic assignment created by AssignRoleToIdentity.
func (az *Client) RemoveRoleFromIdentity(ctx context.Context, resourceGroup, principalID, roleDefinitionID string) error {
	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", az.subscriptionID, resourceGroup)
	assignmentID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(scope+"|"+principalID+"|"+roleDefinitionID)).String()
	_, err := az.roleClient.Delete(ctx, scope, assignmentID, nil)
	return err
}

// SetNodeUserAssignedIdentity patches only the selected VM's identity, not the VMSS model or its other instances.
func (az *Client) SetNodeUserAssignedIdentity(ctx context.Context, providerID, identityID string, attach bool) error {
	id, err := arm.ParseResourceID(strings.TrimPrefix(providerID, "azure://"))
	if err != nil {
		return fmt.Errorf("parsing node provider ID: %w", err)
	}
	if !strings.EqualFold(id.SubscriptionID, az.subscriptionID) {
		return fmt.Errorf("node subscription %s does not match test subscription %s", id.SubscriptionID, az.subscriptionID)
	}
	var update func(context.Context) error
	switch strings.ToLower(id.ResourceType.String()) {
	case "microsoft.compute/virtualmachines":
		update = func(ctx context.Context) error {
			vm, err := az.vmClient.Get(ctx, id.ResourceGroupName, id.Name, nil)
			if err != nil {
				return err
			}
			poller, err := az.vmClient.BeginUpdate(ctx, id.ResourceGroupName, id.Name, armcompute.VirtualMachineUpdate{
				Identity: updatedVMIdentity(vm.Identity, identityID, attach),
			}, &armcompute.VirtualMachinesClientBeginUpdateOptions{IfMatch: vm.Etag})
			if err != nil {
				return err
			}
			_, err = poller.PollUntilDone(ctx, nil)
			return err
		}
	case "microsoft.compute/virtualmachinescalesets/virtualmachines":
		update = func(ctx context.Context) error {
			vm, err := az.vmssVMClient.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
			if err != nil {
				return err
			}
			poller, err := az.vmssVMClient.BeginUpdate(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, armcompute.VirtualMachineScaleSetVM{
				Location: vm.Location,
				Identity: updatedVMIdentity(vm.Identity, identityID, attach),
			}, &armcompute.VirtualMachineScaleSetVMsClientBeginUpdateOptions{IfMatch: vm.Etag})
			if err != nil {
				return err
			}
			_, err = poller.PollUntilDone(ctx, nil)
			return err
		}
	default:
		return fmt.Errorf("unsupported node provider resource type %s", id.ResourceType.String())
	}
	return retryIdentityUpdate(ctx, identityID, update)
}

// identityUpdateRetryDelay is the first backoff between conflicting identity updates.
var identityUpdateRetryDelay = 2 * time.Second

// retryIdentityUpdate lets concurrent tests patch the same node. Each attempt
// re-reads the node, so a retry never overwrites another test's identity.
func retryIdentityUpdate(ctx context.Context, identityID string, update func(context.Context) error) error {
	delay := identityUpdateRetryDelay
	for attempt := 1; ; attempt++ {
		err := update(ctx)
		if err == nil || attempt == 10 || !retryableIdentityUpdateError(err, identityID) {
			return err
		}
		log.Printf("retrying identity update for %s after concurrent node change (attempt %d)", identityID, attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(2*delay, 30*time.Second)
	}
}

func retryableIdentityUpdateError(err error, identityID string) bool {
	var responseErr *azcore.ResponseError
	if !errors.As(err, &responseErr) {
		return false
	}
	switch responseErr.StatusCode {
	case http.StatusConflict, http.StatusPreconditionFailed, http.StatusTooManyRequests:
		return true
	}
	switch responseErr.ErrorCode {
	case "OperationPreempted", "RetryableError":
		return true
	case "FailedIdentityOperation":
		// Identities are validated before If-Match, so a stale read that still lists
		// a peer's since-deleted identity fails here instead of with 412.
		message := strings.ToLower(err.Error())
		return strings.Contains(message, "was not found") && !strings.Contains(message, strings.ToLower(identityID))
	}
	return false
}

func updatedVMIdentity(current *armcompute.VirtualMachineIdentity, identityID string, attach bool) *armcompute.VirtualMachineIdentity {
	identity := &armcompute.VirtualMachineIdentity{
		Type:                   to.Ptr(armcompute.ResourceIdentityTypeNone),
		UserAssignedIdentities: make(map[string]*armcompute.UserAssignedIdentitiesValue),
	}
	systemAssigned := current != nil && current.Type != nil && strings.Contains(string(*current.Type), "SystemAssigned")
	if current != nil {
		for id := range current.UserAssignedIdentities {
			if !strings.EqualFold(id, identityID) {
				identity.UserAssignedIdentities[id] = &armcompute.UserAssignedIdentitiesValue{}
			} else if !attach {
				// Identity PATCH uses null entries to remove assignments.
				identity.UserAssignedIdentities[id] = nil
			}
		}
	}
	if attach {
		identity.UserAssignedIdentities[identityID] = &armcompute.UserAssignedIdentitiesValue{}
	}
	userAssigned := 0
	for _, value := range identity.UserAssignedIdentities {
		if value != nil {
			userAssigned++
		}
	}
	switch {
	case systemAssigned && userAssigned > 0:
		identity.Type = to.Ptr(armcompute.ResourceIdentityTypeSystemAssignedUserAssigned)
	case systemAssigned:
		identity.Type = to.Ptr(armcompute.ResourceIdentityTypeSystemAssigned)
	case userAssigned > 0:
		identity.Type = to.Ptr(armcompute.ResourceIdentityTypeUserAssigned)
	}
	return identity
}
