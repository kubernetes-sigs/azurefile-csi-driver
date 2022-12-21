/*
Copyright 2020 The Kubernetes Authors.

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
	"log"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-03-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2020-07-01/network"
	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2018-05-01/resources"
	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2019-06-01/storage"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/jongio/azidext/go/azidext"
	"k8s.io/utils/pointer"
)

type Client struct {
	environment         azure.Environment
	subscriptionID      string
	groupsClient        resources.GroupsClient
	vmClient            compute.VirtualMachinesClient
	nicClient           network.InterfacesClient
	subnetsClient       network.SubnetsClient
	vnetClient          network.VirtualNetworksClient
	accountsClient      storage.AccountsClient
	filesharesClient    storage.FileSharesClient
	sshPublicKeysClient compute.SSHPublicKeysClient
}

func GetAzureClient(cloud, subscriptionID, clientID, tenantID, clientSecret string) (*Client, error) {
	env, err := azure.EnvironmentFromName(cloud)
	if err != nil {
		return nil, err
	}

	options := azidentity.ClientSecretCredentialOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: getCloudConfig(env),
		},
	}
	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, &options)
	if err != nil {
		return nil, err
	}

	return getClient(env, subscriptionID, tenantID, cred, env.TokenAudience), nil
}

func (az *Client) GetAzureFilesClient() (storage.FileSharesClient, error) {
	return az.filesharesClient, nil
}

func (az *Client) EnsureSSHPublicKey(ctx context.Context, subscriptionID, resourceGroupName, location, keyName string) (publicKey string, err error) {
	_, err = az.sshPublicKeysClient.Create(ctx, resourceGroupName, keyName, compute.SSHPublicKeyResource{Location: &location})
	if err != nil {
		return "", err
	}
	result, err := az.sshPublicKeysClient.GenerateKeyPair(ctx, resourceGroupName, keyName)
	if err != nil {
		return "", err
	}
	return *result.PublicKey, nil
}

func (az *Client) EnsureResourceGroup(ctx context.Context, name, location string, managedBy *string) (resourceGroup *resources.Group, err error) {
	var tags map[string]*string
	group, err := az.groupsClient.Get(ctx, name)
	if err == nil && group.Tags != nil {
		tags = group.Tags
	} else {
		tags = make(map[string]*string)
	}
	// Tags for correlating resource groups with prow jobs on testgrid
	tags["buildID"] = stringPointer(os.Getenv("BUILD_ID"))
	tags["jobName"] = stringPointer(os.Getenv("JOB_NAME"))
	tags["creationTimestamp"] = stringPointer(time.Now().UTC().Format(time.RFC3339))

	response, err := az.groupsClient.CreateOrUpdate(ctx, name, resources.Group{
		Name:      &name,
		Location:  &location,
		ManagedBy: managedBy,
		Tags:      tags,
	})
	if err != nil {
		return &response, err
	}

	return &response, nil
}

func (az *Client) DeleteResourceGroup(ctx context.Context, groupName string, waitForCompletion bool) error {
	_, err := az.groupsClient.Get(ctx, groupName)
	if err == nil {
		future, err := az.groupsClient.Delete(ctx, groupName)
		if err != nil {
			return fmt.Errorf("cannot delete resource group %v: %v", groupName, err)
		}
		if waitForCompletion {
			err = future.WaitForCompletionRef(ctx, az.groupsClient.Client)
			if err != nil {
				// Skip the teardown errors because of https://github.com/Azure/go-autorest/issues/357
				// TODO(feiskyer): fix the issue by upgrading go-autorest version >= v11.3.2.
				log.Printf("Warning: failed to delete resource group %q with error %v", groupName, err)
			}
		}
	}
	return nil
}

func (az *Client) EnsureVirtualMachine(ctx context.Context, groupName, location, vmName string) (vm compute.VirtualMachine, err error) {
	nic, err := az.EnsureNIC(ctx, groupName, location, vmName+"-nic", vmName+"-vnet", vmName+"-subnet")
	if err != nil {
		return vm, err
	}

	publicKey, err := az.EnsureSSHPublicKey(ctx, az.subscriptionID, groupName, location, "test-key")
	if err != nil {
		return vm, err
	}

	future, err := az.vmClient.CreateOrUpdate(
		ctx,
		groupName,
		vmName,
		compute.VirtualMachine{
			Location: pointer.String(location),
			VirtualMachineProperties: &compute.VirtualMachineProperties{
				HardwareProfile: &compute.HardwareProfile{
					VMSize: "Standard_DS2_v2",
				},
				StorageProfile: &compute.StorageProfile{
					ImageReference: &compute.ImageReference{
						Publisher: pointer.String("Canonical"),
						Offer:     pointer.String("UbuntuServer"),
						Sku:       pointer.String("16.04.0-LTS"),
						Version:   pointer.String("latest"),
					},
				},
				OsProfile: &compute.OSProfile{
					ComputerName:  pointer.String(vmName),
					AdminUsername: pointer.String("azureuser"),
					AdminPassword: pointer.String("Azureuser1234"),
					LinuxConfiguration: &compute.LinuxConfiguration{
						DisablePasswordAuthentication: pointer.Bool(true),
						SSH: &compute.SSHConfiguration{
							PublicKeys: &[]compute.SSHPublicKey{
								{
									Path:    pointer.String("/home/azureuser/.ssh/authorized_keys"),
									KeyData: &publicKey,
								},
							},
						},
					},
				},
				NetworkProfile: &compute.NetworkProfile{
					NetworkInterfaces: &[]compute.NetworkInterfaceReference{
						{
							ID: nic.ID,
							NetworkInterfaceReferenceProperties: &compute.NetworkInterfaceReferenceProperties{
								Primary: pointer.Bool(true),
							},
						},
					},
				},
			},
		},
	)
	if err != nil {
		return vm, fmt.Errorf("cannot create vm: %v", err)
	}

	err = future.WaitForCompletionRef(ctx, az.vmClient.Client)
	if err != nil {
		return vm, fmt.Errorf("cannot get the vm create or update future response: %v", err)
	}

	return future.Result(az.vmClient)
}

func (az *Client) EnsureNIC(ctx context.Context, groupName, location, nicName, vnetName, subnetName string) (nic network.Interface, err error) {
	_, err = az.EnsureVirtualNetworkAndSubnet(ctx, groupName, location, vnetName, subnetName)
	if err != nil {
		return nic, err
	}

	subnet, err := az.GetVirtualNetworkSubnet(ctx, groupName, vnetName, subnetName)
	if err != nil {
		return nic, fmt.Errorf("cannot get subnet %s of virtual network %s in %s: %v", subnetName, vnetName, groupName, err)
	}

	future, err := az.nicClient.CreateOrUpdate(
		ctx,
		groupName,
		nicName,
		network.Interface{
			Name:     pointer.String(nicName),
			Location: pointer.String(location),
			InterfacePropertiesFormat: &network.InterfacePropertiesFormat{
				IPConfigurations: &[]network.InterfaceIPConfiguration{
					{
						Name: pointer.String("ipConfig1"),
						InterfaceIPConfigurationPropertiesFormat: &network.InterfaceIPConfigurationPropertiesFormat{
							Subnet:                    &subnet,
							PrivateIPAllocationMethod: network.Dynamic,
						},
					},
				},
			},
		},
	)
	if err != nil {
		return nic, fmt.Errorf("cannot create nic: %v", err)
	}

	err = future.WaitForCompletionRef(ctx, az.nicClient.Client)
	if err != nil {
		return nic, fmt.Errorf("cannot get nic create or update future response: %v", err)
	}

	return future.Result(az.nicClient)
}

func (az *Client) EnsureVirtualNetworkAndSubnet(ctx context.Context, groupName, location, vnetName, subnetName string) (vnet network.VirtualNetwork, err error) {
	future, err := az.vnetClient.CreateOrUpdate(
		ctx,
		groupName,
		vnetName,
		network.VirtualNetwork{
			Location: pointer.String(location),
			VirtualNetworkPropertiesFormat: &network.VirtualNetworkPropertiesFormat{
				AddressSpace: &network.AddressSpace{
					AddressPrefixes: &[]string{"10.0.0.0/8"},
				},
				Subnets: &[]network.Subnet{
					{
						Name: pointer.String(subnetName),
						SubnetPropertiesFormat: &network.SubnetPropertiesFormat{
							AddressPrefix: pointer.String("10.0.0.0/16"),
						},
					},
				},
			},
		})

	if err != nil {
		return vnet, fmt.Errorf("cannot create virtual network: %v", err)
	}

	err = future.WaitForCompletionRef(ctx, az.vnetClient.Client)
	if err != nil {
		return vnet, fmt.Errorf("cannot get the vnet create or update future response: %v", err)
	}

	return future.Result(az.vnetClient)
}

func (az *Client) GetVirtualNetworkSubnet(ctx context.Context, groupName, vnetName, subnetName string) (network.Subnet, error) {
	return az.subnetsClient.Get(ctx, groupName, vnetName, subnetName, "")
}

func (az *Client) GetStorageAccount(ctx context.Context, groupName, accountName string) (storage.Account, error) {
	return az.accountsClient.GetProperties(ctx, groupName, accountName, "")
}

func (az *Client) GetAccountNumByResourceGroup(ctx context.Context, groupName string) (count int, err error) {
	result, err := az.accountsClient.ListByResourceGroup(ctx, groupName)
	if err != nil {
		return -1, err
	}
	return len(*result.Value), nil
}

func getCloudConfig(env azure.Environment) cloud.Configuration {
	switch env.Name {
	case azure.USGovernmentCloud.Name:
		return cloud.AzureGovernment
	case azure.ChinaCloud.Name:
		return cloud.AzureChina
	case azure.PublicCloud.Name:
		return cloud.AzurePublic
	default:
		return cloud.Configuration{
			ActiveDirectoryAuthorityHost: env.ActiveDirectoryEndpoint,
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {
					Audience: env.TokenAudience,
					Endpoint: env.ResourceManagerEndpoint,
				},
			},
		}
	}
}

func getClient(env azure.Environment, subscriptionID, tenantID string, cred *azidentity.ClientSecretCredential, scope string) *Client {
	c := &Client{
		environment:         env,
		subscriptionID:      subscriptionID,
		groupsClient:        resources.NewGroupsClientWithBaseURI(env.ResourceManagerEndpoint, subscriptionID),
		vmClient:            compute.NewVirtualMachinesClient(subscriptionID),
		nicClient:           network.NewInterfacesClient(subscriptionID),
		subnetsClient:       network.NewSubnetsClient(subscriptionID),
		vnetClient:          network.NewVirtualNetworksClient(subscriptionID),
		accountsClient:      storage.NewAccountsClient(subscriptionID),
		filesharesClient:    storage.NewFileSharesClient(subscriptionID),
		sshPublicKeysClient: compute.NewSSHPublicKeysClientWithBaseURI(env.ResourceManagerEndpoint, subscriptionID),
	}

	if !strings.HasSuffix(scope, "/.default") {
		scope += "/.default"
	}
	// Use an adapter so azidentity in the Azure SDK can be used as Authorizer
	// when calling the Azure Management Packages, which we currently use. Once
	// the Azure SDK clients (found in /sdk) move to stable, we can update our
	// clients and they will be able to use the creds directly without the
	// authorizer.
	authorizer := azidext.NewTokenCredentialAdapter(cred, []string{scope})
	c.groupsClient.Authorizer = authorizer
	c.vmClient.Authorizer = authorizer
	c.nicClient.Authorizer = authorizer
	c.subnetsClient.Authorizer = authorizer
	c.vnetClient.Authorizer = authorizer
	c.accountsClient.Authorizer = authorizer
	c.filesharesClient.Authorizer = authorizer
	c.sshPublicKeysClient.Authorizer = authorizer

	return c
}

func stringPointer(s string) *string {
	return &s
}
