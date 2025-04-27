# Vertical pod autoscaler to Azure file CSI controller pods
## Prerequisites
### Step 1: Install Vertical Pod Autoscaler (VPA)
- **Recommend**: You can use the script located at `azurefile-csi-driver/deploy/example/vpa/install-vpa.sh` to install the VPA. If you use this method, you can skip **Step 2**.
- You can also refer to the github repo [vertical-pod-autoscaler](https://github.com/kubernetes/autoscaler/blob/master/vertical-pod-autoscaler/README.md) for installation instructions.


### Step 2: Adjust Admission Controller Webhooks in AKS
In AKS, you need to add label `admissions.enforcer/disabled: true` to admission controller webhooks to impact kube-system AKS namespaces, refer to [Can admission controller webhooks impact kube-system and internal AKS namespaces?](https://learn.microsoft.com/en-us/azure/aks/faq#can-admission-controller-webhooks-impact-kube-system-and-internal-aks-namespaces-). This can be done by using the `--webhook-labels` flag in `vpa-admission-controller`, refer to [Running the admission-controller](https://github.com/kubernetes/autoscaler/blob/master/vertical-pod-autoscaler/docs/components.md#:~:text=You%20can%20specify%20a%20comma%20separated%20list%20to%20set%20webhook%20labels%20with%20%2D%2Dwebhook%2Dlabels%2C%20example%20format%3A%20key1%3Avalue1%2Ckey2%3Avalue2.)

If you do not use the recommended script to install VPA, you should manually add the `--webhook-labels=admissions.enforcer/disabled:true` flag in the vpa-admission-controller deployment.

> edit vpa-admission-controller deployment
```
k edit deploy vpa-admission-controller -n kube-system
```
> add `- --webhook-labels=admissions.enforcer/disabled:true` in containers args
```
  template:
    metadata:
      creationTimestamp: null
      labels:
        app: vpa-admission-controller
    spec:
      containers:
      - args:
        - --v=4
        - --stderrthreshold=info
        - --reload-cert
        - --webhook-labels=admissions.enforcer/disabled:true # add webhook-labels flags
```

> check vpa-admission-controller pod running 
```
k get po -n kube-system | grep vpa-admission-controller
vpa-admission-controller-7fcb5c6b86-s69p8              1/1     Running   0               13m
```

## Create a VPA corresponding to CSI controller deployment
> create a VPA for CSI controller
```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/vpa/vertical-pod-autoscaler.yaml
```
> check the VPA config and current recommended resource requests, have 2 evictedPod events for example
```
kubectl get vpa -n kube-system
NAME                       MODE   CPU   MEM        PROVIDED   AGE
csi-azurefile-controller   Auto   15m   43690666   True       8s

kubectl describe vpa csi-azurefile-controller -n kube-system
Name:         csi-azurefile-controller
Namespace:    kube-system
Labels:       <none>
Annotations:  <none>
API Version:  autoscaling.k8s.io/v1
Kind:         VerticalPodAutoscaler
Metadata:
  Creation Timestamp:  2025-03-25T06:45:04Z
  Generation:          1
  Resource Version:    62488
  UID:                 1675169f-6fd9-4c7c-b33e-fffe4f6fc57c
Spec:
  Resource Policy:
    Container Policies:
      Container Name:  *
      Controlled Resources:
        memory
      Max Allowed:
        Memory:  10Gi
  Target Ref:
    API Version:  apps/v1
    Kind:         Deployment
    Name:         csi-azurefile-controller
  Update Policy:
    Update Mode:  Auto
Status:
  Conditions:
    Last Transition Time:  2025-03-25T06:45:15Z
    Status:                True
    Type:                  RecommendationProvided
  Recommendation:
    Container Recommendations:
      Container Name:  azurefile
      Lower Bound:
        Memory:  43690666
      Target:
        Memory:  43690666
      Uncapped Target:
        Memory:  43690666
      Upper Bound:
        Memory:        10Gi
      Container Name:  csi-attacher
      Lower Bound:
        Memory:  43690666
      Target:
        Memory:  43690666
      Uncapped Target:
        Memory:  43690666
      Upper Bound:
        Memory:        10Gi
      Container Name:  csi-provisioner
      Lower Bound:
        Memory:  43690666
      Target:
        Memory:  43690666
      Uncapped Target:
        Memory:  43690666
      Upper Bound:
        Memory:        10Gi
      Container Name:  csi-resizer
      Lower Bound:
        Memory:  43690666
      Target:
        Memory:  43690666
      Uncapped Target:
        Memory:  43690666
      Upper Bound:
        Memory:        10Gi
      Container Name:  csi-snapshotter
      Lower Bound:
        Memory:  43690666
      Target:
        Memory:  43690666
      Uncapped Target:
        Memory:  43690666
      Upper Bound:
        Memory:        10Gi
      Container Name:  liveness-probe
      Lower Bound:
        Memory:  43690666
      Target:
        Memory:  43690666
      Uncapped Target:
        Memory:  43690666
      Upper Bound:
        Memory:  10Gi
Events:
  Type    Reason      Age   From         Message
  ----    ------      ----  ----         -------
  Normal  EvictedPod  101s  vpa-updater  VPA Updater evicted Pod csi-azurefile-controller-6658fb5fdc-d5mtr to apply resource recommendation.
  Normal  EvictedPod  41s   vpa-updater  VPA Updater evicted Pod csi-azurefile-controller-6658fb5fdc-hpdfk to apply resource recommendation.
```