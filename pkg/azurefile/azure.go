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
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2022-07-01/network"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/azurefile-csi-driver/pkg/filewatcher"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/configloader"
	azcache "sigs.k8s.io/cloud-provider-azure/pkg/cache"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
	azureconfig "sigs.k8s.io/cloud-provider-azure/pkg/provider/config"
	"sigs.k8s.io/cloud-provider-azure/pkg/retry"
)

const (
	DefaultAzureCredentialFileEnv = "AZURE_CREDENTIAL_FILE"
	DefaultCredFilePathLinux      = "/etc/kubernetes/azure.json"
	DefaultCredFilePathWindows    = "C:\\k\\azure.json"
)

var (
	storageService = "Microsoft.Storage"
)

func getRuntimeClassForPod(ctx context.Context, kubeClient clientset.Interface, podName string, podNameSpace string) (string, error) {
	if kubeClient == nil {
		return "", fmt.Errorf("kubeClient is nil")
	}
	// Get runtime class for pod
	pod, err := kubeClient.CoreV1().Pods(podNameSpace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return ptr.Deref(pod.Spec.RuntimeClassName, ""), nil
}

// getCloudProvider get Azure Cloud Provider
func getCloudProvider(ctx context.Context, kubeClient kubernetes.Interface, nodeID, secretName, secretNamespace, userAgent string, allowEmptyCloudConfig bool) (*azure.Cloud, error) {
	var (
		config     *azureconfig.Config
		fromSecret bool
	)

	az := &azure.Cloud{}
	var err error

	if kubeClient != nil {
		klog.V(2).Infof("reading cloud config from secret %s/%s", secretNamespace, secretName)
		config, err = configloader.Load[azureconfig.Config](ctx, &configloader.K8sSecretLoaderConfig{
			K8sSecretConfig: configloader.K8sSecretConfig{
				SecretName:      secretName,
				SecretNamespace: secretNamespace,
				CloudConfigKey:  "cloud-config",
			},
			KubeClient: kubeClient,
		}, nil)
		if err != nil {
			klog.V(2).Infof("InitializeCloudFromSecret: failed to get cloud config from secret %s/%s: %v", secretNamespace, secretName, err)
		}
		if err == nil && config != nil {
			fromSecret = true
		}
	}

	if config == nil {
		klog.V(2).Infof("could not read cloud config from secret %s/%s", secretNamespace, secretName)
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
		config, err = configloader.Load[azureconfig.Config](ctx, nil, &configloader.FileLoaderConfig{
			FilePath: credFile,
		})
		if err != nil {
			klog.V(2).Infof("InitializeCloudFromSecret: failed to get cloud config from file %s: %v", credFile, err)
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
		// these environment variables are injected by workload identity webhook
		if tenantID := os.Getenv("AZURE_TENANT_ID"); tenantID != "" {
			config.TenantID = tenantID
		}
		if clientID := os.Getenv("AZURE_CLIENT_ID"); clientID != "" {
			config.AADClientID = clientID
		}
		if federatedTokenFile := os.Getenv("AZURE_FEDERATED_TOKEN_FILE"); federatedTokenFile != "" {
			config.AADFederatedTokenFile = federatedTokenFile
			config.UseFederatedWorkloadIdentityExtension = true
		}
		if len(config.AADClientCertPath) > 0 {
			// Watch the certificate for changes; if the certificate changes, the pod will be restarted
			err = filewatcher.WatchFileForChanges(config.AADClientCertPath)
			klog.Warningf("Failed to watch certificate file for changes: %v", err)
		}
		if err = az.InitializeCloudFromConfig(ctx, config, fromSecret, false); err != nil {
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

func getKubeConfig(kubeconfig string, enableWindowsHostProcess bool) (config *rest.Config, err error) {
	if kubeconfig != "" {
		if config, err = clientcmd.BuildConfigFromFlags("", kubeconfig); err != nil {
			return nil, err
		}
	} else {
		if config, err = inClusterConfig(enableWindowsHostProcess); err != nil {
			return nil, err
		}
	}
	return config, err
}

func (d *Driver) updateSubnetServiceEndpoints(ctx context.Context, vnetResourceGroup, vnetName, subnetName string) ([]string, error) {
	var vnetResourceIDs []string
	if d.cloud.SubnetsClient == nil {
		return vnetResourceIDs, fmt.Errorf("SubnetsClient is nil")
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

	klog.V(2).Infof("updateSubnetServiceEndpoints on vnetName: %s, subnetName: %s, location: %s", vnetName, subnetName, location)
	if vnetName == "" || location == "" {
		return vnetResourceIDs, fmt.Errorf("vnetName or location is empty")
	}

	lockKey := vnetResourceGroup + vnetName + subnetName
	cache, err := d.subnetCache.Get(ctx, lockKey, azcache.CacheReadTypeDefault)
	if err != nil {
		return nil, err
	}
	if cache != nil {
		vnetResourceIDs = cache.([]string)
		klog.V(2).Infof("subnet %s under vnet %s in rg %s is already updated, vnetResourceIDs: %v", subnetName, vnetName, vnetResourceGroup, vnetResourceIDs)
		return vnetResourceIDs, nil
	}

	d.subnetLockMap.LockEntry(lockKey)
	defer d.subnetLockMap.UnlockEntry(lockKey)

	var subnets []network.Subnet
	if subnetName != "" {
		// list multiple subnets separated by comma
		subnetNames := strings.Split(subnetName, ",")
		for _, sn := range subnetNames {
			sn = strings.TrimSpace(sn)
			subnet, rerr := d.cloud.SubnetsClient.Get(ctx, vnetResourceGroup, vnetName, sn, "")
			if rerr != nil {
				return vnetResourceIDs, fmt.Errorf("failed to get the subnet %s under rg %s vnet %s: %v", subnetName, vnetResourceGroup, vnetName, rerr.Error())
			}
			subnets = append(subnets, subnet)
		}
	} else {
		var rerr *retry.Error
		subnets, rerr = d.cloud.SubnetsClient.List(ctx, vnetResourceGroup, vnetName)
		if rerr != nil {
			return vnetResourceIDs, fmt.Errorf("failed to list the subnets under rg %s vnet %s: %v", vnetResourceGroup, vnetName, rerr.Error())
		}
	}

	for _, subnet := range subnets {
		if subnet.Name == nil {
			return vnetResourceIDs, fmt.Errorf("subnet name is nil")
		}
		sn := *subnet.Name
		vnetResourceID := d.getSubnetResourceID(vnetResourceGroup, vnetName, sn)
		klog.V(2).Infof("set vnetResourceID %s", vnetResourceID)
		vnetResourceIDs = append(vnetResourceIDs, vnetResourceID)

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
			if strings.HasPrefix(ptr.Deref(v.Service, ""), storageService) {
				storageServiceExists = true
				klog.V(4).Infof("serviceEndpoint(%s) is already in subnet(%s)", storageService, sn)
				break
			}
		}

		if !storageServiceExists {
			serviceEndpoints = append(serviceEndpoints, storageServiceEndpoint)
			subnet.SubnetPropertiesFormat.ServiceEndpoints = &serviceEndpoints

			klog.V(2).Infof("begin to update the subnet %s under vnet %s in rg %s", sn, vnetName, vnetResourceGroup)
			if err := d.cloud.SubnetsClient.CreateOrUpdate(ctx, vnetResourceGroup, vnetName, sn, subnet); err != nil {
				return vnetResourceIDs, fmt.Errorf("failed to update the subnet %s under vnet %s: %v", sn, vnetName, err)
			}
		}
	}
	// cache the subnet update
	d.subnetCache.Set(lockKey, vnetResourceIDs)
	return vnetResourceIDs, nil
}

// inClusterConfig is copied from https://github.com/kubernetes/client-go/blob/b46677097d03b964eab2d67ffbb022403996f4d4/rest/config.go#L507-L541
// When using Windows HostProcess containers, the path "/var/run/secrets/kubernetes.io/serviceaccount/" is under host, not container.
// Then the token and ca.crt files would be not found.
// An environment variable $CONTAINER_SANDBOX_MOUNT_POINT is set upon container creation and provides the absolute host path to the container volume.
// See https://kubernetes.io/docs/tasks/configure-pod-container/create-hostprocess-pod/#volume-mounts for more details.
func inClusterConfig(enableWindowsHostProcess bool) (*rest.Config, error) {
	var (
		tokenFile  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
		rootCAFile = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	)
	if enableWindowsHostProcess {
		containerSandboxMountPath := os.Getenv("CONTAINER_SANDBOX_MOUNT_POINT")
		if len(containerSandboxMountPath) == 0 {
			return nil, errors.New("unable to load in-cluster configuration, containerSandboxMountPath must be defined")
		}
		tokenFile = filepath.Join(containerSandboxMountPath, tokenFile)
		rootCAFile = filepath.Join(containerSandboxMountPath, rootCAFile)
	}

	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if len(host) == 0 || len(port) == 0 {
		return nil, rest.ErrNotInCluster
	}

	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, err
	}

	tlsClientConfig := rest.TLSClientConfig{}

	if _, err := certutil.NewPool(rootCAFile); err != nil {
		klog.Errorf("Expected to load root CA config from %s, but got err: %v", rootCAFile, err)
	} else {
		tlsClientConfig.CAFile = rootCAFile
	}

	return &rest.Config{
		Host:            "https://" + net.JoinHostPort(host, port),
		TLSClientConfig: tlsClientConfig,
		BearerToken:     string(token),
		BearerTokenFile: tokenFile,
	}, nil
}
