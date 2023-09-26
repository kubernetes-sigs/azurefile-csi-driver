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

set -eo pipefail

function cleanup {
  echo 'pkill -f azurefileplugin'
  pkill -f azurefileplugin
  echo 'Deleting CSI sanity test binary'
  rm -rf csi-test
}

trap cleanup EXIT

readonly endpoint='unix:///tmp/csi.sock'
nodeid='CSINode'
if [[ "$#" -gt 0 ]] && [[ -n "$1" ]]; then
  nodeid="$1"
fi

ARCH=$(uname -p)
if [[ "${ARCH}" == "x86_64" || ${ARCH} == "unknown" ]]; then
  ARCH="amd64"
fi

azcopyPath="/usr/local/bin/azcopy"
if [ ! -f "$azcopyPath" ]; then
  azcopyTarFile="azcopy.tar.gz"
  echo 'Downloading azcopy...'
  wget -O $azcopyTarFile azcopyvnext.azureedge.net/releases/release-10.24.0-20240326/azcopy_linux_amd64_10.24.0.tar.gz
  tar -zxvf $azcopyTarFile
  mv ./azcopy*/azcopy /usr/local/bin/azcopy
  rm -rf ./$azcopyTarFile
  chmod +x /usr/local/bin/azcopy
fi

_output/${ARCH}/azurefileplugin --endpoint "$endpoint" --nodeid "$nodeid" -v=5 &

# sleep a while waiting for azurefileplugin start up
sleep 1

echo 'Begin to run sanity test...'
readonly CSI_SANITY_BIN='csi-sanity'
"$CSI_SANITY_BIN" --ginkgo.v --ginkgo.noColor --csi.endpoint="$endpoint" --ginkgo.skip='should fail when the volume source snapshot is not found|should work|should fail when the volume does not exist|should fail when the node does not exist|Node Service NodeGetCapabilities|should remove target path'

testvolumeparameters='/tmp/vhd.yaml'
cat > $testvolumeparameters << EOF
fstype: ext4
EOF

echo 'Begin to run sanity test for vhd disk feature...'
"$CSI_SANITY_BIN" --ginkgo.v --ginkgo.noColor --csi.endpoint="$endpoint" --csi.testvolumeparameters="$testvolumeparameters" --ginkgo.skip='should fail when the volume source snapshot is not found|should work|should fail when volume does not exist on the specified path|should fail when the volume does not exist|should fail when the node does not exist|should be idempotent|Node Service NodeGetCapabilities|should remove target path'
