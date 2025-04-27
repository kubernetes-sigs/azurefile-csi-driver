#!/bin/bash
# Copyright 2025 The Kubernetes Authors.
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

set -o errexit
set -o nounset
set -o pipefail
set +o xtrace

SCRIPT_ROOT=$(dirname ${BASH_SOURCE[0]})
COMPONENTS=("vpa-v1-crd-gen" "vpa-rbac" "updater-deployment" "recommender-deployment" "admission-controller-deployment")

echo "Installing VPA ..."
for i in "${COMPONENTS[@]}"; do
  echo "Installing ${i} ..."
  if [ $i == admission-controller-deployment ] ; then
    ${SCRIPT_ROOT}/install-components/gencerts.sh
  fi
  kubectl apply -f ${SCRIPT_ROOT}/install-components/${i}.yaml
done

echo 'VPA installed successfully.'
