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
✅ **Creates backup ConfigMaps** with `.v1.1` suffix before any destructive operations  
✅ **RVPS and KBS Config**: Migrates content with format transformations  
✅ **Policy ConfigMaps** (resource, CPU, GPU): Deletes and recreates with fresh content  
✅ Adds migration annotation to track what's been migrated  
✅ Triggers pod restart to apply new configuration  
✅ Logs migration activity in operator logs  
✅ **Preserves backups** for safety and manual recovery if needed  

**Note**: Policy ConfigMaps (resource-policy, attestation-policy-cpu, attestation-policy-gpu) 
are regenerated during migration to ensure they match the current profile settings. 
Any customizations to these policies should be reapplied after migration.

### Backup ConfigMaps

For safety, the operator creates backup copies of all ConfigMaps before migration:

- **Naming**: Backups have `.v1.1` suffix (e.g., `kbs-config.v1.1`)
- **Metadata**: Each backup includes annotations tracking the original ConfigMap name and timestamp
- **Preserved**: Backups are kept after successful migration for manual recovery if needed
- **Cleanup**: Administrators can manually delete backups once they're confident the migration succeeded

To list all backup ConfigMaps:
```bash
kubectl get configmap -n trustee-operator-system | grep '\.v1\.1$'
```

To restore from a backup (if needed):
```bash
# Get the backup
kubectl get configmap <name>.v1.1 -n trustee-operator-system -o yaml > backup.yaml

# Edit to remove .v1.1 from the name and restore
sed -i 's/\.v1\.1$//' backup.yaml
kubectl apply -f backup.yaml
```

To clean up backups after confirming migration success:
```bash
kubectl delete configmap -n trustee-operator-system -l 'kbs.confidentialcontainers.org/backup-of'
# Or manually: kubectl delete configmap <name>.v1.1 -n trustee-operator-system
```  
