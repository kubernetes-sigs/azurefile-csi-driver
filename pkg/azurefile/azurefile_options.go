/*
Copyright 2019 The Kubernetes Authors.

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

import "flag"

// DriverOptions defines driver parameters specified in driver deployment
type DriverOptions struct {
	NodeID                                 string
	DriverName                             string
	CloudConfigSecretName                  string
	CloudConfigSecretNamespace             string
	CustomUserAgent                        string
	UserAgentSuffix                        string
	AllowEmptyCloudConfig                  bool
	AllowInlineVolumeKeyAccessWithIdentity bool
	EnableVHDDiskFeature                   bool
	EnableVolumeMountGroup                 bool
	EnableGetVolumeStats                   bool
	AppendMountErrorHelpLink               bool
	MountPermissions                       uint64
	FSGroupChangePolicy                    string
	KubeAPIQPS                             float64
	KubeAPIBurst                           int
	EnableWindowsHostProcess               bool
	RemoveSMBMountOnWindows                bool
	AppendClosetimeoOption                 bool
	AppendNoShareSockOption                bool
	AppendNoResvPortOption                 bool
	AppendActimeoOption                    bool
	SkipMatchingTagCacheExpireInMinutes    int
	VolStatsCacheExpireInMinutes           int
	PrintVolumeStatsCallLogs               bool
	SasTokenExpirationMinutes              int
	WaitForAzCopyTimeoutMinutes            int
	KubeConfig                             string
	Endpoint                               string
}

func (o *DriverOptions) AddFlags() *flag.FlagSet {
	if o == nil {
		return nil
	}
	fs := flag.NewFlagSet("", flag.ExitOnError)
	fs.StringVar(&o.NodeID, "nodeid", "", "node id")
	fs.StringVar(&o.DriverName, "drivername", DefaultDriverName, "name of the driver")
	fs.StringVar(&o.CloudConfigSecretName, "cloud-config-secret-name", "azure-cloud-provider", "secret name of cloud config")
	fs.StringVar(&o.CloudConfigSecretNamespace, "cloud-config-secret-namespace", "kube-system", "secret namespace of cloud config")
	fs.StringVar(&o.CustomUserAgent, "custom-user-agent", "", "custom userAgent")
	fs.StringVar(&o.UserAgentSuffix, "user-agent-suffix", "", "userAgent suffix")
	fs.BoolVar(&o.AllowEmptyCloudConfig, "allow-empty-cloud-config", true, "allow running driver without cloud config")
	fs.BoolVar(&o.AllowInlineVolumeKeyAccessWithIdentity, "allow-inline-volume-key-access-with-identity", false, "allow accessing storage account key using cluster identity for inline volume")
	fs.BoolVar(&o.EnableVHDDiskFeature, "enable-vhd", true, "enable VHD disk feature (experimental)")
	fs.BoolVar(&o.EnableVolumeMountGroup, "enable-volume-mount-group", true, "indicates whether enabling VOLUME_MOUNT_GROUP")
	fs.BoolVar(&o.EnableGetVolumeStats, "enable-get-volume-stats", true, "allow GET_VOLUME_STATS on agent node")
	fs.BoolVar(&o.AppendMountErrorHelpLink, "append-mount-error-help-link", true, "Whether to include a link for help with mount errors when a mount error occurs.")
	fs.Uint64Var(&o.MountPermissions, "mount-permissions", 0777, "mounted folder permissions")
	fs.StringVar(&o.FSGroupChangePolicy, "fsgroup-change-policy", "", "indicates how the volume's ownership will be changed by the driver, OnRootMismatch is the default value")
	fs.Float64Var(&o.KubeAPIQPS, "kube-api-qps", 25.0, "QPS to use while communicating with the kubernetes apiserver.")
	fs.IntVar(&o.KubeAPIBurst, "kube-api-burst", 50, "Burst to use while communicating with the kubernetes apiserver.")
	fs.BoolVar(&o.EnableWindowsHostProcess, "enable-windows-host-process", false, "enable windows host process")
	fs.BoolVar(&o.RemoveSMBMountOnWindows, "remove-smb-mount-on-windows", true, "remove smb global mapping on windows during unmount")
	fs.BoolVar(&o.AppendClosetimeoOption, "append-closetimeo-option", false, "Whether appending closetimeo=0 option to smb mount command")
	fs.BoolVar(&o.AppendNoShareSockOption, "append-nosharesock-option", true, "Whether appending nosharesock option to smb mount command")
	fs.BoolVar(&o.AppendNoResvPortOption, "append-noresvport-option", true, "Whether appending noresvport option to nfs mount command")
	fs.BoolVar(&o.AppendActimeoOption, "append-actimeo-option", true, "Whether appending actimeo=0 option to nfs mount command")
	fs.IntVar(&o.SkipMatchingTagCacheExpireInMinutes, "skip-matching-tag-cache-expire-in-minutes", 30, "The cache expire time in minutes for skipMatchingTagCache")
	fs.IntVar(&o.VolStatsCacheExpireInMinutes, "vol-stats-cache-expire-in-minutes", 10, "The cache expire time in minutes for volume stats cache")
	fs.BoolVar(&o.PrintVolumeStatsCallLogs, "print-volume-stats-call-logs", false, "Whether to print volume statfs call logs with log level 2")
	fs.IntVar(&o.SasTokenExpirationMinutes, "sas-token-expiration-minutes", 1440, "sas token expiration minutes during volume cloning and snapshot restore")
	fs.IntVar(&o.WaitForAzCopyTimeoutMinutes, "wait-for-azcopy-timeout-minutes", 5, "timeout in minutes for waiting for azcopy to finish")
	fs.StringVar(&o.KubeConfig, "kubeconfig", "", "Absolute path to the kubeconfig file. Required only when running out of cluster.")
	fs.StringVar(&o.Endpoint, "endpoint", "unix://tmp/csi.sock", "CSI endpoint")

	return fs
}
