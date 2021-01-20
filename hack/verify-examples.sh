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

set -euo pipefail

echo "begin to create deployment examples ..."

if [[ "$1" == "linux" ]]; then
    kubectl apply -f deploy/example/storageclass-azurefile-csi.yaml
    kubectl apply -f deploy/example/daemonset.yaml
    kubectl apply -f deploy/example/deployment.yaml
    kubectl apply -f deploy/example/statefulset.yaml
    kubectl apply -f deploy/example/statefulset-nonroot.yaml
    echo "sleep 60s ..."
    sleep 60
fi

if [[ "$1" == "windows" ]]; then
    kubectl apply -f deploy/example/storageclass-azurefile-csi.yaml
	kubectl apply -f deploy/example/windows/deployment.yaml
	kubectl apply -f deploy/example/windows/statefulset.yaml
    echo "sleep 120s ..."
    sleep 120
fi

echo "begin to check pod status ..."
kubectl get pods -o wide

if [[ "$1" == "linux" ]]; then
    kubectl get pods --field-selector status.phase=Running | grep daemonset-azurefile
    kubectl get pods --field-selector status.phase=Running | grep deployment-azurefile
    kubectl get pods --field-selector status.phase=Running | grep statefulset-azurefile-0
    kubectl get pods --field-selector status.phase=Running | grep statefulset-azurefile-nonroot-0
fi

if [[ "$1" == "windows" ]]; then
    kubectl get pods --field-selector status.phase=Running | grep deployment-azurefile-win
    kubectl get pods --field-selector status.phase=Running | grep statefulset-azurefile-win-0
fi

echo "deployment examples running completed."
