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

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"

	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"
)

func init() {
	klog.InitFlags(nil)
}

var (
	endpoint                               = flag.String("endpoint", "unix://tmp/csi.sock", "CSI endpoint")
	nodeID                                 = flag.String("nodeid", "", "node id")
	version                                = flag.Bool("version", false, "Print the version and exit.")
	metricsAddress                         = flag.String("metrics-address", "", "export the metrics")
	kubeconfig                             = flag.String("kubeconfig", "", "Absolute path to the kubeconfig file. Required only when running out of cluster.")
	driverName                             = flag.String("drivername", azurefile.DefaultDriverName, "name of the driver")
	cloudConfigSecretName                  = flag.String("cloud-config-secret-name", "azure-cloud-provider", "secret name of cloud config")
	cloudConfigSecretNamespace             = flag.String("cloud-config-secret-namespace", "kube-system", "secret namespace of cloud config")
	customUserAgent                        = flag.String("custom-user-agent", "", "custom userAgent")
	userAgentSuffix                        = flag.String("user-agent-suffix", "", "userAgent suffix")
	allowEmptyCloudConfig                  = flag.Bool("allow-empty-cloud-config", true, "allow running driver without cloud config")
	enableVolumeMountGroup                 = flag.Bool("enable-volume-mount-group", true, "indicates whether enabling VOLUME_MOUNT_GROUP")
	enableGetVolumeStats                   = flag.Bool("enable-get-volume-stats", true, "allow GET_VOLUME_STATS on agent node")
	mountPermissions                       = flag.Uint64("mount-permissions", 0777, "mounted folder permissions")
	allowInlineVolumeKeyAccessWithIdentity = flag.Bool("allow-inline-volume-key-access-with-identity", false, "allow accessing storage account key using cluster identity for inline volume")
	fsGroupChangePolicy                    = flag.String("fsgroup-change-policy", "", "indicates how the volume's ownership will be changed by the driver, OnRootMismatch is the default value")
	enableVHDDiskFeature                   = flag.Bool("enable-vhd", true, "enable VHD disk feature (experimental)")
	kubeAPIQPS                             = flag.Float64("kube-api-qps", 25.0, "QPS to use while communicating with the kubernetes apiserver.")
	kubeAPIBurst                           = flag.Int("kube-api-burst", 50, "Burst to use while communicating with the kubernetes apiserver.")
	appendMountErrorHelpLink               = flag.Bool("append-mount-error-help-link", true, "Whether to include a link for help with mount errors when a mount error occurs.")
	enableWindowsHostProcess               = flag.Bool("enable-windows-host-process", false, "enable windows host process")
	appendClosetimeoOption                 = flag.Bool("append-closetimeo-option", false, "Whether appending closetimeo=0 option to smb mount command")
	appendNoShareSockOption                = flag.Bool("append-nosharesock-option", true, "Whether appending nosharesock option to smb mount command")
)

func main() {
	flag.Parse()
	if *version {
		info, err := azurefile.GetVersionYAML(*driverName)
		if err != nil {
			klog.Fatalln(err)
		}
		fmt.Println(info) // nolint
		os.Exit(0)
	}

	if *nodeID == "" {
		// nodeid is not needed in controller component
		klog.Warning("nodeid is empty")
	}

	exportMetrics()
	handle()
	os.Exit(0)
}

func handle() {
	driverOptions := azurefile.DriverOptions{
		NodeID:                                 *nodeID,
		DriverName:                             *driverName,
		CloudConfigSecretName:                  *cloudConfigSecretName,
		CloudConfigSecretNamespace:             *cloudConfigSecretNamespace,
		CustomUserAgent:                        *customUserAgent,
		UserAgentSuffix:                        *userAgentSuffix,
		AllowEmptyCloudConfig:                  *allowEmptyCloudConfig,
		EnableVolumeMountGroup:                 *enableVolumeMountGroup,
		EnableGetVolumeStats:                   *enableGetVolumeStats,
		MountPermissions:                       *mountPermissions,
		AllowInlineVolumeKeyAccessWithIdentity: *allowInlineVolumeKeyAccessWithIdentity,
		FSGroupChangePolicy:                    *fsGroupChangePolicy,
		EnableVHDDiskFeature:                   *enableVHDDiskFeature,
		AppendMountErrorHelpLink:               *appendMountErrorHelpLink,
		KubeAPIQPS:                             *kubeAPIQPS,
		KubeAPIBurst:                           *kubeAPIBurst,
		EnableWindowsHostProcess:               *enableWindowsHostProcess,
		AppendClosetimeoOption:                 *appendClosetimeoOption,
		AppendNoShareSockOption:                *appendNoShareSockOption,
	}
	driver := azurefile.NewDriver(&driverOptions)
	if driver == nil {
		klog.Fatalln("Failed to initialize azurefile CSI Driver")
	}
	driver.Run(*endpoint, *kubeconfig, false)
}

func exportMetrics() {
	if *metricsAddress == "" {
		return
	}
	l, err := net.Listen("tcp", *metricsAddress)
	if err != nil {
		klog.Warningf("failed to get listener for metrics endpoint: %v", err)
		return
	}
	serve(context.Background(), l, serveMetrics)
}

func serve(ctx context.Context, l net.Listener, serveFunc func(net.Listener) error) {
	path := l.Addr().String()
	klog.V(2).Infof("set up prometheus server on %v", path)
	go func() {
		defer l.Close()
		if err := serveFunc(l); err != nil {
			klog.Fatalf("serve failure(%v), address(%v)", err, path)
		}
	}()
}

func serveMetrics(l net.Listener) error {
	m := http.NewServeMux()
	m.Handle("/metrics", legacyregistry.Handler()) //nolint, because azure cloud provider uses legacyregistry currently
	return trapClosedConnErr(http.Serve(l, m))
}

func trapClosedConnErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "use of closed network connection") {
		return nil
	}
	return err
}
