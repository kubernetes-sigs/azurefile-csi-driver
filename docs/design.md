# azurefile CSI driver design goals
 > azurefile CSI driver is implemented as compatitable as possible with built-in [azurefile](https://kubernetes.io/docs/concepts/storage/volumes/#azurefile) plugin, it has following goals:

Goal | Status | Notes
--- | --- | --- |
Support Kubernetes release 1.14 or later | Completed| release prior to 1.14 won't be supported |
Support service principal and msi authentication | Completed |  |
Support both Linux & Windows | Completed | Windows related work is in progress: [Enable CSI hostpath example on windows](https://github.com/kubernetes-csi/drivers/issues/79) |
Compatible with original storage class parameters and usage| Completed | there is a little difference in static provision, see [example](../deploy/example/pv-azurefile-csi.yaml) |
Support sovereign cloud| Completed | verification pass on Azure China |

### Work items
Item | Status | Notes
--- | --- | --- |
Support volume size grow | Completed |  |
Support snapshot | Completed |  |
Enable CI on Windows | Completed |  |
Complete all unit tests | Completed |  |
Set up E2E test | Completed |  |
Implement NodeStage/NodeUnstage functions | Completed | two pods on same node could share same PVC mount |
Implement azure file csi driver on Windows | Completed |  |

### Implementation details
To prevent possible regression issues, azurefile CSI driver use [azure cloud provider](https://github.com/kubernetes/kubernetes/tree/v1.13.0/pkg/cloudprovider/providers/azure) library. Thus, all bug fixes in the built-in azure file plugin would be incorporated into this driver.
