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
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Annotation to mark ConfigMaps that have been migrated
	MigrationAnnotation = "kbs.confidentialcontainers.org/migrated-from-v1.1.0"
	MigrationVersion    = "v1.2.0"
	// Suffix for backup ConfigMaps created during migration
	BackupSuffix = ".v1.1"
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

// migrateKbsConfigMap migrates the KBS config by:
// 1. Creating a backup ConfigMap with .v1.1 suffix
// 2. Deleting the original ConfigMap
// 3. On next reconciliation, merging plugins from backup into recreated v1.2 ConfigMap
// 4. Deleting the backup ConfigMap after successful merge
func (r *KbsConfigReconciler) migrateKbsConfigMap(ctx context.Context) error {
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{
		Namespace: r.namespace,
		Name:      r.kbsConfig.Spec.KbsConfigMapName,
	}, configMap)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			// ConfigMap doesn't exist - check if we have a backup to merge
			backupName := r.kbsConfig.Spec.KbsConfigMapName + BackupSuffix
			backupConfigMap := &corev1.ConfigMap{}
			backupErr := r.Get(ctx, client.ObjectKey{
				Namespace: r.namespace,
				Name:      backupName,
			}, backupConfigMap)

			if backupErr == nil {
				r.log.Info("KBS config ConfigMap not found but backup exists - waiting for recreation")
				// ConfigMap will be recreated by TrusteeConfig controller
				// We'll merge plugins in the next reconciliation
				return nil
			}

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

	// Check if we have a backup from a previous migration attempt
	backupName := r.kbsConfig.Spec.KbsConfigMapName + BackupSuffix
	backupConfigMap := &corev1.ConfigMap{}
	backupErr := r.Get(ctx, client.ObjectKey{
		Namespace: r.namespace,
		Name:      backupName,
	}, backupConfigMap)

	if backupErr == nil {
		// We have a backup and the ConfigMap exists (recreated by TrusteeConfig)
		// This means we're in step 2: merge plugins from backup
		r.log.Info("Merging plugins from backup into recreated v1.2 KBS config", "name", configMap.Name, "backup", backupName)
		return r.mergePluginsFromBackup(ctx, configMap, backupConfigMap)
	}

	// This is step 1: Create backup and delete original ConfigMap
	if _, hasToml := configMap.Data["kbs-config.toml"]; !hasToml {
		r.log.V(1).Info("KBS config ConfigMap has no kbs-config.toml, adding migration annotation", "name", configMap.Name)
		return r.addMigrationAnnotation(ctx, configMap)
	}

	r.log.Info("Creating backup of v1.1 KBS config for migration", "name", configMap.Name, "backup", backupName)

	// Create backup ConfigMap
	err = r.createConfigMapBackup(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to create backup of KBS config ConfigMap: %w", err)
	}

	r.log.Info("Backup created, deleting original ConfigMap", "name", configMap.Name)

	// Delete the ConfigMap so TrusteeConfig recreates it in v1.2 format
	err = r.Delete(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to delete KBS config ConfigMap for migration: %w", err)
	}

	r.log.Info("Deleted KBS config ConfigMap for v1.2 recreation", "name", configMap.Name)
	r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeNormal, "ConfigMapDeleted",
		"ConfigMapMigration", "Deleted KBS config ConfigMap %s - will recreate in v1.2 format and merge plugins from backup %s", configMap.Name, backupName)

	return nil
}

// extractPluginsSections extracts all [[plugins]] sections from v1.1 TOML
// Returns a slice of plugin section strings
func extractPluginsSections(toml string) []string {
	var plugins []string
	lines := strings.Split(toml, "\n")
	var currentPlugin []string
	inPlugin := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check if this is the start of a [[plugins]] section
		if trimmed == "[[plugins]]" {
			// Save previous plugin if exists
			if inPlugin && len(currentPlugin) > 0 {
				plugins = append(plugins, strings.Join(currentPlugin, "\n"))
			}
			// Start new plugin
			currentPlugin = []string{line}
			inPlugin = true
			continue
		}

		// If we're in a plugin section
		if inPlugin {
			// Check if we've hit another section header (starts with '[')
			if strings.HasPrefix(trimmed, "[") {
				// End of plugin section
				plugins = append(plugins, strings.Join(currentPlugin, "\n"))
				currentPlugin = nil
				inPlugin = false
				continue
			}
			// Add line to current plugin
			currentPlugin = append(currentPlugin, line)
		}
	}

	// Don't forget the last plugin if we ended in one
	if inPlugin && len(currentPlugin) > 0 {
		plugins = append(plugins, strings.Join(currentPlugin, "\n"))
	}

	return plugins
}

