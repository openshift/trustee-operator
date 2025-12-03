# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:1.25.3-1764620329 as builder

# Required by the ubi based go-toolset image
USER root

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Copy the config templates
COPY config/templates/ config/templates/
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/controller/ internal/controller/

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.7-1764578379

WORKDIR /
COPY --from=builder /opt/app-root/src/manager .

# Copy the config templates
COPY --from=builder /opt/app-root/src/config/templates/ /config/templates/

USER 65532:65532

ENTRYPOINT ["/manager"]

# Red Hat labels.

ARG NAME=trustee-operator
ARG DESCRIPTION="The Trustee operator."

LABEL com.redhat.component=$NAME
LABEL description=$DESCRIPTION
LABEL io.k8s.description=$DESCRIPTION
LABEL io.k8s.display-name=$NAME
LABEL name=$NAME
LABEL summary=$DESCRIPTION
LABEL distribution-scope=public
LABEL url="https://access.redhat.com/"
LABEL vendor="Red Hat, Inc."
LABEL version="1.0.0"
LABEL maintainer="Red Hat"
LABEL io.openshift.tags=""

# Licenses

COPY LICENSE /licenses/LICENSE
