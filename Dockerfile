# Build the manager binary
FROM quay.io/centos/centos:stream9-minimal AS builder
RUN microdnf install -y golang git \
    && microdnf clean all -y

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Ensure correct Go version
RUN export GO_VERSION=$(grep -E "go [[:digit:]]\.[[:digit:]][[:digit:]]" go.mod | awk '{print $2}') && \
    go install golang.org/dl/go${GO_VERSION}@latest && \
    ~/go/bin/go${GO_VERSION} download && \
    /bin/cp -f ~/go/bin/go${GO_VERSION} /usr/bin/go && \
    go version

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY hack/ hack/
COPY pkg/ pkg/
COPY version/ version/
COPY vendor/ vendor/

# for getting version info
COPY .git/ .git/

# Build
RUN ./hack/build.sh

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.1.0

WORKDIR /
COPY --from=builder /workspace/manager .

# Add Fence Agents and 3 more cloud agents
RUN microdnf install -y yum-utils \
    && dnf config-manager --set-enabled rhel-9-for-x86_64-highavailability-rpms \
    && dnf install -y fence-agents-all fence-agents-aws fence-agents-azure-arm fence-agents-gce \
    && dnf clean all -y

USER 65532:65532
    # RUN yum-config-manager --set-enabled rhel-9-for-x86_64-highavailability-rpms
ENTRYPOINT ["/manager"]
