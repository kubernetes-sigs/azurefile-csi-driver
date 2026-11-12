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
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	storage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"github.com/google/uuid"
)

// CreateSMBOAuthAccountWithSharedKeyPolicy sets the service policy, not a CSI volume parameter.
// Infrastructure encryption isolates these accounts from other specs' account
// search while keeping auth rows on the search path (unlike skip-matching).
func (az *Client) CreateSMBOAuthAccountWithSharedKeyPolicy(ctx context.Context, resourceGroup, name, location string, tags map[string]*string, allowSharedKey *bool) error {
	_, err := az.accountsClient.Create(ctx, resourceGroup, name, &storage.AccountCreateParameters{
		Location: to.Ptr(location),
		Kind:     to.Ptr(storage.KindFileStorage),
		SKU:      &storage.SKU{Name: to.Ptr(storage.SKUNamePremiumLRS)},
		Tags:     tags,
		Properties: &storage.AccountPropertiesCreateParameters{
			EnableHTTPSTrafficOnly: to.Ptr(true),
			AllowSharedKeyAccess:   allowSharedKey,
			Encryption: &storage.Encryption{
				KeySource:                       to.Ptr(storage.KeySourceMicrosoftStorage),
				RequireInfrastructureEncryption: to.Ptr(true),
			},
			AzureFilesIdentityBasedAuthentication: &storage.AzureFilesIdentityBasedAuthentication{
				DirectoryServiceOptions: to.Ptr(storage.DirectoryServiceOptionsNone),
				SmbOAuthSettings:        &storage.SmbOAuthSettings{IsSmbOAuthEnabled: to.Ptr(true)},
			},
		},
	})
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) && responseErr.ErrorCode == "StorageAccountOperationInProgress" {
		// The name is unique to this spec, so the conflicting operation is an SDK
		// retry of this create racing the original request, which still completes.
		log.Printf("waiting for in-progress creation of storage account %s", name)
		return az.waitForStorageAccount(ctx, resourceGroup, name)
	}
	return err
}

func (az *Client) waitForStorageAccount(ctx context.Context, resourceGroup, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		account, err := az.accountsClient.GetProperties(ctx, resourceGroup, name, nil)
		if err == nil && account.Properties != nil && account.Properties.ProvisioningState != nil &&
			*account.Properties.ProvisioningState == storage.ProvisioningStateSucceeded {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for storage account %s: %w (last error: %v)", name, ctx.Err(), err)
		case <-time.After(10 * time.Second):
		}
	}
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

// GetApprovedPrivateEndpoint returns the ID and private IPv4 address of the
// account's approved private endpoint.
func (az *Client) GetApprovedPrivateEndpoint(ctx context.Context, resourceGroup, account string) (string, string, error) {
	properties, err := az.accountsClient.GetProperties(ctx, resourceGroup, account, nil)
	if err != nil {
		return "", "", err
	}
	if properties.Properties == nil {
		return "", "", fmt.Errorf("account %s returned no properties", account)
	}
	for _, connection := range properties.Properties.PrivateEndpointConnections {
		if connection == nil || connection.Properties == nil || connection.Properties.PrivateEndpoint == nil ||
			connection.Properties.PrivateEndpoint.ID == nil || connection.Properties.PrivateLinkServiceConnectionState == nil ||
			connection.Properties.PrivateLinkServiceConnectionState.Status == nil ||
			*connection.Properties.PrivateLinkServiceConnectionState.Status != storage.PrivateEndpointServiceConnectionStatusApproved {
			continue
		}
		endpointID := *connection.Properties.PrivateEndpoint.ID
		endpointResource, err := arm.ParseResourceID(endpointID)
		if err != nil {
			return "", "", err
		}
		endpoint, err := az.endpointsClient.Get(ctx, endpointResource.ResourceGroupName, endpointResource.Name, nil)
		if err != nil {
			return "", "", err
		}
		if endpoint.Properties == nil {
			continue
		}
		for _, nic := range endpoint.Properties.NetworkInterfaces {
			if nic == nil || nic.ID == nil {
				continue
			}
			nicResource, err := arm.ParseResourceID(*nic.ID)
			if err != nil {
				return "", "", err
			}
			networkInterface, err := az.interfacesClient.Get(ctx, nicResource.ResourceGroupName, nicResource.Name, nil)
			if err != nil {
				return "", "", err
			}
			if networkInterface.Properties == nil {
				continue
			}
			for _, config := range networkInterface.Properties.IPConfigurations {
				if config != nil && config.Properties != nil && config.Properties.PrivateIPAddress != nil {
					return endpointID, *config.Properties.PrivateIPAddress, nil
				}
			}
		}
	}
	return "", "", fmt.Errorf("account %s has no approved private endpoint with a private IP address", account)
}

func (az *Client) DeletePrivateEndpoint(ctx context.Context, endpointID string) error {
	endpointResource, err := arm.ParseResourceID(endpointID)
	if err != nil {
		return err
	}
	poller, err := az.endpointsClient.BeginDelete(ctx, endpointResource.ResourceGroupName, endpointResource.Name, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (az *Client) DeleteUserAssignedIdentity(ctx context.Context, resourceGroup, name string) error {
	_, err := az.uamiClient.Delete(ctx, resourceGroup, name, nil)
	return err
}

func (az *Client) ResourceGroupScope(resourceGroup string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", az.subscriptionID, resourceGroup)
}

func (az *Client) StorageAccountScope(resourceGroup, account string) string {
	return az.ResourceGroupScope(resourceGroup) + "/providers/Microsoft.Storage/storageAccounts/" + account
}

// RemoveRoleAtScope removes the deterministic assignment created by AssignRoleAtScope.
func (az *Client) RemoveRoleAtScope(ctx context.Context, scope, principalID, roleDefinitionID string) error {
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
