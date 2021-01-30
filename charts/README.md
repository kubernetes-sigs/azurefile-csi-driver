# Install CSI driver with Helm 3

## Prerequisites

 - [install Helm](https://helm.sh/docs/intro/quickstart/#install-helm)

## install latest version

```console
helm repo add azurefile-csi-driver https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/charts
helm install azurefile-csi-driver azurefile-csi-driver/azurefile-csi-driver --namespace kube-system
```

### install a specific version

```console
helm repo add azurefile-csi-driver https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/charts
helm install azurefile-csi-driver azurefile-csi-driver/azurefile-csi-driver --namespace kube-system --version v0.10.0
```

### search for all available chart versions
```console
helm search repo -l azurefile-csi-driver
```

## uninstall CSI driver

```console
helm uninstall azurefile-csi-driver -n kube-system
```

## latest chart configuration

The following table lists the configurable parameters of the latest Azure File CSI Driver chart and default values.

| Parameter                                         | Description                                                | Default                                                           |
|---------------------------------------------------|------------------------------------------------------------|-------------------------------------------------------------------|
| `image.azurefile.repository`                      | azurefile-csi-driver docker image                          | mcr.microsoft.com/k8s/csi/azurefile-csi                            |
| `image.azurefile.tag`                             | azurefile-csi-driver docker image tag                      | latest                                                            |
| `image.azurefile.pullPolicy`                      | azurefile-csi-driver image pull policy                     | IfNotPresent                                                      |
| `image.csiProvisioner.repository`                 | csi-provisioner docker image                               | mcr.microsoft.com/oss/kubernetes-csi/csi-provisioner              |
| `image.csiProvisioner.tag`                        | csi-provisioner docker image tag                           | v1.4.0                                                            |
| `image.csiProvisioner.pullPolicy`                 | csi-provisioner image pull policy                          | IfNotPresent                                                      |
| `image.csiAttacher.repository`                    | csi-attacher docker image                                  | mcr.microsoft.com/oss/kubernetes-csi/csi-attacher                 |
| `image.csiAttacher.tag`                           | csi-attacher docker image tag                              | v1.2.0                                                            |
| `image.csiAttacher.pullPolicy`                    | csi-attacher image pull policy                             | IfNotPresent                                                      |
| `image.csiSnapshotter.repository`                 | csi-snapshotter docker image                               | mcr.microsoft.com/oss/kubernetes-csi/csi-snapshotter              |
| `image.csiSnapshotter.tag`                        | csi-snapshotter docker image tag                           | v1.1.0                                                            |
| `image.csiSnapshotter.pullPolicy`                 | csi-snapshotter image pull policy                          | IfNotPresent                                                      |
| `image.csiResizer.repository`                     | csi-resizer docker image                                   | mcr.microsoft.com/oss/kubernetes-csi/csi-resizer                  |
| `image.csiResizer.tag`                            | csi-resizer docker image tag                               | v0.3.0                                                            |
| `image.csiResizer.pullPolicy`                     | csi-resizer image pull policy                              | IfNotPresent                                                      |
| `image.livenessProbe.repository`                  | liveness-probe docker image                                | mcr.microsoft.com/oss/kubernetes-csi/livenessprobe                |
| `image.livenessProbe.tag`                         | liveness-probe docker image tag                            | v1.1.0                                                            |
| `image.livenessProbe.pullPolicy`                  | liveness-probe image pull policy                           | IfNotPresent                                                      |
| `image.nodeDriverRegistrar.repository`            | csi-node-driver-registrar docker image                     | mcr.microsoft.com/oss/kubernetes-csi/csi-node-driver-registrar    |
| `image.nodeDriverRegistrar.tag`                   | csi-node-driver-registrar docker image tag                 | v2.0.1                                                            |
| `image.nodeDriverRegistrar.pullPolicy`            | csi-node-driver-registrar image pull policy                | IfNotPresent                                                      |
| `imagePullSecrets`                                | Specify docker-registry secret names as an array           | [] (does not add image pull secrets to deployed pods)             | 
| `serviceAccount.create`                           | whether create service account of csi-azurefile-controller  | true                                                              |
| `rbac.create`                                     | whether create rbac of csi-azurefile-controller             | true                                                              |
| `controller.replicas`                             | the replicas of csi-azurefile-controller                    | 2                                                                 |
| `controller.metricsPort`                          | metrics port of csi-azurefile-controller                   |29614                                                        |
| `controller.runOnMaster`                          | run controller on master node                                                          |
`false`                                                           |
| `node.metricsPort`                                | metrics port of csi-azurefile-node                         |29615                                                        |
| `snapshot.enabled`                                | whether enable snapshot feature                            | false                                                        |
| `snapshot.image.csiSnapshotter.repository`        | csi-snapshotter docker image                               | mcr.microsoft.com/oss/kubernetes-csi/csi-snapshotter         |
| `snapshot.image.csiSnapshotter.tag`               | csi-snapshotter docker image tag                           | v2.0.1                                                       |
| `snapshot.image.csiSnapshotter.pullPolicy`        | csi-snapshotter image pull policy                          | IfNotPresent                                                 |
| `snapshot.image.csiSnapshotController.repository` | snapshot-controller docker image                           | mcr.microsoft.com/oss/kubernetes-csi/snapshot-controller     |
| `snapshot.image.csiSnapshotController.tag`        | snapshot-controller docker image tag                       | v2.1.1                                                       |
| `snapshot.image.csiSnapshotController.pullPolicy` | snapshot-controller image pull policy                      | IfNotPresent                                                 |
| `snapshot.snapshotController.replicas`            | the replicas of snapshot-controller                        | 1                                                            |
| `snapshot.snapshotController.serviceAccount`      | whether create service account of snapshot-controller      | true                                                         |
| `snapshot.snapshotController.rbac`                | whether create rbac of snapshot-controller                 | true                                                         |
| `linux.enabled`                                   | whether enable linux feature                               | true                                                              |
| `windows.enabled`                                 | whether enable windows feature                             | true                                                             |
| `windows.image.livenessProbe.repository`          | windows liveness-probe docker image                        | mcr.microsoft.com/oss/kubernetes-csi/livenessprobe                |
| `windows.image.livenessProbe.tag`                 | windows liveness-probe docker image tag                    | v2.0.1-alpha.1-windows-1809-amd64                                 |
| `windows.image.livenessProbe.pullPolicy`          | windows liveness-probe image pull policy                   | IfNotPresent                                                      |
| `windows.image.nodeDriverRegistrar.repository`    | windows csi-node-driver-registrar docker image             | mcr.microsoft.com/oss/kubernetes-csi/csi-node-driver-registrar    |
| `windows.image.nodeDriverRegistrar.tag`           | windows csi-node-driver-registrar docker image tag         | v2.0.1                                 |
| `windows.image.nodeDriverRegistrar.pullPolicy`    | windows csi-node-driver-registrar image pull policy        | IfNotPresent                                                      |
| `kubelet.linuxPath`                               | configure the kubelet path for Linux node                  | `/var/lib/kubelet`                                                |
| `kubelet.windowsPath`                             | configure the kubelet path for Windows node                | `'C:\var\lib\kubelet'`                                            |

## troubleshooting
 - Add `--wait -v=5 --debug` in `helm install` command to get detailed error
 - Use `kubectl describe` to acquire more info