// removePluginsSections removes all [[plugins]] sections from TOML
// Used to strip default plugins from v1.2 template before merging saved v1.1 plugins
func removePluginsSections(toml string) string {
	var result []string
	lines := strings.Split(toml, "\n")
	inPlugin := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check if this is the start of a [[plugins]] section
		if trimmed == "[[plugins]]" {
			inPlugin = true
			continue
		}

		// If we're in a plugin section
		if inPlugin {
			// Check if we've hit another section header (starts with '[')
			if strings.HasPrefix(trimmed, "[") {
				// End of plugin section, include this new section header
				inPlugin = false
				result = append(result, line)
				continue
			}
			// Skip lines inside plugin section
			continue
		}

		// Not in plugin section, keep the line
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// mergePluginsFromBackup merges [[plugins]] sections from backup ConfigMap into recreated v1.2 KBS config
func (r *KbsConfigReconciler) mergePluginsFromBackup(ctx context.Context, configMap *corev1.ConfigMap, backupConfigMap *corev1.ConfigMap) error {
	// Extract plugins from backup
	backupToml, hasToml := backupConfigMap.Data["kbs-config.toml"]
	if !hasToml {
		r.log.Info("Backup ConfigMap has no kbs-config.toml, adding migration annotation")
		// Clean up backup
		if err := r.Delete(ctx, backupConfigMap); err != nil {
			r.log.Info("Failed to delete backup ConfigMap", "name", backupConfigMap.Name, "err", err)
		}
		return r.addMigrationAnnotation(ctx, configMap)
	}

	// Extract [[plugins]] sections from backup
	pluginsSections := extractPluginsSections(backupToml)

	if len(pluginsSections) == 0 {
		r.log.Info("No plugins in backup to merge, adding migration annotation")
		// Clean up backup
		if err := r.Delete(ctx, backupConfigMap); err != nil {
			r.log.Info("Failed to delete backup ConfigMap", "name", backupConfigMap.Name, "err", err)
		}
		return r.addMigrationAnnotation(ctx, configMap)
	}

	// Get current TOML from recreated ConfigMap
	tomlData, hasToml := configMap.Data["kbs-config.toml"]
	if !hasToml {
		return fmt.Errorf("recreated ConfigMap has no kbs-config.toml")
	}

	// Remove any existing [[plugins]] sections from the v1.2 template
	// to avoid duplicates when we append the saved v1.1 plugins
	tomlWithoutPlugins := removePluginsSections(tomlData)

	// Transform plugins from v1.1 to v1.2 format and append
	transformedPlugins := transformPluginsToV12(pluginsSections)
	mergedToml := tomlWithoutPlugins + "\n\n" + strings.Join(transformedPlugins, "\n\n")

	// Update ConfigMap
	configMap.Data["kbs-config.toml"] = mergedToml

	// Add migration annotation
	if configMap.Annotations == nil {
		configMap.Annotations = make(map[string]string)
	}
	configMap.Annotations[MigrationAnnotation] = MigrationVersion

	err := r.Update(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to update ConfigMap with merged plugins: %w", err)
	}

	r.log.Info("Successfully merged plugins into v1.2 KBS config", "count", len(pluginsSections))
	r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeNormal, "ConfigMapMigrated",
		"ConfigMapMigrated", "Merged %d plugin(s) from backup %s into v1.2 KBS config ConfigMap %s (backup preserved)", len(pluginsSections), backupConfigMap.Name, configMap.Name)

	return nil
}

