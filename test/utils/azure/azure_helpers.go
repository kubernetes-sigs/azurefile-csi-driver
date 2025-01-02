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
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	compute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	network "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	resources "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	storage "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/accountclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/fileshareclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/interfaceclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/resourcegroupclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/sshpublickeyresourceclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/subnetclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/virtualmachineclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/virtualnetworkclient"
)

type Client struct {
	groupsClient        resourcegroupclient.Interface
	vmClient            virtualmachineclient.Interface
	nicClient           interfaceclient.Interface
	subnetsClient       subnetclient.Interface
	vnetClient          virtualnetworkclient.Interface
	accountsClient      accountclient.Interface
	filesharesClient    fileshareclient.Interface
	sshPublicKeysClient sshpublickeyresourceclient.Interface
}

func GetAzureClient(cloud, subscriptionID, clientID, tenantID, clientSecret, aadFederatedTokenFile string) (*Client, error) {
	armConfig := &azclient.ARMClientConfig{
		Cloud:     cloud,
		TenantID:  tenantID,
		UserAgent: azurefile.GetUserAgent(azurefile.DefaultDriverName, "", "e2e-test"),
	}
	clientOps, _, err := azclient.GetAzureCloudConfigAndEnvConfig(armConfig)
	if err != nil {
		return nil, err
	}
	useFederatedWorkloadIdentityExtension := false
	if aadFederatedTokenFile != "" {
		useFederatedWorkloadIdentityExtension = true
	}
	credProvider, err := azclient.NewAuthProvider(armConfig, &azclient.AzureAuthConfig{
		AADClientID:                           clientID,
		AADClientSecret:                       clientSecret,
		AADFederatedTokenFile:                 aadFederatedTokenFile,
		UseFederatedWorkloadIdentityExtension: useFederatedWorkloadIdentityExtension,
	})
	if err != nil {
		return nil, err
	}
	cred := credProvider.GetAzIdentity()
	factory, err := azclient.NewClientFactory(&azclient.ClientFactoryConfig{
		SubscriptionID: subscriptionID,
	}, armConfig, clientOps, cred)
	if err != nil {
		return nil, err
	}
	return &Client{
		groupsClient:        factory.GetResourceGroupClient(),
		vmClient:            factory.GetVirtualMachineClient(),
		nicClient:           factory.GetInterfaceClient(),
		subnetsClient:       factory.GetSubnetClient(),
		vnetClient:          factory.GetVirtualNetworkClient(),
		sshPublicKeysClient: factory.GetSSHPublicKeyResourceClient(),
		accountsClient:      factory.GetAccountClient(),
		filesharesClient:    factory.GetFileShareClient(),
	}, nil
}

func (az *Client) GetAzureFilesClient() (fileshareclient.Interface, error) {
	return az.filesharesClient, nil
}

