/*
Copyright Confidential Containers Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Annotation to mark ConfigMaps that have been migrated
	MigrationAnnotation = "kbs.confidentialcontainers.org/migrated-from-v1.1.0"
	MigrationVersion    = "v1.2.0"
)

// migrateConfigMapsIfNeeded checks for old ConfigMap formats and migrates them automatically
// This provides a seamless upgrade path from v1.1.0 to v1.2.0+
func (r *KbsConfigReconciler) migrateConfigMapsIfNeeded(ctx context.Context) error {
	r.log.Info("Checking for ConfigMap migrations")

	// Migrate KBS config TOML ConfigMap (most critical - has TOML structure changes)
	if r.kbsConfig.Spec.KbsConfigMapName != "" {
		err := r.migrateKbsConfigMap(ctx)
		if err != nil {
			r.log.Info("Failed to migrate KBS config ConfigMap", "err", err)
			return err
		}
	}

	// Migrate RVPS reference values ConfigMap
	if r.kbsConfig.Spec.KbsRvpsRefValuesConfigMapName != "" {
		err := r.migrateRvpsConfigMap(ctx)
		if err != nil {
			r.log.Info("Failed to migrate RVPS ConfigMap", "err", err)
			return err
		}
	}

	// Migrate resource policy ConfigMap
	if r.kbsConfig.Spec.KbsResourcePolicyConfigMapName != "" {
		err := r.migrateResourcePolicyConfigMap(ctx)
		if err != nil {
			r.log.Info("Failed to migrate resource policy ConfigMap", "err", err)
			return err
		}
	}

	// Migrate attestation policy ConfigMap
	if r.kbsConfig.Spec.KbsAttestationPolicyConfigMapName != "" {
		err := r.migrateAttestationPolicyConfigMap(ctx)
		if err != nil {
			r.log.Info("Failed to migrate attestation policy ConfigMap", "err", err)
			return err
		}
	}

	// Migrate GPU attestation policy ConfigMap
	if r.kbsConfig.Spec.KbsGpuAttestationPolicyConfigMapName != "" {
		err := r.migrateGpuAttestationPolicyConfigMap(ctx)
		if err != nil {
			r.log.Info("Failed to migrate GPU attestation policy ConfigMap", "err", err)
			return err
		}
	}

	return nil
}

// migrateKbsConfigMap migrates the main KBS configuration TOML from old paths to new paths
// This handles TOML structure changes like storage directory consolidation
func (r *KbsConfigReconciler) migrateKbsConfigMap(ctx context.Context) error {
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{
		Namespace: r.namespace,
		Name:      r.kbsConfig.Spec.KbsConfigMapName,
	}, configMap)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.log.V(1).Info("KBS config ConfigMap not found, skipping migration")
			return nil
		}
		return err
	}

	// Check if already migrated
	if configMap.Annotations != nil {
		if _, exists := configMap.Annotations[MigrationAnnotation]; exists {
			r.log.V(1).Info("KBS config ConfigMap already migrated", "name", configMap.Name)
			return nil
		}
	}

	// Get the kbs-config.toml data
	tomlData, hasToml := configMap.Data["kbs-config.toml"]
	if !hasToml {
		r.log.V(1).Info("KBS config ConfigMap has no kbs-config.toml, adding migration annotation", "name", configMap.Name)
		return r.addMigrationAnnotation(ctx, configMap)
	}

	r.log.Info("Migrating KBS config TOML (applying transformations and adding annotation)", "name", configMap.Name)

	// Perform string replacements for path migrations
	// This is a simple approach - for complex TOML parsing we'd need a TOML library
	migratedToml := tomlData

	// Storage directory consolidation
	migratedToml = replaceString(migratedToml,
		`dir_path = "/opt/confidential-containers/kbs/repository"`,
		`dir_path = "/opt/confidential-containers/storage/repository"`)

	// Policy path migration
	migratedToml = replaceString(migratedToml,
		`policy_path = "/opt/confidential-containers/opa/policy.rego"`,
		`policy_path = "/opt/confidential-containers/storage/kbs/resource-policy.rego"`)

	// RVPS storage type field rename
	migratedToml = replaceString(migratedToml,
		`type = "LocalJson"`,
		`storage_type = "LocalJson"`)

	// Migrate [admin] section fields
	// Old v1.1: type = "DenyAll"
	// New v1.2: authorization_mode = "DenyAll"
	if containsString(migratedToml, "[admin]") {
		// Replace old 'type' field with new 'authorization_mode' field
		migratedToml = replaceString(migratedToml,
			`type = "DenyAll"`,
			`authorization_mode = "DenyAll"`)

		// Add missing authorization_mode field if not present and no 'type' was found
		if !containsString(migratedToml, "authorization_mode") {
			// Find [admin] section and add authorization_mode after the section header
			migratedToml = replaceString(migratedToml,
				"[admin]\n",
				"[admin]\nauthorization_mode = \"DenyAll\"\n")
			// Also handle case without trailing newline
			migratedToml = replaceString(migratedToml,
				"[admin]\r\n",
				"[admin]\r\nauthorization_mode = \"DenyAll\"\r\n")
		}
	}

	// Migrate [attestation_token] section fields
	// Old v1.1: insecure_key = true
	// New v1.2: insecure_header_jwk = true
	migratedToml = replaceString(migratedToml,
		`insecure_key = true`,
		`insecure_header_jwk = true`)
	migratedToml = replaceString(migratedToml,
		`insecure_key = false`,
		`insecure_header_jwk = false`)

	// Migrate old [[plugins]] LocalFs type field
	// Note: We do NOT change Vault or CosmosDB plugin configs - only LocalFs paths
	// Old v1.1 LocalFs: type = "LocalFs", dir_path = "/old/path"
	// New v1.2: storage_backend section handles this
	// However, we preserve the old format for backward compatibility
	// The new KBS can understand both formats

	// RVPS file_path to file_dir_path with new structure
	// Old: file_path = "/opt/confidential-containers/rvps/reference-values/reference-values.json"
	// New: file_dir_path = "/opt/confidential-containers/storage/local_json"
	if containsString(migratedToml, `file_path = "/opt/confidential-containers/rvps/reference-values`) {
		// Need to restructure the RVPS config section
		migratedToml = migrateRvpsStorageSection(migratedToml)
	}

	// Update ConfigMap with migrated TOML
	configMap.Data["kbs-config.toml"] = migratedToml

	// Add migration annotation
	if configMap.Annotations == nil {
		configMap.Annotations = make(map[string]string)
	}
	configMap.Annotations[MigrationAnnotation] = MigrationVersion

	err = r.Update(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to update migrated KBS config ConfigMap: %w", err)
	}

	r.log.Info("Successfully migrated KBS config ConfigMap", "name", configMap.Name)
	r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeNormal, "ConfigMapMigrated",
		"ConfigMapMigrated", "Migrated KBS config ConfigMap %s from v1.1.0 format to v1.2.0 format", configMap.Name)

	return nil
}

// migrateRvpsStorageSection migrates the RVPS storage configuration structure
// Old format:
//
//	[attestation_service.rvps_config.storage]
//	type = "LocalJson"
//	file_path = "/opt/confidential-containers/rvps/reference-values/reference-values.json"
//
// New format:
//
//	[attestation_service.rvps_config.storage]
//	storage_type = "LocalJson"
//
//	[attestation_service.rvps_config.storage.backends.local_json]
//	file_dir_path = "/opt/confidential-containers/storage/local_json"
func migrateRvpsStorageSection(toml string) string {
	// This is a simplified migration that handles the common case
	// For complex TOML structures, a proper TOML parser would be needed

	result := toml

	// Replace the old file_path line with new structure
	oldPattern := `file_path = "/opt/confidential-containers/rvps/reference-values/reference-values.json"`
	newPattern := `
        [attestation_service.rvps_config.storage.backends.local_json]
        file_dir_path = "/opt/confidential-containers/storage/local_json"`

	result = replaceString(result, oldPattern, newPattern)

	return result
}

// Helper functions for string operations
func containsString(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && findString(s, substr) >= 0
}

func findString(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func replaceString(s, old, new string) string {
	if !containsString(s, old) {
		return s
	}

	result := ""
	remaining := s

	for {
		index := findString(remaining, old)
		if index < 0 {
			result += remaining
			break
		}

		result += remaining[:index] + new
		remaining = remaining[index+len(old):]
	}

	return result
}

// migrateRvpsConfigMap migrates RVPS reference values from old format to new format
// Old format: reference-values.json with array structure (plain JSON)
// Example: [{"name": "svn", "expiration": "2027-01-01T00:00:00Z", "value": 1}]
//
// New format: reference_value with object structure (base64-encoded JSON values)
// Example: {"svn": "eyJleHBpcmF0aW9uIjoiMjAyNy0wMS0wMVQwMDowMDowMFoiLCJ2YWx1ZSI6MX0="}
func (r *KbsConfigReconciler) migrateRvpsConfigMap(ctx context.Context) error {
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{
		Namespace: r.namespace,
		Name:      r.kbsConfig.Spec.KbsRvpsRefValuesConfigMapName,
	}, configMap)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.log.V(1).Info("RVPS ConfigMap not found, skipping migration")
			return nil
		}
		return err
	}

	// Check if already migrated
	if configMap.Annotations != nil {
		if _, exists := configMap.Annotations[MigrationAnnotation]; exists {
			r.log.V(1).Info("RVPS ConfigMap already migrated", "name", configMap.Name)
			return nil
		}
	}

	// Get the old format data
	oldData, hasOldFormat := configMap.Data["reference-values.json"]

	if !hasOldFormat {
		// No old format found, just add migration annotation
		r.log.V(1).Info("RVPS ConfigMap has no old format data, adding migration annotation", "name", configMap.Name)
		return r.addMigrationAnnotation(ctx, configMap)
	}

	r.log.Info("Migrating RVPS ConfigMap from old format to new format", "name", configMap.Name)

	// Parse old format (array of objects)
	var oldFormat []map[string]any
	err = json.Unmarshal([]byte(oldData), &oldFormat)
	if err != nil {
		r.log.Info("Failed to parse old RVPS format, skipping migration", "err", err)
		return nil // Don't fail reconciliation on parse errors
	}

	// Convert to new format (object with base64-encoded values)
	newFormat := make(map[string]string)
	for _, item := range oldFormat {
		name, hasName := item["name"].(string)
		if !hasName {
			r.log.Info("Skipping RVPS entry without name field", "item", item)
			continue
		}

		// Keep the entire item including the "name" field in the base64-encoded value
		// The name field is used as the key in the new format AND kept in the value

		// Marshal the complete item to compact JSON (no whitespace)
		valueJSON, err := json.Marshal(item)
		if err != nil {
			r.log.Info("Failed to marshal RVPS entry, skipping", "name", name, "err", err)
			continue
		}

		// Base64 encode the compact JSON
		encodedValue := base64Encode(valueJSON)
		newFormat[name] = encodedValue
	}

	// Marshal new format to JSON
	newDataBytes, err := json.MarshalIndent(newFormat, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal new RVPS format: %w", err)
	}

	// Update ConfigMap with new format
	configMap.Data["reference_value"] = string(newDataBytes)
	// Remove old format - migration to v1.2 is complete
	delete(configMap.Data, "reference-values.json")

	// Add migration annotation
	if configMap.Annotations == nil {
		configMap.Annotations = make(map[string]string)
	}
	configMap.Annotations[MigrationAnnotation] = MigrationVersion

	err = r.Update(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to update migrated RVPS ConfigMap: %w", err)
	}

	r.log.Info("Successfully migrated RVPS ConfigMap", "name", configMap.Name)
	r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeNormal, "ConfigMapMigrated",
		"ConfigMapMigrated", "Migrated RVPS ConfigMap %s from v1.1.0 format to v1.2.0 format", configMap.Name)

	return nil
}

// migrateResourcePolicyConfigMap migrates resource policy from old key to new key
// Old format: policy.rego
// New format: resource-policy.rego
func (r *KbsConfigReconciler) migrateResourcePolicyConfigMap(ctx context.Context) error {
	return r.migrateConfigMapKey(ctx,
		r.kbsConfig.Spec.KbsResourcePolicyConfigMapName,
		"policy.rego",
		"resource-policy.rego",
		"resource policy")
}

// migrateAttestationPolicyConfigMap migrates attestation policy from old key to new key
// Old format: policy.rego
// New format: attestation-policy.rego (if changed, otherwise skip)
func (r *KbsConfigReconciler) migrateAttestationPolicyConfigMap(ctx context.Context) error {
	// Attestation policy typically doesn't need key migration, but check anyway
	return r.migrateConfigMapKey(ctx,
		r.kbsConfig.Spec.KbsAttestationPolicyConfigMapName,
		"policy.rego",
		"attestation-policy.rego",
		"attestation policy")
}

// migrateGpuAttestationPolicyConfigMap migrates GPU attestation policy
func (r *KbsConfigReconciler) migrateGpuAttestationPolicyConfigMap(ctx context.Context) error {
	// GPU attestation policy typically doesn't need key migration, but check anyway
	return r.migrateConfigMapKey(ctx,
		r.kbsConfig.Spec.KbsGpuAttestationPolicyConfigMapName,
		"policy.rego",
		"gpu-attestation-policy.rego",
		"GPU attestation policy")
}

// migrateConfigMapKey is a helper function to migrate a ConfigMap key name
func (r *KbsConfigReconciler) migrateConfigMapKey(ctx context.Context, configMapName, oldKey, newKey, description string) error {
	if configMapName == "" {
		return nil
	}

	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{
		Namespace: r.namespace,
		Name:      configMapName,
	}, configMap)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.log.V(1).Info("ConfigMap not found, skipping migration", "name", configMapName, "type", description)
			return nil
		}
		return err
	}

	// Check if already migrated
	if configMap.Annotations != nil {
		if _, exists := configMap.Annotations[MigrationAnnotation]; exists {
			r.log.V(1).Info("ConfigMap already migrated", "name", configMap.Name, "type", description)
			return nil
		}
	}

	// Get the old format data
	oldData, hasOldFormat := configMap.Data[oldKey]

	if !hasOldFormat {
		// No old format key found, just add migration annotation
		r.log.V(1).Info("ConfigMap has no old format key, adding migration annotation", "name", configMap.Name, "type", description, "oldKey", oldKey)
		return r.addMigrationAnnotation(ctx, configMap)
	}

	r.log.Info("Migrating ConfigMap key", "name", configMap.Name, "type", description, "from", oldKey, "to", newKey)

	// Copy data from old key to new key
	configMap.Data[newKey] = oldData
	// Remove old key - migration to v1.2 is complete
	delete(configMap.Data, oldKey)

	// Add migration annotation
	if configMap.Annotations == nil {
		configMap.Annotations = make(map[string]string)
	}
	configMap.Annotations[MigrationAnnotation] = MigrationVersion

	err = r.Update(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to update migrated %s ConfigMap: %w", description, err)
	}

	r.log.Info("Successfully migrated ConfigMap", "name", configMap.Name, "type", description)
	r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeNormal, "ConfigMapMigrated",
		"ConfigMapMigrated", "Migrated %s ConfigMap %s from v1.1.0 format to v1.2.0 format", description, configMap.Name)

	return nil
}

// addMigrationAnnotation adds the migration annotation to a ConfigMap that's already in the new format
func (r *KbsConfigReconciler) addMigrationAnnotation(ctx context.Context, configMap *corev1.ConfigMap) error {
	if configMap.Annotations == nil {
		configMap.Annotations = make(map[string]string)
	}

	// Check if already annotated
	if _, exists := configMap.Annotations[MigrationAnnotation]; exists {
		return nil
	}

	configMap.Annotations[MigrationAnnotation] = MigrationVersion
	err := r.Update(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to add migration annotation to ConfigMap %s: %w", configMap.Name, err)
	}

	return nil
}

// cleanupOldConfigMapKeys removes old format keys after successful migration
// This is optional and can be called manually or after a grace period
func (r *KbsConfigReconciler) cleanupOldConfigMapKeys(ctx context.Context, configMapName string, oldKeys []string) error {
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{
		Namespace: r.namespace,
		Name:      configMapName,
	}, configMap)

	if err != nil {
		return err
	}

	// Only cleanup if migration annotation is present
	if configMap.Annotations == nil || configMap.Annotations[MigrationAnnotation] == "" {
		r.log.V(1).Info("ConfigMap not migrated yet, skipping cleanup", "name", configMapName)
		return nil
	}

	changed := false
	for _, oldKey := range oldKeys {
		if _, exists := configMap.Data[oldKey]; exists {
			delete(configMap.Data, oldKey)
			changed = true
			r.log.Info("Removed old ConfigMap key", "name", configMapName, "key", oldKey)
		}
	}

	if changed {
		err = r.Update(ctx, configMap)
		if err != nil {
			return fmt.Errorf("failed to cleanup old ConfigMap keys: %w", err)
		}
		r.log.Info("Successfully cleaned up old ConfigMap keys", "name", configMapName)
	}

	return nil
}

// base64Encode encodes data to base64 string (standard encoding, no line wrapping)
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
