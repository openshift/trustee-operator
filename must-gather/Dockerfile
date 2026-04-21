FROM registry.redhat.io/openshift4/ose-must-gather-rhel9:v4.19 as builder

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.7-1776104705

# For gathering data from nodes
# NOTE for hermetic build: any change to the packages installed here must
# be reflected in the rpms.in.yaml file.
# Also make sure to re-generate the rpms.lock.yaml file when doing so.
# See https://konflux.pages.redhat.com/docs/users/building/prefetching-dependencies.html#rpm
RUN microdnf update -y && microdnf install tar rsync -y && microdnf clean all

COPY --from=builder /usr/bin/oc /usr/bin/oc

# Save original gather script
COPY --from=builder /usr/bin/gather /usr/bin/gather_original

# Copy all collection scripts to /usr/bin
COPY collection-scripts/* /usr/bin/

# Red Hat labels
LABEL name="openshift-trustee-operator/trustee-must-gather-rhel9" \
cpe="cpe:/a:redhat:confidential_compute_attestation:1.101::el9" \
version="1.1.0" \
com.redhat.component="redhat-build-of-trustee-must-gather-container" \
summary="redhat-build-of-trustee-must-gather collects information about RedHat Build of Trustee and its components" \
maintainer="support@redhat.com" \
description="trustee-must-gather collects information about RedHat Build of Trustee" \
io.k8s.display-name="redhat-build-of-trustee-must-gather" \
io.k8s.description="redhat-build-of-trustee-must-gather collects information about RedHat Build of Trustee and its components" \
io.openshift.tags=""

ENTRYPOINT /usr/bin/gather
