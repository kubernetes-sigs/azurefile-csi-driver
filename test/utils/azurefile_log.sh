#!/bin/bash

# Copyright 2020 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# set -e

NS=kube-system
CONTAINER=azurefile
DRIVER=azurefile
if [[ "$#" -gt 0 ]]; then
    DRIVER=$1
fi

cleanup() {
    echo "hit unexpected error during log print, exit 0"
    exit 0
}

trap cleanup ERR

echo "print out all nodes status ..."
kubectl get nodes -o wide
echo "======================================================================================"

echo "print out all default namespace pods status ..."
kubectl get pods -n default -o wide
echo "======================================================================================"

echo "print out all $NS namespace pods status ..."
kubectl get pods -n${NS} -o wide
echo "======================================================================================"

echo "print out csi-$DRIVER-controller pods ..."
echo "======================================================================================"
LABEL="app=csi-$DRIVER-controller"
kubectl get pods -n${NS} -l${LABEL} \
    | awk 'NR>1 {print $1}' \
    | xargs -I {} bash -c "echo 'dumping logs for ${NS}/{}/${DRIVER}' && kubectl logs {} -c${CONTAINER} -n${NS}"

#echo "print out csi-snapshot-controller pods ..."
#echo "======================================================================================"
#LABEL='app=csi-snapshot-controller'
#kubectl get pods -n${NS} -l${LABEL} \
#    | awk 'NR>1 {print $1}' \
#    | xargs -I {} bash -c "echo 'dumping logs for ${NS}/{}/csi-snapshot-controller' && kubectl logs {} -n${NS}"

echo "print out csi-$DRIVER-node logs ..."
echo "======================================================================================"
LABEL="app=csi-$DRIVER-node"
kubectl get pods -n${NS} -l${LABEL} \
    | awk 'NR>1 {print $1}' \
    | xargs -I {} bash -c "echo 'dumping logs for ${NS}/{}/${DRIVER}' && kubectl logs {} -c${CONTAINER} -n${NS}"

echo "print out csi-$DRIVER-node-win logs ..."
echo "======================================================================================"
LABEL="app=csi-$DRIVER-node-win"
kubectl get pods -n${NS} -l${LABEL} \
    | awk 'NR>1 {print $1}' \
    | xargs -I {} bash -c "echo 'dumping logs for ${NS}/{}/${DRIVER}' && kubectl logs {} -c${CONTAINER} -n${NS}"

echo "======================================================================================"
ip=`kubectl get svc csi-${DRIVER}-controller -n kube-system | awk '{print $4}'`
if echo "$ip" | grep -q "\."; then
    echo "print out cloudprovider_azure metrics ..."
    curl http://$ip:29614/metrics
else
    echo "csi-$DRIVER-controller service ip is empty"
    kubectl get svc csi-$DRIVER-controller -n kube-system
fi
