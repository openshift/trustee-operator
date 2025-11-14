# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The `trustee-operator` is a Kubernetes operator that manages the lifecycle of [trustee](https://github.com/confidential-containers/trustee) (formerly Key Broker Service/KBS) along with its configuration in a Kubernetes cluster. This is part of the Confidential Containers project and handles attestation and secret provisioning for confidential computing workloads.

Built with Kubebuilder v4 and Go 1.24, the operator follows the Kubernetes Operator pattern using controller-runtime.

## Architecture

### Custom Resource Definitions (CRDs)

The operator manages two primary CRDs:

1. **KbsConfig** (`api/v1alpha1/kbsconfig_types.go`): Low-level configuration that directly manages trustee deployment resources. Requires users to provide ConfigMaps and Secrets explicitly.

2. **TrusteeConfig** (`api/v1alpha1/trusteeconfig_types.go`): High-level, user-friendly interface that abstracts complexity. Automatically generates required ConfigMaps, Secrets, and creates a KbsConfig resource.

### Controllers

- **KbsConfigReconciler** (`internal/controller/kbsconfig_controller.go`): Main controller (~990 lines) that reconciles KbsConfig resources. Creates and manages:
  - Deployments for KBS components
  - Services for network access
  - Volume mounts for configs, secrets, and certificates
  - Handles both AllInOneDeployment and MicroservicesDeployment modes

- **TrusteeConfigReconciler** (`internal/controller/trusteeconfig_controller.go`): Higher-level controller (~1300 lines) that reconciles TrusteeConfig resources. Generates configuration from templates, creates auth keys, and delegates to KbsConfig.

### Deployment Modes

- **AllInOneDeployment**: All KBS components (KBS, AS, RVPS) in a single container
- **MicroservicesDeployment** (default): Components deployed in separate containers within the same pod

### Helper Modules

- `volumes.go`: Volume and mount management for configs, secrets, certs
- `crypto_helper.go`: Cryptographic operations (ED25519 key generation)
- `rvps_helper.go`: RVPS (Reference Value Provider Service) configuration
- `resource_policy_helper.go`: Resource policy handling
- `tdx_helper.go`: Intel TDX specific configuration
- `common.go`: Constants including default image names and paths

### Configuration Templates

Templates in `config/templates/` provide default configurations:
- `kbs-config-permissive.toml` / `kbs-config-restricted.toml`: KBS configs
- `attestation-policy.rego`: OPA policies for attestation
- `resource-policy-permissive.rego` / `resource-policy-restrictive.rego`: Resource access policies
- `rvps-reference-values.json`: Reference values for attestation
- `tdx-config.json`: Intel TDX configuration

## Common Development Commands

### Building and Testing

```bash
# Build the operator binary
make build

# Run tests
make test

# Run controller locally (uses current kubeconfig context)
make run

# Format and vet code
make fmt vet

# Generate manifests (CRDs, RBAC) after API changes
make manifests

# Generate DeepCopy methods after API changes
make generate
```

### Docker Image Operations

```bash
# Build Docker image
make docker-build IMG=<registry>/<image>:<tag>

# Push Docker image
make docker-push IMG=<registry>/<image>:<tag>

# Build multi-platform image (amd64, arm64, s390x, ppc64le)
make docker-buildx IMG=<registry>/<image>:<tag>
```

### Kubernetes Deployment

```bash
# Install CRDs into cluster
make install

# Deploy operator to cluster
make deploy IMG=<registry>/<image>:<tag>

# Uninstall CRDs
make uninstall

# Undeploy operator
make undeploy
```

### Testing Deployment Locally

```bash
# Deploy sample configuration (microservices mode)
cd config/samples/microservices

# Generate authentication keys
openssl genpkey -algorithm ed25519 > privateKey
openssl pkey -in privateKey -pubout -out kbs.pem

# Apply all resources
kubectl apply -k .

# Check operator status
kubectl get pods -n trustee-operator-system --watch

# Check trustee deployment
kubectl get pods -n trustee-operator-system --selector=app=kbs
```

### Integration Tests

```bash
# Run end-to-end tests in ephemeral kind cluster
# Requires: kuttl plugin and kind installed
make test-e2e

# Use custom images
KBS_IMAGE_NAME=<image> CLIENT_IMAGE_NAME=<client-image> make test-e2e
```

## Key Implementation Details

### Controller Reconciliation Flow

**KbsConfigReconciler**:
1. Fetches KbsConfig CR
2. Validates referenced ConfigMaps and Secrets exist
3. Creates/updates Deployment with appropriate containers based on deployment type
4. Creates/updates Service for network access
5. Manages volume mounts for configs, secrets, attestation policies, and TEE-specific configs

**TrusteeConfigReconciler**:
1. Fetches TrusteeConfig CR
2. Generates ConfigMaps from templates based on user settings
3. Creates ED25519 key pair for authentication if not provided
4. Creates Secrets for auth keys and client resources
5. Builds KbsConfigSpec from TrusteeConfig
6. Creates/updates corresponding KbsConfig CR

### Volume Management

The operator mounts various volumes into trustee pods:
- Configuration files (kbs-config.toml, as-config.json, rvps-config.json)
- Authentication secrets (public keys)
- Attestation and resource policies
- TEE-specific configs (TDX, IBM SE)
- Client secret resources
- Local certificate caches for disconnected environments

### TEE-Specific Configuration

- **Intel TDX**: Requires `sgx_default_qcnl.conf` ConfigMap
- **IBM Secure Execution**: Requires PVC with certificates/keys
- **Disconnected environments**: Support for mounting VCEK certificates via secrets

### Default Images

Located in `internal/controller/common.go`:
- KBS: `ghcr.io/confidential-containers/key-broker-service:latest`
- AS: `ghcr.io/confidential-containers/attestation-service:latest`
- RVPS: `ghcr.io/confidential-containers/reference-value-provider-service:latest`

## Important Notes

- The operator requires ConfigMaps to be created before deploying KbsConfig resources when using low-level API
- TrusteeConfig is the recommended user-facing API as it handles configuration generation automatically
- RVPS reference values must be updated from defaults for production use
- The namespace `trustee-operator-system` is the default operator namespace
- OpenShift proxy configuration is supported through proxy detection
- FIPS compliance is handled via UBI minimal base image with OpenSSL

## Documentation References

- IBM SE configuration: `docs/ibmse.md`
- Intel ITA configuration: `docs/ita.md`
- Disconnected environments: `docs/disconnected.md`
- Sample configurations: `config/samples/all-in-one/` and `config/samples/microservices/`
