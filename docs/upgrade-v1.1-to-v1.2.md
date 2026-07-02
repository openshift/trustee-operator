# Upgrading from v1.1.0 to v1.2.0+

## Overview

Version 1.2.0 introduces automatic ConfigMap migration to provide a **zero-downtime, seamless upgrade experience** from v1.1.0.

## What Changed

### Configuration Format Changes

1. **Storage Directory Consolidation**
   - All storage moved to `/opt/confidential-containers/storage/`
   - Old: `/opt/confidential-containers/kbs/repository`
   - New: `/opt/confidential-containers/storage/repository`

2. **RVPS Storage Format**
   - Old: Single JSON file with array structure
   - New: Directory-based storage with object structure

3. **ConfigMap Key Names**
   - Resource policy: `policy.rego` → `resource-policy.rego`
   - RVPS reference values: `reference-values.json` → `reference_value`

### What's Handled Automatically

The operator **automatically detects and migrates** old ConfigMap formats during reconciliation:

✅ Detects v1.1 ConfigMap format (deprecated fields, missing v1.2 fields)  
✅ Regenerates ConfigMap content from v1.2 templates  
✅ Replaces old format with new format  
✅ Adds migration annotation to track what's been migrated  
✅ Triggers pod restart to apply new configuration  
✅ Logs migration activity in operator logs  

## Upgrade Process

### Development/Testing Upgrade

For testing the migration with latest code from this repository:

```bash
# 1. Build operator image with migration code
make docker-build docker-push IMG=<your-registry>/trustee-operator:upgrade-procedure

# 2. Deploy to your cluster
make deploy IMG=<your-registry>/trustee-operator:upgrade-procedure

# 3. Watch the migration happen (optional)
# Note: Migration logs only appear if v1.1 format is detected
kubectl logs -n trustee-operator-system -l control-plane=controller-manager -f | grep -i "Detected v1.1"

# You should see a log like:
# "Detected v1.1 config format, regenerating from template"

# 4. Check migration annotations on ConfigMaps
kubectl get configmap -n trustee-operator-system \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.annotations.kbs\.confidentialcontainers\.org/migrated-from-v1\.1\.0}{"\n"}{end}'

# 6. Verify KBS deployment is ready
kubectl wait --for=condition=Ready pod -l app=kbs -n trustee-operator-system --timeout=300s
```

**Note**: Replace `<your-registry>` with your container registry (e.g., `quay.io/youruser`).

### Production Upgrade (when v1.2.0 is released)

# For OpenShift environments using OLM
# The operator will be upgraded automatically via Operator Lifecycle Manager

# Watch the migration happen (optional)
kubectl logs -n trustee-operator-system -l control-plane=controller-manager -f | grep -i "migrat\|v1.1"

# Verify KBS deployment is ready
kubectl wait --for=condition=Ready pod -l app=kbs -n trustee-operator-system --timeout=300s
```

### Migration Verification

Check that ConfigMaps have been migrated:

```bash
# Check for migration annotations
kubectl get configmap -n trustee-operator-system \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.annotations.kbs\.confidentialcontainers\.org/migrated-from-v1\.1\.0}{"\n"}{end}'

# Example output:
# kbs-config                    v1.2.0
# rvps-reference-values         v1.2.0
# resource-policy               v1.2.0
```
