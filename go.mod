module sigs.k8s.io/azurefile-csi-driver

go 1.16

require (
	github.com/Azure/azure-sdk-for-go v53.1.0+incompatible
	github.com/Azure/azure-storage-file-go v0.8.0
	github.com/Azure/go-autorest/autorest v0.11.17
	github.com/Azure/go-autorest/autorest/adal v0.9.10
	github.com/Azure/go-autorest/autorest/to v0.3.0
	github.com/container-storage-interface/spec v1.3.0
	github.com/golang/mock v1.4.4
	github.com/golang/protobuf v1.4.3
	github.com/kubernetes-csi/csi-lib-utils v0.7.0
	github.com/kubernetes-csi/csi-proxy/client v0.2.2
	github.com/kubernetes-csi/external-snapshotter/v2 v2.0.0-20200617021606-4800ca72d403
	github.com/onsi/ginkgo v1.12.0
	github.com/onsi/gomega v1.9.0
	github.com/pborman/uuid v1.2.0
	github.com/pelletier/go-toml v1.7.0
	github.com/rubiojr/go-vhd v0.0.0-20200706105327-02e210299021
	github.com/stretchr/testify v1.6.1
	golang.org/x/net v0.0.0-20210224082022-3d97a244fca7
	google.golang.org/grpc v1.28.0
	k8s.io/api v0.21.0
	k8s.io/apimachinery v0.22.0-alpha.0.0.20210417144234-8daf28983e6e
	k8s.io/client-go v0.21.0
	k8s.io/cloud-provider v0.21.0
	k8s.io/component-base v0.21.0
	k8s.io/klog/v2 v2.8.0
	k8s.io/kubernetes v1.21.0
	k8s.io/mount-utils v0.0.0
	k8s.io/utils v0.0.0-20201110183641-67b214c5f920
	sigs.k8s.io/cloud-provider-azure v0.7.4
	sigs.k8s.io/yaml v1.2.0
)

replace (
	github.com/container-storage-interface/spec => github.com/container-storage-interface/spec v1.3.0
	github.com/niemeyer/pretty => github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e
	github.com/prometheus/client_golang => github.com/prometheus/client_golang v1.7.1
	go.etcd.io/etcd => go.etcd.io/etcd v0.0.0-20200410171415-59f5fb25a533
	google.golang.org/grpc => google.golang.org/grpc v1.27.0
	k8s.io/api => k8s.io/api v0.21.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.21.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.21.0
	k8s.io/apiserver => k8s.io/apiserver v0.21.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.21.0
	k8s.io/client-go => k8s.io/client-go v0.21.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.21.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.21.0
	k8s.io/code-generator => k8s.io/code-generator v0.21.0
	k8s.io/component-base => k8s.io/component-base v0.21.0
	k8s.io/component-helpers => k8s.io/component-helpers v0.21.0
	k8s.io/controller-manager => k8s.io/controller-manager v0.21.0
	k8s.io/cri-api => k8s.io/cri-api v0.21.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.21.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.21.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.21.0
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20201113171705-d219536bb9fd
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.21.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.21.0
	k8s.io/kubectl => k8s.io/kubectl v0.21.0
	k8s.io/kubelet => k8s.io/kubelet v0.21.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.21.0
	k8s.io/metrics => k8s.io/metrics v0.21.0
	k8s.io/mount-utils => k8s.io/mount-utils v0.21.1
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.21.0
	k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.21.0
	k8s.io/sample-controller => k8s.io/sample-controller v0.21.0
	sigs.k8s.io/azurefile-csi-driver => ./
	sigs.k8s.io/cloud-provider-azure => sigs.k8s.io/cloud-provider-azure v0.7.1-0.20210428063649-9c3d7ab4b44f
)
