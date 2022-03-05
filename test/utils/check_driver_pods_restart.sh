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

function get_array() {
    for value in ${1}
    do
        arr[${#arr[@]}]=$value
    done
    echo "${arr[*]}"
}

echo "check the driver pods if restarts ..."
mapfile -t PODS_WITH_RESTARTS < <(kubectl get pods --namespace kube-system \
    --selector app.kubernetes.io/name=azurefile-csi-driver \
    --output=jsonpath='{range .items[*]}{.metadata.name} {range .status.containerStatuses[?(@.restartCount!=0)]}{.name};{end}{"\n"}{end}' \
    | awk '{if ($2 != "") print $1}')

for POD_WITH_RESTART in "${PODS_WITH_RESTARTS[@]}"; do
    echo "there is a driver pod which has restarted"

    if [[ "$1" == "log" ]]; then
        kubectl describe pod "$POD_WITH_RESTART" --namespace kube-system
        echo "======================================================================================"
        echo "print previous azurefile container logs since there is a restart"
        kubectl logs "$POD_WITH_RESTART" --container azurefile --previous --namespace kube-system
        echo "======================================================================================"
    fi
done

echo "======================================================================================"
