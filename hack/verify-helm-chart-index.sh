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

readonly PKG_ROOT="$(git rev-parse --show-toplevel)"

INDEX=${PKG_ROOT}/charts/index.yaml

check_url() {  
    url=$1
    result=$(curl -I -m 5 -s -w "%{http_code}\n" -o /dev/null $1)
    if [ $result -eq 200 ]
    then
        echo "$1 is ok."
    else
        echo "$1 is fail."
        echo "rm "${PKG_ROOT}${url#*master}
        rm -rf ${PKG_ROOT}${url#*master}
        echo "helm repo index"
        helm repo index ${PKG_ROOT}/charts/ --url https://raw.githubusercontent.com/kubernetes-sigs/azurefile-csi-driver/master/charts/
    fi
}

check_yaml() {
    flag=0
    cat $INDEX | while read LINE
    do 
        if [ $flag = 0 ]
        then
            if [ "$(echo $LINE | grep "urls:")" != "" ]
            then
                flag=1
                continue
            fi
        fi
        if [ $flag = 1 ]
        then
            if [ "$(echo $LINE | grep -E "^- ")" != "" ];then
                url=$(echo "$LINE" | awk -F " " '{print $2}')
                check_url $url
                continue
            else
                flag=0
                continue
            fi
        fi
    done
}

echo "begin to verify helm chart index ..."
check_yaml
