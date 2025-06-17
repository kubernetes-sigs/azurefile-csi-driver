#!/bin/sh

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

set -xe

MIGRATE_K8S_REPO=${MIGRATE_K8S_REPO:-false}

KUBELET_PATH=${KUBELET_PATH:-/var/lib/kubelet}
if [ "$KUBELET_PATH" != "/var/lib/kubelet" ];then
  echo "kubelet path is $KUBELET_PATH, update azurefile-proxy.service...."
  sed -i "s#--azurefile-proxy-endpoint[^ ]*#--azurefile-proxy-endpoint=unix:/${KUBELET_PATH}/plugins/file.csi.azure.com/azurefile-proxy.sock#" /azurefile-proxy/azurefile-proxy.service
  echo "azurefile-proxy endpoint is updated to unix:/$KUBELET_PATH/plugins/file.csi.azure.com/azurefile-proxy.sock"
fi 

HOST_CMD="nsenter --mount=/proc/1/ns/mnt"

DISTRIBUTION=$($HOST_CMD cat /etc/os-release | grep ^ID= | cut -d'=' -f2 | tr -d '"')
ARCH=$($HOST_CMD uname -m)
echo "Linux distribution: $DISTRIBUTION, Arch: $ARCH"

# Only install aznfs-mount on ubuntu and azurelinux
if [ "$DISTRIBUTION" != "azurelinux" ] && [ "$DISTRIBUTION" != "ubuntu" ];then
  echo "aznfs-mount is not supported on Linux distribution: $DISTRIBUTION"
  exit 0
fi

if [ "$ARCH" = "aarch64" ];then
  echo "aznfs-mount is not supported on architecture: $ARCH"
  exit 0
fi

. ./azurefile-proxy/install-proxy.sh

