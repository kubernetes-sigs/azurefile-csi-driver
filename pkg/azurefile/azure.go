/*
Copyright 2017 The Kubernetes Authors.

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

package azurefile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
)

const (
	DefaultAzureCredentialFileEnv = "AZURE_CREDENTIAL_FILE"
	DefaultCredFilePathLinux      = "/etc/kubernetes/azure.json"
	DefaultCredFilePathWindows    = "C:\\k\\azure.json"
)

var (
	storageService = "Microsoft.Storage"
)

// getCloudProvider get Azure Cloud Provider
func getCloudProvider(kubeconfig, nodeID, secretName, secretNamespace, userAgent string, allowEmptyCloudConfig bool) (*azure.Cloud, error) {
	az := &azure.Cloud{
		InitSecretConfig: azure.InitSecretConfig{
			SecretName:      secretName,
			SecretNamespace: secretNamespace,
			CloudConfigKey:  "cloud-config",
		},
	}

	kubeClient, err := getKubeClient(kubeconfig)
	if err != nil {
		klog.Warningf("get kubeconfig(%s) failed with error: %v", kubeconfig, err)
		if !os.IsNotExist(err) && !errors.Is(err, rest.ErrNotInCluster) {
			return az, fmt.Errorf("failed to get KubeClient: %v", err)
		}
	}

	var (
		config     *azure.Config
		fromSecret bool
	)

	if kubeClient != nil {
		klog.V(2).Infof("reading cloud config from secret %s/%s", az.SecretNamespace, az.SecretName)
		az.KubeClient = kubeClient
		config, err = az.GetConfigFromSecret()
		if err == nil && config != nil {
			fromSecret = true
		}
		if err != nil {
			klog.Warningf("InitializeCloudFromSecret: failed to get cloud config from secret %s/%s: %v", az.SecretNamespace, az.SecretName, err)
		}
	}

	if config == nil {
		klog.V(2).Infof("could not read cloud config from secret %s/%s", az.SecretNamespace, az.SecretName)
		credFile, ok := os.LookupEnv(DefaultAzureCredentialFileEnv)
		if ok && strings.TrimSpace(credFile) != "" {
			klog.V(2).Infof("%s env var set as %v", DefaultAzureCredentialFileEnv, credFile)
		} else {
			if runtime.GOOS == "windows" {
				credFile = DefaultCredFilePathWindows
			} else {
				credFile = DefaultCredFilePathLinux
			}
			klog.V(2).Infof("use default %s env var: %v", DefaultAzureCredentialFileEnv, credFile)
		}

		credFileConfig, err := os.Open(credFile)
		if err != nil {
			klog.Warningf("load azure config from file(%s) failed with %v", credFile, err)
		} else {
			defer credFileConfig.Close()
			klog.V(2).Infof("read cloud config from file: %s successfully", credFile)
			if config, err = azure.ParseConfig(credFileConfig); err != nil {
				klog.Warningf("parse config file(%s) failed with error: %v", credFile, err)
			}
		}
	}

	if config == nil {
		if allowEmptyCloudConfig {
			klog.V(2).Infof("no cloud config provided, error: %v, driver will run without cloud config", err)
		} else {
			return az, fmt.Errorf("no cloud config provided, error: %v", err)
		}
	} else {
		config.UserAgent = userAgent
		if err = az.InitializeCloudFromConfig(config, fromSecret, false); err != nil {
			klog.Warningf("InitializeCloudFromConfig failed with error: %v", err)
		}
	}

	// reassign kubeClient
	if kubeClient != nil && az.KubeClient == nil {
		az.KubeClient = kubeClient
	}

	isController := (nodeID == "")
	if isController {
		if err == nil {
			// Disable UseInstanceMetadata for controller to mitigate a timeout issue using IMDS
			// https://github.com/kubernetes-sigs/azuredisk-csi-driver/issues/168
			klog.V(2).Infof("disable UseInstanceMetadata for controller server")
			az.Config.UseInstanceMetadata = false
		}
		klog.V(2).Infof("starting controller server...")
	} else {
		klog.V(2).Infof("starting node server on node(%s)", nodeID)
	}

	return az, nil
}

func getKubeClient(kubeconfig string) (*kubernetes.Clientset, error) {
	var (
		config *rest.Config
		err    error
	)
	if kubeconfig != "" {
		if config, err = clientcmd.BuildConfigFromFlags("", kubeconfig); err != nil {
			return nil, err
		}
	} else {
		if config, err = rest.InClusterConfig(); err != nil {
			return nil, err
		}
	}

	return kubernetes.NewForConfig(config)
}

func (d *Driver) updateSubnetServiceEndpoints(ctx context.Context, vnetResourceGroup, vnetName, subnetName string) error {
	if d.cloud.SubnetsClient == nil {
		return fmt.Errorf("SubnetsClient is nil")
	}

	if vnetResourceGroup == "" {
		vnetResourceGroup = d.cloud.ResourceGroup
		if len(d.cloud.VnetResourceGroup) > 0 {
			vnetResourceGroup = d.cloud.VnetResourceGroup
		}
	}

	location := d.cloud.Location
	if vnetName == "" {
		vnetName = d.cloud.VnetName
	}
	if subnetName == "" {
		subnetName = d.cloud.SubnetName
	}

	klog.V(2).Infof("updateSubnetServiceEndpoints on vnetName: %s, subnetName: %s, location: %s", vnetName, subnetName, location)
	if subnetName == "" || vnetName == "" || location == "" {
		return fmt.Errorf("value of subnetName, vnetName or location is empty")
	}

	lockKey := vnetResourceGroup + vnetName + subnetName
	d.subnetLockMap.LockEntry(lockKey)
	defer d.subnetLockMap.UnlockEntry(lockKey)

	subnet, err := d.cloud.SubnetsClient.Get(ctx, vnetResourceGroup, vnetName, subnetName, "")
	if err != nil {
		return fmt.Errorf("failed to get the subnet %s under vnet %s: %v", subnetName, vnetName, err)
	}
	endpointLocaions := []string{location}
	storageServiceEndpoint := network.ServiceEndpointPropertiesFormat{
		Service:   &storageService,
		Locations: &endpointLocaions,
	}
	storageServiceExists := false
	if subnet.SubnetPropertiesFormat == nil {
		subnet.SubnetPropertiesFormat = &network.SubnetPropertiesFormat{}
	}
	if subnet.SubnetPropertiesFormat.ServiceEndpoints == nil {
		subnet.SubnetPropertiesFormat.ServiceEndpoints = &[]network.ServiceEndpointPropertiesFormat{}
	}
	serviceEndpoints := *subnet.SubnetPropertiesFormat.ServiceEndpoints
	for _, v := range serviceEndpoints {
		if v.Service != nil && *v.Service == storageService {
			storageServiceExists = true
			klog.V(4).Infof("serviceEndpoint(%s) is already in subnet(%s)", storageService, subnetName)
			break
		}
	}

	if !storageServiceExists {
		serviceEndpoints = append(serviceEndpoints, storageServiceEndpoint)
		subnet.SubnetPropertiesFormat.ServiceEndpoints = &serviceEndpoints

		if err := d.cloud.SubnetsClient.CreateOrUpdate(ctx, vnetResourceGroup, vnetName, subnetName, subnet); err != nil {
			return fmt.Errorf("failed to update the subnet %s under vnet %s: %v", subnetName, vnetName, err)
		}
		klog.V(2).Infof("serviceEndpoint(%s) is appended in subnet(%s)", storageService, subnetName)
	}

	return nil
}
