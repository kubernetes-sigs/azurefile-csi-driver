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

if [ "${MIGRATE_K8S_REPO}" = "true" ]
then
  # https://kubernetes.io/blog/2023/08/15/pkgs-k8s-io-introduction/#how-to-migrate
  echo "deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.30/deb/ /" | tee /host/etc/apt/sources.list.d/kubernetes.list
  $HOST_CMD curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.30/deb/Release.key | $HOST_CMD gpg --dearmor --batch --yes -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
fi


if [ "${INSTALL_AZNFS_MOUNT}" = "true" ];then
  # install aznfs-mount on ubuntu
  if [ "$DISTRIBUTION" = "ubuntu" ];then
    AZNFS_VERSION="0.3.15"
    echo "install aznfs v$AZNFS_VERSION...."
    # shellcheck disable=SC1091
    $HOST_CMD curl -sSL -O "https://packages.microsoft.com/config/$(. /host/etc/os-release && echo "$ID/$VERSION_ID")/packages-microsoft-prod.deb"
    yes | $HOST_CMD dpkg -i packages-microsoft-prod.deb && $HOST_CMD apt-get update
    $HOST_CMD rm packages-microsoft-prod.deb
    $HOST_CMD apt-get install -y aznfs="$AZNFS_VERSION"
    echo "aznfs-mount installed"
  elif [ "$DISTRIBUTION" = "azurelinux" ];then # install aznfs-mount on azure linux 3.0
    AZNFS_VERSION="0.1.548"
    echo "install aznfs v$AZNFS_VERSION...."
    $HOST_CMD curl -fsSL https://github.com/Azure/AZNFS-mount/releases/download/$AZNFS_VERSION/aznfs_install.sh | $HOST_CMD bash
  else
    echo "aznfs-mount is not supported on Linux distribution: $DISTRIBUTION"
    exit 0
  fi

  # Only install aznfswatchdogv4, so aznfswatchdogv3 is not needed
  echo "stop aznfswatchdog, only need aznfswatchdogv4."
  $HOST_CMD systemctl disable aznfswatchdog
  $HOST_CMD systemctl stop aznfswatchdog
fi

if [ "${INSTALL_AZUREFILE_PROXY}" = "true" ];then
  # install azurefile-proxy
  echo "install azurefile-proxy...."
  updateAzurefileProxy="true"
  if [ -f "/host/usr/bin/azurefile-proxy" ];then
    old=$(sha256sum /host/usr/bin/azurefile-proxy | awk '{print $1}')
    new=$(sha256sum /azurefile-proxy/azurefile-proxy | awk '{print $1}')
    if [ "$old" = "$new" ];then
      updateAzurefileProxy="false"
      echo "no need to update azurefile-proxy"
    fi
  fi
  if [ "$updateAzurefileProxy" = "true" ];then
    echo "copy azurefile-proxy...."
    rm -rf /host/"$KUBELET_PATH"/plugins/file.csi.azure.com/azurefile-proxy.sock
    cp /azurefile-proxy/azurefile-proxy /host/usr/bin/azurefile-proxy --force
    chmod 755 /host/usr/bin/azurefile-proxy
  fi

  updateService="true"
  if [ -f "/host/usr/lib/systemd/system/azurefile-proxy.service" ];then
    old=$(sha256sum /host/usr/lib/systemd/system/azurefile-proxy.service | awk '{print $1}')
    new=$(sha256sum /azurefile-proxy/azurefile-proxy.service | awk '{print $1}')
    if [ "$old" = "$new" ];then
        updateService="false"
        echo "no need to update azurefile-proxy.service"
    fi
  fi
  if [ "$updateService" = "true" ];then
    echo "copy azurefile-proxy.service...."
    mkdir -p /host/usr/lib/systemd/system
    cp /azurefile-proxy/azurefile-proxy.service /host/usr/lib/systemd/system/azurefile-proxy.service
  fi

  $HOST_CMD systemctl daemon-reload
  $HOST_CMD systemctl enable azurefile-proxy.service
  if [ "$updateAzurefileProxy" = "true" ] || [ "$updateService" = "true" ];then
    echo "restart azurefile-proxy...."
    $HOST_CMD systemctl restart azurefile-proxy.service
  else
    echo "start azurefile-proxy...."
    $HOST_CMD systemctl start azurefile-proxy.service
  fi
fi
