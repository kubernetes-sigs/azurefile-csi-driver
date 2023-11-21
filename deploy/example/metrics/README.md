## Get Prometheus metrics from CSI driver controller
1. Create `csi-azurefile-controller` service with targetPort `29614`
```console
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/deploy/example/metrics/csi-azurefile-controller-svc.yaml
```

2. Get `EXTERNAL-IP` of service `csi-azurefile-controller`
```console
$ kubectl get svc csi-azurefile-controller -n kube-system
NAME                       TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)     AGE
csi-azurefile-controller   ClusterIP   10.0.184.0   20.39.21.132  29614/TCP   47m
```

3. Run following command to get cloudprovider_azure operation metrics
```console
ip=`kubectl get svc csi-azurefile-controller -n kube-system | grep file | awk '{print $4}'`
curl http://$ip:29614/metrics | grep cloudprovider_azure | grep file | grep -e sum -e count
```

## Get Prometheus metrics from CSI driver node pod

```console
kubectl get --raw /api/v1/namespaces/kube-system/pods/csi-azurefile-node-hfgrn:29615/proxy/metrics
```
