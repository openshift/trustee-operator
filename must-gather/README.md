# Trustee Operator Must-Gather

This directory contains the must-gather implementation for RedHat Build of Trustee. The must-gather tool collects diagnostic information about the operator and its components to assist with troubleshooting.

## What is collected

The must-gather image collects the following information:

- **Namespaces**: `trustee-operator-system`, `openshift-operator-lifecycle-manager`, `openshift-marketplace`
- **Custom Resource Definitions (CRDs)**:
  - `kbsconfigs.confidentialcontainers.org`
  - `trusteeconfigs.confidentialcontainers.org`
- **Custom Resources**: All KbsConfig and TrusteeConfig instances
- **Kubernetes Resources**:
  - Deployments
  - Pods (descriptions and status)
  - Services
  - ConfigMaps
  - Secrets (metadata only, not the actual secret data)
  - Events
- **Logs**: Pod logs from all trustee operator components including:
  - trustee-operator controller manager
  - KBS (Key Broker Service) pods
- **Cluster Information**: Nodes and Machines

## Usage

To collect diagnostic information using this must-gather image:

```bash
oc adm must-gather --image=<image-registry>/trustee-must-gather:latest
```

The collected data will be saved to a local directory (e.g., `must-gather.local.<timestamp>`).

## Building the Image

To build the must-gather image:

```bash
cd must-gather
podman build -t trustee-must-gather:latest .
```

## Directory Structure

```
must-gather/
├── Dockerfile                    # Must-gather image definition
├── collection-scripts/
│   ├── gather                    # Main collection orchestrator
│   ├── gather_crds               # Collects CRD definitions
│   └── gather_trustee_operator   # Collects trustee-specific resources and logs
└── README.md                     # This file
```

## Troubleshooting

If the must-gather fails to collect certain resources:

1. Verify that the trustee operator is installed in the `trustee-operator-system` namespace
2. Check that you have appropriate RBAC permissions to access the resources
3. Review the must-gather pod logs for specific error messages

## Related Documentation

- [Trustee Operator Documentation](../README.md)
- [OpenShift Must-Gather Documentation](https://docs.openshift.com/container-platform/latest/support/gathering-cluster-data.html)
