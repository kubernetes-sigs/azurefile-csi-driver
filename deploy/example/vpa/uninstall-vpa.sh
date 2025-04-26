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

echo "Uninstalling VPA ..."

for i in "${COMPONENTS[@]}"; do
  echo "Uninstalling ${i} ..."
  if [ $i == admission-controller-deployment ] ; then
    ${SCRIPT_ROOT}/install-components/rmcerts.sh
    ${SCRIPT_ROOT}/install-components/delete-webhook.sh
  fi
  kubectl delete -f ${SCRIPT_ROOT}/install-components/${i}.yaml --ignore-not-found
done
bash ${SCRIPT_ROOT}/install-components/warn-obsolete-vpa-objects.sh

echo 'VPA uninstalled successfully.'
