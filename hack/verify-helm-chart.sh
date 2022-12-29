#!/bin/bash

# Copyright 2019 The Kubernetes Authors.
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

readonly PKG_ROOT="$(git rev-parse --show-toplevel)"

function get_image_from_helm_chart() {
  local -r image_name="${1}"
  image_repository="$(cat ${PKG_ROOT}/charts/latest/azurefile-csi-driver/values.yaml | yq -r ${image_name}.repository)"
  image_tag="$(cat ${PKG_ROOT}/charts/latest/azurefile-csi-driver/values.yaml | yq -r ${image_name}.tag)"
  echo "${image_repository}:${image_tag}"
}

function validate_image() {
  local -r expected_image="${1}"
  local -r image="${2}"

  if [[ ! "${expected_image}" == *"${image}" ]]; then
    echo "Expected ${expected_image}, but got ${image} in helm chart"
    exit 1
  fi
}

echo "Running helm lint"

if [[ -z "$(command -v helm)" ]]; then
  echo "Cannot find helm. Installing helm..."
  curl https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3 | bash
fi

helm lint ${PKG_ROOT}/charts/latest/azurefile-csi-driver

echo "Comparing image version between helm chart and manifests in deploy folder"

if [[ -z "$(command -v pip)" ]]; then
  echo "Cannot find pip. Installing pip3..."
  apt install python3-pip -y
  update-alternatives --install /usr/bin/pip pip /usr/bin/pip3 1
fi

if [[ -z "$(command -v jq)" ]]; then
  echo "Cannot find jq. Installing yq..."
  apt install jq -y
fi

# install yq
pip install --ignore-installed --require-hashes -r ${PKG_ROOT}/hack/requirements.txt

# Extract images from csi-azurefile-controller.yaml
expected_csi_provisioner_image="$(cat ${PKG_ROOT}/deploy/csi-azurefile-controller.yaml | yq -r .spec.template.spec.containers[0].image | head -n 1)"
expected_csi_attacher_image="$(cat ${PKG_ROOT}/deploy/csi-azurefile-controller.yaml | yq -r .spec.template.spec.containers[1].image | head -n 1)"
expected_csi_snapshotter_image="$(cat ${PKG_ROOT}/deploy/csi-azurefile-controller.yaml | yq -r .spec.template.spec.containers[2].image | head -n 1)"
expected_csi_resizer_image="$(cat ${PKG_ROOT}/deploy/csi-azurefile-controller.yaml | yq -r .spec.template.spec.containers[3].image | head -n 1)"
expected_liveness_probe_image="$(cat ${PKG_ROOT}/deploy/csi-azurefile-controller.yaml | yq -r .spec.template.spec.containers[4].image | head -n 1)"
expected_azurefile_image="$(cat ${PKG_ROOT}/deploy/csi-azurefile-controller.yaml | yq -r .spec.template.spec.containers[5].image | head -n 1)"

csi_provisioner_image="$(get_image_from_helm_chart ".image.csiProvisioner")"
validate_image "${expected_csi_provisioner_image}" "${csi_provisioner_image}"

csi_attacher_image="$(get_image_from_helm_chart ".image.csiAttacher")"
validate_image "${expected_csi_attacher_image}" "${csi_attacher_image}"

csi_snapshotter_image="$(get_image_from_helm_chart ".snapshot.image.csiSnapshotter")"
validate_image "${expected_csi_snapshotter_image}" "${csi_snapshotter_image}"

csi_resizer_image="$(get_image_from_helm_chart ".image.csiResizer")"
validate_image "${expected_csi_resizer_image}" "${csi_resizer_image}"

liveness_probe_image="$(get_image_from_helm_chart ".image.livenessProbe")"
validate_image "${expected_liveness_probe_image}" "${liveness_probe_image}"

azurefile_image="$(get_image_from_helm_chart ".image.azurefile")"
validate_image "${expected_azurefile_image}" "${azurefile_image}"

# Extract images from csi-azurefile-node.yaml
expected_liveness_probe_image="$(cat ${PKG_ROOT}/deploy/csi-azurefile-node.yaml | yq -r .spec.template.spec.containers[0].image | head -n 1)"
expected_node_driver_registrar="$(cat ${PKG_ROOT}/deploy/csi-azurefile-node.yaml | yq -r .spec.template.spec.containers[1].image | head -n 1)"
expected_azurefile_image="$(cat ${PKG_ROOT}/deploy/csi-azurefile-node.yaml | yq -r .spec.template.spec.containers[2].image | head -n 1)"

validate_image "${expected_liveness_probe_image}" "${liveness_probe_image}"

node_driver_registrar="$(get_image_from_helm_chart ".image.nodeDriverRegistrar")"
validate_image "${expected_node_driver_registrar}" "${node_driver_registrar}"

validate_image "${expected_azurefile_image}" "${azurefile_image}"

# Extract images from csi-snapshot-controller.yaml
expected_snapshot_controller_image="$(cat ${PKG_ROOT}/deploy/csi-snapshot-controller.yaml | yq -r .spec.template.spec.containers[0].image | head -n 1)"

snapshot_controller_image="$(get_image_from_helm_chart ".snapshot.image.csiSnapshotController")"
validate_image "${expected_snapshot_controller_image}" "${snapshot_controller_image}"

echo "Images in deploy/ matches those in the latest helm chart."
