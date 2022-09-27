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

function check_url() {  
    url=$1
    result=$(curl -I -m 5 -s -w "%{http_code}\n" -o /dev/null $1)
    if [ $result -ne 200 ]
    then
        echo "Error: $1 is fail."
        echo "Error: wrong pkg in "${PKG_ROOT}${url#*master}
        exit 1
    fi
}

function check_yaml() {
    grep http $INDEX | while read LINE
    do
        url=$(echo "$LINE" | awk -F " " '{print $2}')
        check_url $url
    done
}

echo "begin to verify helm chart index ..."
check_yaml
echo "all url in helm chart index have been checked"
