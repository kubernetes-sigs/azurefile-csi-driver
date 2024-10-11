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

if [[ "$#" -eq 0 ]]; then
    echo "./verify-examples.sh requires at least 1 parameter"
    exit 1
fi

echo "begin to create deployment examples with parameter ", $1

kubectl config set-context --current --namespace=default

kubectl apply -f deploy/example/storageclass-azurefile-csi.yaml

rollout_and_wait() {
    echo "Applying config \"$1\""
    trap "echo \"Failed to apply config \\\"$1\\\"\" >&2" err

    APPNAME=$(kubectl apply -f $1 | grep -E "^(:?daemonset|deployment|statefulset|pod)" | awk '{printf $1}')
    if [[ -n $(expr "${APPNAME}" : "\(daemonset\|deployment\|statefulset\)" || true) ]]; then
        kubectl rollout status $APPNAME --watch --timeout=10m
    else
        kubectl wait "${APPNAME}" --for condition=ready --timeout=10m
    fi
}

EXAMPLES=()

if [[ "$1" == "linux" ]]; then
    EXAMPLES+=(\
        deploy/example/daemonset.yaml \
        deploy/example/deployment.yaml \
        deploy/example/statefulset.yaml \
        deploy/example/statefulset-nonroot.yaml \
        )
fi

if ! [[ "$1" == *"windows-2022"* ]]; then
    echo "switch to use ltsc2019 image in example test"
    sed -i 's/ltsc2022/ltsc2019/g' deploy/example/windows/deployment.yaml
    sed -i 's/ltsc2022/ltsc2019/g' deploy/example/windows/statefulset.yaml
fi

if [[ "$1" == *"windows"* ]]; then
    EXAMPLES+=(\
    deploy/example/windows/deployment.yaml \
    deploy/example/windows/statefulset.yaml \
    )
fi

for EXAMPLE in "${EXAMPLES[@]}"; do
    rollout_and_wait $EXAMPLE
done

echo "deployment examples running completed."
