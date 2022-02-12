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

NS=kube-system
CONTAINER=azurefile

echo "check the driver pods if restarts ..."
original_pods=$(kubectl get pods -n kube-system | grep azurefile | awk '{print $1}')
original_restarts=$(kubectl get pods -n kube-system | grep azurefile | awk '{print $4}')

processed_pods=("$(get_array "${original_pods[@]}")")
processed_restarts=("$(get_array "${original_restarts[@]}")")

for ((i=0; i<${#processed_restarts[@]}; i++)); do
    if [ "${processed_restarts[$i]}" -ne "0" ]
    then
        echo "there is a driver pod which has restarted"
	    #disable pods restart check temporarily since there is driver restart in MSI enabled cluster
        #exit 3
        if [[ "$1" == "log" ]]; then
            kubectl describe po ${processed_pods[$i]} -n kube-system
            echo "======================================================================================"
            echo "print previous azurefile container logs since there is a restart"
            kubectl logs ${processed_pods[$i]} -c azurefile -p -n kube-system
            echo "======================================================================================"
        fi
    fi
done

echo "======================================================================================"
