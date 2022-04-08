## Install azurefile CSI driver v1.12.0 version on a Kubernetes cluster

### Install by kubectl
 - Option#1. remote install
```console
curl -skSL https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/v1.12.0/deploy/install-driver.sh | bash -s v1.12.0 --
```

 - Option#2. local install
```console
git clone https://github.com/kubernetes-sigs/azurefile-csi-driver.git
cd azurefile-csi-driver
git checkout v1.12.0
./deploy/install-driver.sh v1.12.0 local
```

 - check pods status:
```console
kubectl -n kube-system get pod -o wide --watch -l app=csi-azurefile-controller
kubectl -n kube-system get pod -o wide --watch -l app=csi-azurefile-node
```

example output:

```
NAME                                            READY   STATUS    RESTARTS   AGE     IP             NODE
csi-azurefile-controller-56bfddd689-dh5tk       6/6     Running   0          35s     10.240.0.19    k8s-agentpool-22533604-0
csi-azurefile-node-cvgbs                        3/3     Running   0          7m4s    10.240.0.35    k8s-agentpool-22533604-1
csi-azurefile-node-dr4s4                        3/3     Running   0          7m4s    10.240.0.4     k8s-agentpool-22533604-0
```


### clean up Azure File CSI driver
 - Option#1. remote uninstall
```console
curl -skSL https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/v1.12.0/deploy/uninstall-driver.sh | bash -s --
```

 - Option#2. local uninstall
```console
git clone https://github.com/kubernetes-sigs/azurefile-csi-driver.git
cd azurefile-csi-driver
git checkout v1.12.0
./deploy/install-driver.sh v1.12.0 local
```
