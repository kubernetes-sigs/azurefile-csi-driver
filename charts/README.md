# Install CSI driver with Helm 3

## Prerequisites
 - [install Helm](https://helm.sh/docs/intro/quickstart/#install-helm)

### Tips
 - make controller only run on control plane node: `--set controller.runOnControlPlane=true`
 - set replica of controller as `1`: `--set controller.replicas=1` (only applied for NFS protocol)
 - specify different cloud config secret for the driver:
   - `--set controller.cloudConfigSecretName`
   - `--set controller.cloudConfigSecretNamesapce`
   - `--set node.cloudConfigSecretName`
   - `--set node.cloudConfigSecretNamesapce`
 - switch to `mcr.azk8s.cn` repository in Azure China: `--set image.baseRepo=mcr.azk8s.cn`

### install a specific version
```console
helm repo add azurefile-csi-driver https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/charts
helm install azurefile-csi-driver azurefile-csi-driver/azurefile-csi-driver --namespace kube-system --version v1.30.2
```

### install on RedHat/CentOS
```console
helm install azurefile-csi-driver azurefile-csi-driver/azurefile-csi-driver --namespace kube-system --set linux.distro=fedora
```

### install driver with customized driver name, deployment name
> only supported from `v1.5.0`+
 - following example would install a driver with name `file2`
```console
helm install azurefile2-csi-driver azurefile-csi-driver/azurefile-csi-driver --namespace kube-system --set driver.name="file2.csi.azure.com" --set controller.name="csi-azurefile2-controller" --set rbac.name=azurefile2 --set serviceAccount.controller=csi-azurefile2-controller-sa --set serviceAccount.node=csi-azurefile2-node-sa --set linux.dsName=csi-azurefile2-node --set windows.dsName=csi-azurefile2-node-win --set node.livenessProbe.healthPort=39613
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
| `azureCredentialFileConfigMap`                    | alternative ConfigMap name for the credentials file        | `azure-cred-file` |
| `driver.name`                                     | alternative driver name                                    | `file.csi.azure.com` |
| `driver.customUserAgent`                          | custom userAgent               | `` |
| `driver.userAgentSuffix`                          | userAgent suffix               | `OSS-helm` |
| `driver.azureGoSDKLogLevel`                       | [Azure go sdk log level](https://github.com/Azure/azure-sdk-for-go/blob/main/documentation/previous-versions-quickstart.md#built-in-basic-requestresponse-logging)  | ``(no logs), `DEBUG`, `INFO`, `WARNING`, `ERROR`, [etc](https://github.com/Azure/go-autorest/blob/50e09bb39af124f28f29ba60efde3fa74a4fe93f/logger/logger.go#L65-L73). |
| `feature.enableGetVolumeStats`                    | allow GET_VOLUME_STATS on agent node                       | `true`                      |
| `feature.enableVolumeMountGroup`                  | indicates whether enabling VOLUME_MOUNT_GROUP                       | `true`                      |
| `feature.fsGroupPolicy`                           | CSIDriver FSGroupPolicy value                  | `ReadWriteOnceWithFSType`(available values: `ReadWriteOnceWithFSType`, `File`, `None`) |
| `image.baseRepo`                                  | base repository of driver images                           | `mcr.microsoft.com`                      |
| `image.azurefile.repository`                      | azurefile-csi-driver docker image                          | `/oss/kubernetes-csi/azurefile-csi`                            |
| `image.azurefile.tag`                             | azurefile-csi-driver docker image tag                      | ``                                                            |
| `image.azurefile.pullPolicy`                      | azurefile-csi-driver image pull policy                     | `IfNotPresent`                                                      |
| `image.csiProvisioner.repository`                 | csi-provisioner docker image                               | `/oss/kubernetes-csi/csi-provisioner`              |
| `image.csiProvisioner.tag`                        | csi-provisioner docker image tag                           | `v4.0.0`                                                            |
| `image.csiProvisioner.pullPolicy`                 | csi-provisioner image pull policy                          | `IfNotPresent`                                                      |
| `image.csiResizer.repository`                     | csi-resizer docker image                                   | `/oss/kubernetes-csi/csi-resizer`                  |
| `image.csiResizer.tag`                            | csi-resizer docker image tag                               | `v1.9.3`                                                            |
| `image.csiResizer.pullPolicy`                     | csi-resizer image pull policy                              | `IfNotPresent`                                                      |
| `image.livenessProbe.repository`                  | liveness-probe docker image                                | `/oss/kubernetes-csi/livenessprobe`                |
| `image.livenessProbe.tag`                         | liveness-probe docker image tag                            | `v2.12.0`                                                            |
| `image.livenessProbe.pullPolicy`                  | liveness-probe image pull policy                           | `IfNotPresent`                                                      |
| `image.nodeDriverRegistrar.repository`            | csi-node-driver-registrar docker image                     | `/oss/kubernetes-csi/csi-node-driver-registrar`    |
| `image.nodeDriverRegistrar.tag`                   | csi-node-driver-registrar docker image tag                 | `v2.10.0`                                                            |
| `image.nodeDriverRegistrar.pullPolicy`            | csi-node-driver-registrar image pull policy                | `IfNotPresent`                                                      |
| `imagePullSecrets`                                | Specify docker-registry secret names as an array           | [] (does not add image pull secrets to deployed pods)             |
| `customLabels`                                    | Custom labels to add into metadata                         | `{}`                                                                |
| `serviceAccount.create`                           | whether create service account of csi-azurefile-controller, csi-azurefile-node, and snapshot-controller| `true`                                                    |
| `serviceAccount.controller`                       | name of service account for csi-azurefile-controller       | `csi-azurefile-controller-sa`                                  |
| `serviceAccount.node`                             | name of service account for csi-azurefile-node             | `csi-azurefile-node-sa`                                        |
| `serviceAccount.snapshotController`               | name of service account for csi-snapshot-controller        | `csi-snapshot-controller-sa`                                   |
| `rbac.create`                                     | whether create rbac for this driver     | `true`                                                              |
| `rbac.name`                                       | driver name in rbac role                | `azurefile`                                                         |
| `controller.name`                                 | name of driver deployment                  | `csi-azurefile-controller`
| `controller.cloudConfigSecretName`                | cloud config secret name of controller driver               | `azure-cloud-provider`
| `controller.cloudConfigSecretNamespace`           | cloud config secret namespace of controller driver          | `kube-system`
| `controller.allowEmptyCloudConfig`                | Whether allow running controller driver without cloud config          | `true`
| `controller.replicas`                             | replicas of csi-azurefile-controller                    | `2`                                                                 |
| `controller.labels`                               | controller deployment extra labels                    | `{}`
| `controller.annotations`                          | controller deployment extra annotations               | `{}`
| `controller.podLabels`                            | controller pods extra labels                          | `{}`
| `controller.podAnnotations`                       | controller pods extra annotations                     | `{}`
| `controller.hostNetwork`                          | `hostNetwork` setting on controller driver(could be disabled if controller does not depend on MSI setting)                            | `true`                                                            | `true`, `false`
| `controller.metricsPort`                          | metrics port of csi-azurefile-controller                   |`29614`                                                        |
| `controller.livenessProbe.healthPort `            | health check port for liveness probe                   | `29612` |
| `controller.runOnMaster`                          | run controller on master node(deprecated on k8s 1.25+)                                                          |`false`                                                           |
| `controller.runOnControlPlane`                    | run controller on control plane node                                                          |`false`                                                           |
| `controller.attachRequired`                       | enable attach/detach (only valid for vhd disk feature)                                            |`false`                                                           |
| `controller.logLevel`                             | controller driver log level                                                          |`5`                                                           |
| `controller.resources.csiProvisioner.limits.memory`   | csi-provisioner memory limits                         | 500Mi                                                          |
| `controller.resources.csiProvisioner.requests.cpu`    | csi-provisioner cpu requests                   | 10m                                                            |
| `controller.resources.csiProvisioner.requests.memory` | csi-provisioner memory requests                | 20Mi                                                           |
| `controller.resources.csiAttacher.limits.memory`      | csi-attacher memory limits                         | 500Mi                                                          |
| `controller.resources.csiAttacher.requests.cpu`       | csi-attacher cpu requests                   | 10m                                                            |
| `controller.resources.csiAttacher.requests.memory`    | csi-attacher memory requests                | 20Mi                                                           |
| `controller.resources.csiResizer.limits.memory`       | csi-resizer memory limits                         | 500Mi                                                          |
| `controller.resources.csiResizer.requests.cpu`        | csi-resizer cpu requests                   | 10m                                                            |
| `controller.resources.csiResizer.requests.memory`     | csi-resizer memory requests                | 20Mi                                                           |
| `controller.resources.csiSnapshotter.limits.memory`   | csi-snapshotter memory limits                         | 500Mi                                                          |
| `controller.resources.csiSnapshotter.requests.cpu`    | csi-snapshotter cpu requests                   | 10m                                                            |
| `controller.resources.csiSnapshotter.requests.memory` | csi-snapshotter memory requests                | 20Mi                                                           |
| `controller.resources.livenessProbe.limits.memory`    | liveness-probe memory limits                          | 100Mi                                                          |
| `controller.resources.livenessProbe.requests.cpu`     | liveness-probe cpu requests                    | 10m                                                            |
| `controller.resources.livenessProbe.requests.memory`  | liveness-probe memory requests                 | 20Mi                                                           |
| `controller.resources.azurefile.limits.memory`        | azurefile memory limits                         | 200Mi                                                          |
| `controller.resources.azurefile.requests.cpu`         | azurefile cpu requests                   | 10m                                                            |
| `controller.resources.azurefile.requests.memory`      | azurefile memory requests                | 20Mi                                                           |
| `controller.kubeconfig`                               | configure kubeconfig path on controller node                | '' (empty, use InClusterConfig by default)
| `controller.tolerations`                              | controller pod tolerations                            |                                                              |
| `controller.affinity`                                 | controller pod affinity                               | `{}`                                                             |
| `controller.nodeSelector`                             | controller pod node selector                          | `{}`                                                             |
| `node.cloudConfigSecretName`                      | cloud config secret name of node driver               | `azure-cloud-provider`
| `node.cloudConfigSecretNamespace`                 | cloud config secret namespace of node driver          | `kube-system`
| `node.allowEmptyCloudConfig`                      | Whether allow running node driver without cloud config          | `true`
| `node.allowInlineVolumeKeyAccessWithIdentity`     | Whether allow accessing storage account key using cluster identity for inline volume          | `false`
| `node.maxUnavailable`                             | `maxUnavailable` value of driver node daemonset                            | `1`
| `node.livenessProbe.healthPort `                  | health check port for liveness probe                   | `29613` |
| `node.logLevel`                                   | node driver log level                                                          |`5`                                                           |
| `snapshot.enabled`                                | whether enable snapshot feature                            | `false`                                                        |
| `snapshot.image.csiSnapshotter.repository`        | csi-snapshotter docker image                               | `/oss/kubernetes-csi/csi-snapshotter`         |
| `snapshot.image.csiSnapshotter.tag`               | csi-snapshotter docker image tag                           | `v6.3.3`                                                       |
| `snapshot.image.csiSnapshotter.pullPolicy`        | csi-snapshotter image pull policy                          | `IfNotPresent`                                                 |
| `snapshot.image.csiSnapshotController.repository` | snapshot-controller docker image                           | `/oss/kubernetes-csi/snapshot-controller`     |
| `snapshot.image.csiSnapshotController.tag`        | snapshot-controller docker image tag                       | `v6.3.3`                                                       |
| `snapshot.image.csiSnapshotController.pullPolicy` | snapshot-controller image pull policy                      | `IfNotPresent`                                                 |
| `snapshot.snapshotController.name`                | snapshot controller name                                   | `csi-snapshot-controller`                                                           |
| `snapshot.snapshotController.replicas`            | the replicas of snapshot-controller                        | `2`                                                          |
| `snapshot.snapshotController.labels`                               | snapshot controller deployment extra labels                    | `{}`
| `snapshot.snapshotController.annotations`                          | snapshot controller deployment extra annotations               | `{}`
| `snapshot.snapshotController.podLabels`                            | snapshot controller pods extra labels                          | `{}`
| `snapshot.snapshotController.podAnnotations`                       | snapshot controller pods extra annotations                     | `{}`
| `snapshot.snapshotController.resources.limits.memory`          | csi-snapshot-controller memory limits                          | 100Mi                                                          |
| `snapshot.snapshotController.resources.requests.cpu`           | csi-snapshot-controller cpu requests                    | 10m                                                            |
| `snapshot.snapshotController.resources.requests.memory`        | csi-snapshot-controller memory requests                 | 20Mi                                                           |
| `linux.enabled`                                   | whether enable linux feature                               | `true`                                                              |
| `linux.dsName`                                    | name of driver daemonset on linux                             |`csi-azurefile-node`                                                         |
| `linux.dnsPolicy`                                 | dnsPolicy setting of driver daemonset on linux                             | `Default` (available values: `Default`, `ClusterFirst`, `ClusterFirstWithHostNet`, `None`)
| `linux.kubelet`                                   | configure kubelet directory path on Linux agent node node                  | `/var/lib/kubelet`                                                |
| `linux.kubeconfig`                                | configure kubeconfig path on Linux agent node                | '' (empty, use InClusterConfig by default)                                            |
| `linux.distro`                                    | configure ssl certificates for different Linux distribution(available values: `debian`, `fedora`)                  |
| `linux.mountPermissions`                          | mounted folder permissions                 | `0777`
| `linux.enableRegistrationProbe`                   | enable [kubelet-registration-probe](https://github.com/kubernetes-csi/node-driver-registrar#health-check-with-an-exec-probe) on Linux driver config     | `true`
| `linux.tolerations`                               | linux node driver tolerations                            |
| `linux.affinity`                                  | linux node pod affinity                                     | `{}`                                                             |
| `linux.nodeSelector`                              | linux node pod node selector                                | `{}`                                                             |
| `linux.labels`                                    | linux node daemonset extra labels                     | `{}`
| `linux.annotations`                               | linux node daemonset extra annotations                | `{}`
| `linux.podLabels`                                 | linux node pods extra labels                          | `{}`
| `linux.podAnnotations`                            | linux node pods extra annotations                     | `{}`
| `linux.resources.livenessProbe.limits.memory`          | liveness-probe memory limits                          | 100Mi                                                          |
| `linux.resources.livenessProbe.requests.cpu`           | liveness-probe cpu requests                    | 10m                                                            |
| `linux.resources.livenessProbe.requests.memory`        | liveness-probe memory requests                 | 20Mi                                                           |
| `linux.resources.nodeDriverRegistrar.limits.memory`    | csi-node-driver-registrar memory limits               | 100Mi                                                          |
| `linux.resources.nodeDriverRegistrar.requests.cpu`     | csi-node-driver-registrar cpu requests         | 30m                                                            |
| `linux.resources.nodeDriverRegistrar.requests.memory`  | csi-node-driver-registrar memory requests      | 20Mi                                                           |
| `linux.resources.azurefile.limits.memory`              | azurefile memory limits                         | 200Mi                                                         |
| `linux.resources.azurefile.requests.cpu`               | azurefile cpu requests                   | 10m                                                            |
| `linux.resources.azurefile.requests.memory`            | azurefile memory requests                | 20Mi                                                           |
| `windows.enabled`                                 | whether enable windows feature                             | `true`                                                             |
| `windows.dsName`                                  | name of driver daemonset on windows                             |`csi-azurefile-node-win`                                                         |
| `windows.useHostProcessContainers`                | whether deploy driver daemonset with host process containers on windows | `false`                                                             |
| `windows.kubelet`                                 | configure kubelet directory path on Windows agent node                | `'C:\var\lib\kubelet'`                                            |
| `windows.kubeconfig`                              | configure kubeconfig path on Windows agent node                | `` (empty, use InClusterConfig by default)                                            |
| `windows.enableRegistrationProbe`                 | enable [kubelet-registration-probe](https://github.com/kubernetes-csi/node-driver-registrar#health-check-with-an-exec-probe) on windows driver config     | `true`
| `windows.tolerations`                             | windows node driver tolerations                            |                                                              |
| `windows.affinity`                                | windows node pod affinity                                     | `{}`                                                             |
| `windows.nodeSelector`                            | windows node pod node selector                                | `{}`                                                             |
| `windows.labels`                                  | windows node daemonset extra labels                     | `{}`
| `windows.annotations`                             | windows node daemonset extra annotations                | `{}`
| `windows.podLabels`                               | windows node pods extra labels                          | `{}`
| `windows.podAnnotations`                          | windows node pods extra annotations                     | `{}`
| `windows.resources.livenessProbe.limits.memory`          | liveness-probe memory limits                          | 150Mi                                                          |
| `windows.resources.livenessProbe.requests.cpu`           | liveness-probe cpu requests                    | 10m                                                            |
| `windows.resources.livenessProbe.requests.memory`        | liveness-probe memory requests                 | 40Mi                                                           |
| `windows.resources.nodeDriverRegistrar.limits.memory`    | csi-node-driver-registrar memory limits               | 150Mi                                                          |
| `windows.resources.nodeDriverRegistrar.requests.cpu`     | csi-node-driver-registrar cpu requests         | 10m                                                            |
| `windows.resources.nodeDriverRegistrar.requests.memory`  | csi-node-driver-registrar memory requests      | 40Mi                                                           |
| `windows.resources.azurefile.limits.memory`              | azurefile memory limits                         | 200Mi                                                         |
| `windows.resources.azurefile.requests.cpu`               | azurefile cpu requests                   | 10m                                                            |
| `windows.resources.azurefile.requests.memory`            | azurefile memory requests                | 40Mi                                                           |
| `workloadIdentity.clientID` | client ID of workload identity | ''
| `workloadIdentity.tenantID` | [optional] If the AAD application or user-assigned managed identity is not in the same tenant as the cluster then set tenantID with the AAD application or user-assigned managed identity tenant ID | ''

## troubleshooting
 - Add `--wait -v=5 --debug` in `helm install` command to get detailed error
 - Use `kubectl describe` to acquire more info
