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

## Build

FROM docker.io/library/golang:1.21 AS build

ARG TARGETOS
ARG TARGETARCH
ARG BUILD_SUFFIX=""
ARG BUILD_LIST="${TARGETOS}_${TARGETARCH}"

# Avoid safe directory git failures building with default user from go-toolset
USER root

WORKDIR /build

# Copy just the mod file for better layer caching when building locally
COPY go.mod go.sum ./
RUN go mod download

# Now copy everything including .git
COPY . .

RUN /build/build.sh "${BUILD_LIST}" "${BUILD_SUFFIX}"

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.4@sha256:a7d837b00520a32502ada85ae339e33510cdfdbc8d2ddf460cc838e12ec5fa5a

ARG TARGETOS
ARG TARGETARCH

LABEL \
  name="ec-cli" \
  description="Enterprise Contract verifies and checks supply chain artifacts to ensure they meet security and business policies." \
  io.k8s.description="Enterprise Contract verifies and checks supply chain artifacts to ensure they meet security and business policies." \
  summary="Provides the binaries for downloading the EC CLI. Also used as a Tekton task runner image for EC tasks. Upstream build." \
  io.k8s.display-name="Enterprise Contract" \
  io.openshift.tags="enterprise-contract ec opa cosign sigstore"

# Install tools we want to use in the Tekton task
RUN microdnf upgrade --assumeyes --nodocs --setopt=keepcache=0 --refresh && microdnf -y --nodocs --setopt=keepcache=0 install git-core jq

# Copy all the binaries so they're available to extract and download
# (Beware if you're testing this locally it will copy everything from
# your dist directory, not just the freshly built binaries.)
COPY --from=build /build/dist/* /usr/local/bin/

# Gzip them because that's what the cli downloader image expects, see
# https://github.com/securesign/sigstore-ocp/blob/main/images/Dockerfile-clientserver
RUN gzip /usr/local/bin/ec_*

# Copy the one ec binary that can run in this container
COPY --from=build "/build/dist/ec_${TARGETOS}_${TARGETARCH}" /usr/local/bin/ec

# OpenShift preflight check requires a license
COPY --from=build /build/LICENSE /licenses/LICENSE

# OpenShift preflight check requires a non-root user
USER 1001

# Show some version numbers for troubleshooting purposes
RUN git version && jq --version && ec version && ls -l /usr/local/bin

ENTRYPOINT ["/usr/local/bin/ec"]