// transformPluginsToV12 transforms [[plugins]] sections from v1.1 to v1.2 format
// v1.1: type = "LocalFs" | "Vault"
// v1.2: storage_backend_type = "kvstorage" | "Vault"
func transformPluginsToV12(pluginsSections []string) []string {
	var transformed []string

	for _, plugin := range pluginsSections {
		// For resource plugin with LocalFs, transform to storage_backend_type = "kvstorage"
		if strings.Contains(plugin, `name = "resource"`) && strings.Contains(plugin, `type = "LocalFs"`) {
			// Replace type = "LocalFs" with storage_backend_type = "kvstorage"
			plugin = strings.ReplaceAll(plugin, `type = "LocalFs"`, `storage_backend_type = "kvstorage"`)
			// Remove dir_path if present (v1.2 uses storage_backend configuration)
			lines := strings.Split(plugin, "\n")
			var filtered []string
			for _, line := range lines {
				if !strings.Contains(line, "dir_path =") {
					filtered = append(filtered, line)
				}
			}
			plugin = strings.Join(filtered, "\n")
		} else if strings.Contains(plugin, `name = "resource"`) && strings.Contains(plugin, `type = "Vault"`) {
			// For Vault, just rename the field from type to storage_backend_type
			plugin = strings.ReplaceAll(plugin, `type = "Vault"`, `storage_backend_type = "Vault"`)
		}
		// Append the transformed plugin
		transformed = append(transformed, plugin)
	}

	return transformed
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

	// Create backup before modifying
	err = r.createConfigMapBackup(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to create backup of RVPS ConfigMap: %w", err)
	}

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

// migrateResourcePolicyConfigMap migrates resource policy ConfigMap
// If the ConfigMap exists but doesn't have the migration annotation,
// delete it so the reconciliation loop recreates it with updated content.
// This ensures the policy content matches the current profile and settings.
func (r *KbsConfigReconciler) migrateResourcePolicyConfigMap(ctx context.Context) error {
	return r.migrateGeneratedPolicyConfigMap(ctx,
		r.kbsConfig.Spec.KbsResourcePolicyConfigMapName,
		"resource policy")
}

// migrateAttestationPolicyConfigMap migrates CPU attestation policy ConfigMap
// If the ConfigMap exists but doesn't have the migration annotation,
// delete it so the reconciliation loop recreates it with updated content.
func (r *KbsConfigReconciler) migrateAttestationPolicyConfigMap(ctx context.Context) error {
	return r.migrateGeneratedPolicyConfigMap(ctx,
		r.kbsConfig.Spec.KbsAttestationPolicyConfigMapName,
		"CPU attestation policy")
}

// migrateGpuAttestationPolicyConfigMap migrates GPU attestation policy ConfigMap
// If the ConfigMap exists but doesn't have the migration annotation,
// delete it so the reconciliation loop recreates it with updated content.
func (r *KbsConfigReconciler) migrateGpuAttestationPolicyConfigMap(ctx context.Context) error {
	return r.migrateGeneratedPolicyConfigMap(ctx,
		r.kbsConfig.Spec.KbsGpuAttestationPolicyConfigMapName,
		"GPU attestation policy")
}

// migrateGeneratedPolicyConfigMap handles migration of dynamically generated policy ConfigMaps
// These ConfigMaps are generated based on the profile and other settings, so if they exist
// without a migration annotation, we create a backup and delete them, letting the reconciliation
// loop recreate them with updated content.
func (r *KbsConfigReconciler) migrateGeneratedPolicyConfigMap(ctx context.Context, configMapName, description string) error {
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

	// ConfigMap exists but has no migration annotation - create backup and delete it
	// The reconciliation loop will recreate it with updated content
	r.log.Info("Creating backup and deleting policy ConfigMap to trigger recreation", "name", configMap.Name, "type", description)

	// Create backup before deletion
	err = r.createConfigMapBackup(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to create backup of %s ConfigMap: %w", description, err)
	}

	err = r.Delete(ctx, configMap)
	if err != nil {
		return fmt.Errorf("failed to delete %s ConfigMap for migration: %w", description, err)
	}

	r.log.Info("Successfully deleted non-migrated ConfigMap", "name", configMap.Name, "type", description)
	r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeNormal, "ConfigMapDeleted",
		"ConfigMapMigration", "Deleted %s ConfigMap %s for migration - will be recreated with updated content (backup: %s)", description, configMap.Name, configMap.Name+BackupSuffix)

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

// base64Encode encodes data to base64 string (standard encoding, no line wrapping)
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// createConfigMapBackup creates a backup copy of a ConfigMap with .v1.1 suffix
// The backup is preserved after migration for safety and manual recovery if needed
func (r *KbsConfigReconciler) createConfigMapBackup(ctx context.Context, configMap *corev1.ConfigMap) error {
	backupName := configMap.Name + BackupSuffix

	// Check if backup already exists
	existingBackup := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{
		Namespace: configMap.Namespace,
		Name:      backupName,
	}, existingBackup)

	if err == nil {
		r.log.Info("Backup ConfigMap already exists, skipping creation", "name", backupName)
		return nil
	}

	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to check for existing backup: %w", err)
	}

	// Create backup ConfigMap
	backup := &corev1.ConfigMap{
		ObjectMeta: configMap.ObjectMeta,
		Data:       configMap.Data,
		BinaryData: configMap.BinaryData,
	}
	backup.Name = backupName
	backup.ResourceVersion = ""
	backup.UID = ""
	backup.CreationTimestamp = configMap.CreationTimestamp

	if backup.Annotations == nil {
		backup.Annotations = make(map[string]string)
	}
	backup.Annotations["kbs.confidentialcontainers.org/backup-of"] = configMap.Name
	backup.Annotations["kbs.confidentialcontainers.org/backup-timestamp"] = configMap.CreationTimestamp.String()

	err = r.Create(ctx, backup)
	if err != nil {
		return fmt.Errorf("failed to create backup ConfigMap: %w", err)
	}

	r.log.Info("Created backup ConfigMap (will be preserved)", "name", backupName, "original", configMap.Name)
	r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeNormal, "ConfigMapBackupCreated",
		"ConfigMapMigration", "Created backup %s of ConfigMap %s before migration (preserved for safety)", backupName, configMap.Name)

	return nil
}
