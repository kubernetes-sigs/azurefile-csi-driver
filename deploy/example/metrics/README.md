## Get Prometheus metrics from CSI driver controller
1. Create `csi-azurefile-controller` service with targetPort `29614`
```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/metrics/csi-azurefile-controller-svc.yaml
```

2. Get `EXTERNAL-IP` of service `csi-azurefile-controller`
```console
$ kubectl get svc csi-azurefile-controller -n kube-system
NAME                       TYPE           CLUSTER-IP   EXTERNAL-IP    PORT(S)           AGE
csi-azurefile-controller   LoadBalancer   10.0.184.0   20.39.21.132   29614:30563/TCP   47m
```

3. Run following command to get cloudprovider_azure operation metrics
```console
ip=`kubectl get svc csi-azurefile-controller -n kube-system | grep file | awk '{print $4}'`
curl http://$ip:29614/metrics | grep cloudprovider_azure | grep file | grep -e sum -e count
```

4. Run following command to get CSI-specific operation metrics
```console
ip=`kubectl get svc csi-azurefile-controller -n kube-system | grep file | awk '{print $4}'`
curl http://$ip:29614/metrics | grep azurefile_csi_driver_operation | grep -e sum -e count
```


## CSI Driver Metrics

The Azure File CSI driver exposes the following custom metrics:

### Controller Metrics (port 29614)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `azurefile_csi_driver_operation_duration_seconds` | Histogram | `operation`, `success` | Duration of CSI operations in seconds |
| `azurefile_csi_driver_operation_duration_seconds_labeled` | Histogram | `operation`, `success`, `protocol`, `storage_account_type` | Duration of CSI operations with additional labels |
| `azurefile_csi_driver_operations_total` | Counter | `operation`, `success` | Total number of CSI operations |

**Label Values:**
- `operation`: `controller_create_volume`, `controller_delete_volume`, `controller_create_snapshot`, `controller_delete_snapshot`, `controller_expand_volume`
- `success`: `true`, `false`
- `protocol`: `SMB`, `NFS`
- `storage_account_type`: `Premium_LRS`, `Premium_ZRS`, `Standard_LRS`, `StandardV2_LRS`, `Standard_GRS`, `Standard_ZRS`, etc.

### Node Metrics (port 29615)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `azurefile_csi_driver_operation_duration_seconds` | Histogram | `operation`, `success` | Duration of CSI operations in seconds |
| `azurefile_csi_driver_operations_total` | Counter | `operation`, `success` | Total number of CSI operations |
| `azurefile_csi_driver_mount_operations_total` | Counter | `operation`, `success`, `protocol`, `storage_account`, `subscription_id`, `mount_error_reason` | Total number of mount-path executions, labeled with the target storage account identity (on failures) and a classified error reason. Used to track mount failures for a single file share across many nodes/clusters/subscriptions. |

**Label Values:**
- `operation`: `node_stage_volume`, `node_unstage_volume`, `node_publish_volume`, `node_unpublish_volume`, `inline_mount`
- `success`: `true`, `false`

Ephemeral (inline) volume `NodePublishVolume` calls are tracked through `operations_total` with `operation="inline_mount"`, so they can be distinguished from persistent publishes without a dedicated metric.

**Details of `mount_operations_total`**
- `operation`: `node_stage_volume_mount` (the mount command).
  - For disk-on-file (vhd) volumes a single `NodeStageVolume` emits two rows: the SMB mount (`protocol="cifs"`) and the inner disk mount (`protocol="disk"`), so filter `protocol="cifs"`/`"nfs"` when counting share mounts to avoid double-counting those volumes.
- `protocol`: `SMB`/`cifs`, `NFS`/`nfs`, `aznfs`, or `disk`.
- `storage_account`, `subscription_id`: the target share's storage account and subscription. **Populated only on failed mounts** (`success="false"`); successful mounts leave these blank so steady-state cardinality stays bounded to the small set of distinct *failing* shares/subscriptions rather than every share ever mounted on the node. `file_share` and `resource_group` are intentionally never recorded (file_share is per-PVC high-cardinality; resource_group adds no attribution value beyond account + subscription).
- `mount_error_reason`: `""` on success, otherwise one of `timeout`, `network`, `stale` (retryable/transient), `busy` (target/resource busy, often already mounted), `enoent` (ambiguous, see below), `access_denied`, or `other`.
  - **`enoent` is ambiguous, not just "retryable"** for SMB. Kernel returns `mount error(2): No such file or directory` for a truly absent share (SMB2 maps `STATUS_BAD_NETWORK_NAME` to `-ENOENT`, terminal) and a transient tree-connect rejection under overload. `mount.cifs` prints the mapped errno, not status name (which only reaches `dmesg`), so indistinguishable from the mount error alone.
  - The reason is derived only from the mount command's own output (`mount.cifs`/`mount.nfs` stderr and synthesized timeout).

### Azure Cloud Provider Metrics

The CSI driver also exposes Azure cloud provider metrics from the underlying Azure SDK operations:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `cloudprovider_azure_api_request_duration_seconds` | Histogram | `request`, `resource_group`, `subscription_id`, `source`, `result` | Latency of Azure API calls |
| `cloudprovider_azure_api_request_throttled_count` | Counter | `request`, `resource_group`, `subscription_id`, `source` | Number of throttled Azure API requests |
| `cloudprovider_azure_api_request_errors` | Counter | `request`, `resource_group`, `subscription_id`, `source` | Number of errors in Azure API requests |

These metrics help monitor Azure API performance, throttling, and error rates for file share operations.

## Get Prometheus metrics from CSI driver node pod

```console
kubectl get --raw /api/v1/namespaces/kube-system/pods/csi-azurefile-node-xxxxx:29615/proxy/metrics
```

> **Note:** Replace `csi-azurefile-node-xxxxx` with an actual pod name from `kubectl get pods -n kube-system -l app=csi-azurefile-node`
