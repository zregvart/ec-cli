#!/usr/bin/env bash
# Copyright The Enterprise Contract Contributors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0

# Updates the rpms.lock.yaml file

set -o errexit
set -o pipefail
set -o nounset

root_dir=$(git rev-parse --show-toplevel)

latest_release=$(gh api '/repos/konflux-ci/rpm-lockfile-prototype/tags?per_page=1' --jq '.[0].name')

# build the image for running the RPM lock tool
echo Building...
image=$(podman build --quiet --file <(cat <<DOCKERFILE
FROM registry.access.redhat.com/ubi9/python-39:latest

USER 0

RUN dnf install --assumeyes --nodocs --setopt=keepcache=0 --refresh skopeo jq

RUN pip install https://github.com/konflux-ci/rpm-lockfile-prototype/archive/refs/tags/${latest_release}.tar.gz
RUN pip install dockerfile-parse

ENV PYTHONPATH=/usr/lib64/python3.9/site-packages:/usr/lib/python3.9/site-packages
ENV XDG_DATA_HOME=/opt/app-root
DOCKERFILE
))

echo "Built: ${image}"

# script that performs everything within the image built above
# shellcheck disable=SC2016,SC2125
script='
set -o errexit
set -o pipefail
set -o nounset
shopt -s extglob

# determine the base image
base_img=$(python <<SCRIPT
from dockerfile_parse import DockerfileParser

dfp = DockerfileParser()
with open("Dockerfile") as d:
    dfp.content = d.read()

# assume the last mentioned FROM is the image we want to base on
print(dfp.parent_images[-1])

SCRIPT
)

# copy the base image to temporary directory
base_img_dir=$(mktemp -d --tmpdir)
skopeo copy --quiet "docker://${base_img/:!(:)@/@}" "dir:/${base_img_dir}"

# extract all /etc/yum.repos.d/* files from the base image
tar --dir "${base_img_dir}" --extract --ignore-zeros 'etc/yum.repos.d/*' -f "${base_img_dir}/$(jq -r '\''.layers[].digest | sub("sha256:"; "")'\'' "${base_img_dir}/manifest.json")"

# enable source repositories
for r in $(dnf repolist --setopt=reposdir="${base_img_dir}/etc/yum.repos.d" --disabled --quiet|grep -- '\''-source'\'' | sed '\''s/ .*//'\''); do
    dnf config-manager --quiet --setopt=reposdir="${base_img_dir}/etc/yum.repos.d" "${r}" --set-enabled
done

cp "${base_img_dir}/etc/yum.repos.d"/*.repo /opt/app-root/src/

# generate/update the RPM lock file
/opt/app-root/bin/rpm-lockfile-prototype -f Dockerfile --outfile rpms.lock.yaml rpms.in.yaml
'

echo Running...
podman run \
    --rm \
    --mount type=bind,source="${root_dir}/Dockerfile.dist",destination=/opt/app-root/src/Dockerfile \
    --mount type=bind,source="${root_dir}/rpms.in.yaml",destination=/opt/app-root/src/rpms.in.yaml \
    --mount type=bind,source="${root_dir}/rpms.lock.yaml",destination=/opt/app-root/src/rpms.lock.yaml \
    "${image}" \
    bash -c "${script}"
