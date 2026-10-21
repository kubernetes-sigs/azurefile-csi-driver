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
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	armmsi "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	storage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage/v2"
	"github.com/google/uuid"
)

func (az *Client) CreateSMBOAuthAccount(ctx context.Context, resourceGroup, name, location string, tags map[string]*string) error {
	_, err := az.accountsClient.Create(ctx, resourceGroup, name, &storage.AccountCreateParameters{
		Location: to.Ptr(location),
		Kind:     to.Ptr(storage.KindFileStorage),
		SKU:      &storage.SKU{Name: to.Ptr(storage.SKUNamePremiumLRS)},
		Tags:     tags,
		Properties: &storage.AccountPropertiesCreateParameters{
			EnableHTTPSTrafficOnly: to.Ptr(true),
			AzureFilesIdentityBasedAuthentication: &storage.AzureFilesIdentityBasedAuthentication{
				DirectoryServiceOptions: to.Ptr(storage.DirectoryServiceOptionsNone),
				SmbOAuthSettings:        &storage.SmbOAuthSettings{IsSmbOAuthEnabled: to.Ptr(true)},
			},
		},
	})
	return err
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
	switch strings.ToLower(id.ResourceType.String()) {
	case "microsoft.compute/virtualmachines":
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
	case "microsoft.compute/virtualmachinescalesets/virtualmachines":
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
	default:
		return fmt.Errorf("unsupported node provider resource type %s", id.ResourceType.String())
	}
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