func (az *Client) EnsureSSHPublicKey(ctx context.Context, resourceGroupName, location, keyName string) (publicKey string, err error) {
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

func (az *Client) EnsureResourceGroup(ctx context.Context, name, location string, managedBy *string) (resourceGroup *resources.ResourceGroup, err error) {
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

	response, err := az.groupsClient.CreateOrUpdate(ctx, name, resources.ResourceGroup{
		Name:      &name,
		Location:  &location,
		ManagedBy: managedBy,
		Tags:      tags,
	})
	if err != nil {
		return response, err
	}

	return response, nil
}

func (az *Client) DeleteResourceGroup(ctx context.Context, groupName string) error {
	_, err := az.groupsClient.Get(ctx, groupName)
	if err != nil {
		return err
	}

	timeout := 20 * time.Minute
	ch := make(chan bool, 1)

	go func() {
		err = az.groupsClient.Delete(ctx, groupName)
		ch <- true
	}()

	select {
	case <-ch:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("timeout waiting for resource group %s to be deleted", groupName)
	}
}

func (az *Client) EnsureVirtualMachine(ctx context.Context, groupName, location, vmName string) (vm *compute.VirtualMachine, err error) {
	nic, err := az.EnsureNIC(ctx, groupName, location, vmName+"-nic", vmName+"-vnet", vmName+"-subnet")
	if err != nil {
		return vm, err
	}

	publicKey, err := az.EnsureSSHPublicKey(ctx, groupName, location, "test-key")
	if err != nil {
		return vm, err
	}

	future, err := az.vmClient.CreateOrUpdate(
		ctx,
		groupName,
		vmName,
		compute.VirtualMachine{
			Location: ptr.To(location),
			Properties: &compute.VirtualMachineProperties{
				HardwareProfile: &compute.HardwareProfile{
					VMSize: to.Ptr(compute.VirtualMachineSizeTypesStandardDS2V2),
				},
				StorageProfile: &compute.StorageProfile{
					ImageReference: &compute.ImageReference{
						Publisher: ptr.To("Canonical"),
						Offer:     ptr.To("UbuntuServer"),
						SKU:       ptr.To("16.04.0-LTS"),
						Version:   ptr.To("latest"),
					},
				},
				OSProfile: &compute.OSProfile{
					ComputerName:  ptr.To(vmName),
					AdminUsername: ptr.To("azureuser"),
					AdminPassword: ptr.To("Azureuser1234"),
					LinuxConfiguration: &compute.LinuxConfiguration{
						DisablePasswordAuthentication: ptr.To(true),
						SSH: &compute.SSHConfiguration{
							PublicKeys: []*compute.SSHPublicKey{
								{
									Path:    ptr.To("/home/azureuser/.ssh/authorized_keys"),
									KeyData: &publicKey,
								},
							},
						},
					},
				},
				NetworkProfile: &compute.NetworkProfile{
					NetworkInterfaces: []*compute.NetworkInterfaceReference{
						{
							ID: nic.ID,
							Properties: &compute.NetworkInterfaceReferenceProperties{
								Primary: ptr.To(true),
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

	return future, nil
}

func (az *Client) EnsureNIC(ctx context.Context, groupName, location, nicName, vnetName, subnetName string) (nic *network.Interface, err error) {
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
			Name:     ptr.To(nicName),
			Location: ptr.To(location),
			Properties: &armnetwork.InterfacePropertiesFormat{
				IPConfigurations: []*network.InterfaceIPConfiguration{
					{
						Name: ptr.To("ipConfig1"),
						Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
							Subnet:                    subnet,
							PrivateIPAllocationMethod: to.Ptr(network.IPAllocationMethodDynamic),
						},
					},
				},
			},
		},
	)
	if err != nil {
		return nic, fmt.Errorf("cannot create nic: %v", err)
	}

	return future, nil
}

func (az *Client) EnsureVirtualNetworkAndSubnet(ctx context.Context, groupName, location, vnetName, subnetName string) (vnet *network.VirtualNetwork, err error) {
	future, err := az.vnetClient.CreateOrUpdate(
		ctx,
		groupName,
		vnetName,
		network.VirtualNetwork{
			Location: ptr.To(location),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{
					AddressPrefixes: []*string{to.Ptr("10.0.0.0/8")},
				},
				Subnets: []*network.Subnet{
					{
						Name: ptr.To(subnetName),
						Properties: &armnetwork.SubnetPropertiesFormat{
							AddressPrefix: ptr.To("10.0.0.0/16"),
						},
					},
				},
			},
		})

	if err != nil {
		return vnet, fmt.Errorf("cannot create virtual network: %v", err)
	}

	return future, nil
}

func (az *Client) GetVirtualNetworkSubnet(ctx context.Context, groupName, vnetName, subnetName string) (*network.Subnet, error) {
	return az.subnetsClient.Get(ctx, groupName, vnetName, subnetName, nil)
}

func (az *Client) GetStorageAccount(ctx context.Context, groupName, accountName string) (*storage.Account, error) {
	return az.accountsClient.GetProperties(ctx, groupName, accountName, nil)
}

func (az *Client) GetAccountNumByResourceGroup(ctx context.Context, groupName string) (count int, err error) {
	result, err := az.accountsClient.List(ctx, groupName)
	if err != nil {
		return -1, err
	}
	return len(result), nil
}

func stringPointer(s string) *string {
	return &s
}
