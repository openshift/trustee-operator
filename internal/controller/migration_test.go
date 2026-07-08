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
	"testing"

	confidentialcontainersorgv1alpha1 "github.com/confidential-containers/trustee-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMigrateRvpsConfigMap(t *testing.T) {
	tests := []struct {
		name           string
		inputData      map[string]string
		expectMigrated bool
		expectError    bool
	}{
		{
			name: "Old format with reference-values.json",
			inputData: map[string]string{
				"reference-values.json": `[
					{
						"name": "svn",
						"expiration": "2026-01-01T00:00:00Z",
						"value": 1
					}
				]`,
			},
			expectMigrated: true,
			expectError:    false,
		},
		{
			name: "New format only",
			inputData: map[string]string{
				"reference_value": `{"svn": {"value": 1}}`,
			},
			expectMigrated: true,
			expectError:    false,
		},
		{
			name:           "Empty ConfigMap",
			inputData:      map[string]string{},
			expectMigrated: false, // Empty ConfigMap doesn't get migrated
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client with the test ConfigMap
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = confidentialcontainersorgv1alpha1.AddToScheme(scheme)

			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rvps-configmap",
					Namespace: "default",
				},
				Data: tt.inputData,
			}

			kbsConfig := &confidentialcontainersorgv1alpha1.KbsConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-kbsconfig",
					Namespace: "default",
				},
				Spec: confidentialcontainersorgv1alpha1.KbsConfigSpec{
					KbsRvpsRefValuesConfigMapName: "test-rvps-configmap",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(configMap, kbsConfig).
				Build()

			// Create reconciler
			r := &KbsConfigReconciler{
				Client:    fakeClient,
				Scheme:    scheme,
				Recorder:  &events.FakeRecorder{},
				kbsConfig: kbsConfig,
				log:       logr.Discard(),
				namespace: "default",
			}

			// Run migration
			err := r.migrateRvpsConfigMap(context.Background())

			// Check error
			if (err != nil) != tt.expectError {
				t.Errorf("migrateRvpsConfigMap() error = %v, expectError %v", err, tt.expectError)
				return
			}

			// Verify migration annotation was added
			updatedConfigMap := &corev1.ConfigMap{}
			err = fakeClient.Get(context.Background(),
				types.NamespacedName{Name: "test-rvps-configmap", Namespace: "default"},
				updatedConfigMap)
			if err != nil {
				t.Fatalf("Failed to get updated ConfigMap: %v", err)
			}

			if tt.expectMigrated {
				if updatedConfigMap.Annotations[MigrationAnnotation] != MigrationVersion {
					t.Errorf("Expected migration annotation, got %v", updatedConfigMap.Annotations)
				}

				// If old format existed, verify new format was created with base64 encoding
				if _, hasOld := tt.inputData["reference-values.json"]; hasOld {
					newValue, hasNew := updatedConfigMap.Data["reference_value"]
					if !hasNew {
						t.Errorf("Expected new format key 'reference_value' to be created")
					}

					// Verify the new format is base64-encoded
					// Parse the new format JSON
					var newFormat map[string]string
					err := json.Unmarshal([]byte(newValue), &newFormat)
					if err != nil {
						t.Errorf("Failed to parse new format JSON: %v", err)
					}

					// Verify "svn" key exists and is base64-encoded
					if encodedValue, ok := newFormat["svn"]; ok {
						// Try to decode it - should be valid base64
						decodedBytes, err := base64.StdEncoding.DecodeString(encodedValue)
						if err != nil {
							t.Errorf("Expected base64-encoded value, got error decoding: %v", err)
						}

						// Decoded value should be valid JSON
						var decodedJSON map[string]any
						err = json.Unmarshal(decodedBytes, &decodedJSON)
						if err != nil {
							t.Errorf("Expected decoded base64 to be valid JSON, got error: %v", err)
						}

						// Verify fields exist in decoded JSON (WITH "name" field)
						if _, hasName := decodedJSON["name"]; !hasName {
							t.Errorf("Expected 'name' field to be present in value, but it's missing")
						}
						if _, hasValue := decodedJSON["value"]; !hasValue {
							t.Errorf("Expected 'value' field to exist in decoded JSON")
						}
						// Verify the name field matches the key
						if decodedJSON["name"] != "svn" {
							t.Errorf("Expected 'name' field to be %q, got %q", "svn", decodedJSON["name"])
						}
					}
				}
			}
		})
	}
}

func TestMigrateKbsConfigMap(t *testing.T) {
	oldKbsConfigToml := `[http_server]
sockets = ["0.0.0.0:8080"]
insecure_http = false
private_key = "/etc/https-key/privateKey"
certificate = "/etc/https-cert/certificate"
worker_count = 4

[admin]
type = "DenyAll"
insecure_api = false
auth_public_key = "/etc/auth-secret/publicKey"

[attestation_token]
insecure_key = false
attestation_token_type = "CoCo"
trusted_certs_paths = ["/etc/attestation-cert/token.crt"]

[attestation_service]
type = "coco_as_builtin"
work_dir = "/opt/confidential-containers/attestation-service"
policy_engine = "opa"

[attestation_service.attestation_token_broker]
type = "Ear"
policy_dir = "/opt/confidential-containers/attestation-service/policies"

[attestation_service.attestation_token_config]
duration_min = 5

[attestation_service.rvps_config]
type = "BuiltIn"

[attestation_service.verifier_config.snp_verifier]
# Configure VCEK sources to try, in order. Defaults to [KDS].
vcek_sources = [
    { type = "OfflineStore" },
    { type = "KDS" }
]

[attestation_service.rvps_config.storage]
type = "LocalJson"
file_path = "/opt/confidential-containers/rvps/reference-values/reference-values.json"

[attestation_service.verifier_config.nvidia_verifier]
type = "Remote"
verifier_url = "https://nras.attestation.nvidia.com/v4/attest"

[attestation_service.attestation_token_broker.signer]
key_path = "/etc/attestation-key/token.key"
cert_path = "/etc/attestation-cert/token.crt"

[[plugins]]
name = "resource"
type = "LocalFs"
dir_path = "/opt/confidential-containers/kbs/repository"

[policy_engine]
policy_path = "/opt/confidential-containers/opa/policy.rego"`

	tests := []struct {
		name             string
		inputData        map[string]string
		inputAnnotations map[string]string
		expectDeleted    bool
		expectPreserved  bool
	}{
		{
			name: "Old format KBS config with v1.1.0 paths - should delete and save plugins",
			inputData: map[string]string{
				"kbs-config.toml": oldKbsConfigToml,
			},
			inputAnnotations: nil,
			expectDeleted:    true,
			expectPreserved:  false,
		},
		{
			name: "Already migrated KBS config - should preserve",
			inputData: map[string]string{
				"kbs-config.toml": `dir_path = "/opt/confidential-containers/storage/repository"`,
			},
			inputAnnotations: map[string]string{
				MigrationAnnotation: MigrationVersion,
			},
			expectDeleted:   false,
			expectPreserved: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client with the test ConfigMap
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = confidentialcontainersorgv1alpha1.AddToScheme(scheme)

			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-kbs-config",
					Namespace:   "default",
					Annotations: tt.inputAnnotations,
				},
				Data: tt.inputData,
			}

			kbsConfig := &confidentialcontainersorgv1alpha1.KbsConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-kbsconfig",
					Namespace: "default",
				},
				Spec: confidentialcontainersorgv1alpha1.KbsConfigSpec{
					KbsConfigMapName: "test-kbs-config",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(configMap, kbsConfig).
				Build()

			// Create reconciler
			r := &KbsConfigReconciler{
				Client:    fakeClient,
				Scheme:    scheme,
				Recorder:  &events.FakeRecorder{},
				kbsConfig: kbsConfig,
				log:       logr.Discard(),
				namespace: "default",
			}

			// Run migration
			err := r.migrateKbsConfigMap(context.Background())
			if err != nil {
				t.Fatalf("migrateKbsConfigMap() error = %v", err)
			}

			// Verify migration behavior
			updatedConfigMap := &corev1.ConfigMap{}
			err = fakeClient.Get(context.Background(),
				types.NamespacedName{Name: "test-kbs-config", Namespace: "default"},
				updatedConfigMap)

			if tt.expectDeleted {
				// ConfigMap without migration annotation should be deleted
				if !k8serrors.IsNotFound(err) {
					t.Errorf("Expected ConfigMap to be deleted for migration, but it still exists")
				}

				// Verify backup ConfigMap was created
				backupConfigMap := &corev1.ConfigMap{}
				backupName := "test-kbs-config" + BackupSuffix
				err = fakeClient.Get(context.Background(),
					types.NamespacedName{Name: backupName, Namespace: "default"},
					backupConfigMap)
				if err != nil {
					t.Fatalf("Expected backup ConfigMap %s to exist, got error: %v", backupName, err)
				}

				// Verify backup has the original data
				if _, hasToml := backupConfigMap.Data["kbs-config.toml"]; !hasToml {
					t.Errorf("Expected backup ConfigMap to have kbs-config.toml")
				}

				// Verify backup has metadata annotations
				if backupOf, ok := backupConfigMap.Annotations["kbs.confidentialcontainers.org/backup-of"]; !ok || backupOf != "test-kbs-config" {
					t.Errorf("Expected backup ConfigMap to have backup-of annotation pointing to original")
				}
			}

			if tt.expectPreserved {
				// ConfigMap with migration annotation should be preserved
				if err != nil {
					t.Fatalf("Failed to get ConfigMap: %v", err)
				}
				if updatedConfigMap.Annotations[MigrationAnnotation] != MigrationVersion {
					t.Errorf("Expected migration annotation to be preserved, got %v", updatedConfigMap.Annotations)
				}
			}
		})
	}
}

func TestTransformPluginsToV12(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name: "LocalFs transforms to kvstorage",
			input: []string{`[[plugins]]
name = "resource"
type = "LocalFs"
dir_path = "/opt/confidential-containers/kbs/repository"`},
			expected: []string{`[[plugins]]
name = "resource"
storage_backend_type = "kvstorage"`},
		},
		{
			name: "Vault transforms type to storage_backend_type",
			input: []string{`[[plugins]]
name = "resource"
type = "Vault"
vault_address = "https://vault.example.com:8200"
vault_token = "s.mytoken123"
vault_path = "secret/data/kbs"`},
			expected: []string{`[[plugins]]
name = "resource"
storage_backend_type = "Vault"
vault_address = "https://vault.example.com:8200"
vault_token = "s.mytoken123"
vault_path = "secret/data/kbs"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformPluginsToV12(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d plugins, got %d", len(tt.expected), len(result))
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("Plugin %d mismatch.\nExpected:\n%s\n\nGot:\n%s", i, tt.expected[i], result[i])
				}
			}
		})
	}
}

func TestRemovePluginsSections(t *testing.T) {
	input := `[http_server]
sockets = ["0.0.0.0:8080"]

[[plugins]]
name = "resource"
storage_backend_type = "kvstorage"

[policy_engine]
policy_path = "/opt/confidential-containers/storage/kbs/resource-policy.rego"`

	expected := `[http_server]
sockets = ["0.0.0.0:8080"]

[policy_engine]
policy_path = "/opt/confidential-containers/storage/kbs/resource-policy.rego"`

	result := removePluginsSections(input)
	if result != expected {
		t.Errorf("removePluginsSections failed.\nExpected:\n%s\n\nGot:\n%s", expected, result)
	}
}

func TestMigrateResourcePolicyConfigMap(t *testing.T) {
	tests := []struct {
		name             string
		inputData        map[string]string
		inputAnnotations map[string]string
		expectDeleted    bool
		expectPreserved  bool
	}{
		{
			name: "Old format without migration annotation - should delete",
			inputData: map[string]string{
				"policy.rego": "package policy\ndefault allow = false",
			},
			inputAnnotations: nil,
			expectDeleted:    true,
			expectPreserved:  false,
		},
		{
			name: "New format without migration annotation - should delete",
			inputData: map[string]string{
				"resource-policy.rego": "package policy\ndefault allow = false",
			},
			inputAnnotations: nil,
			expectDeleted:    true,
			expectPreserved:  false,
		},
		{
			name: "Already migrated - should preserve",
			inputData: map[string]string{
				"resource-policy.rego": "package policy\ndefault allow = false",
			},
			inputAnnotations: map[string]string{
				MigrationAnnotation: MigrationVersion,
			},
			expectDeleted:   false,
			expectPreserved: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client with the test ConfigMap
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = confidentialcontainersorgv1alpha1.AddToScheme(scheme)

			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-resource-policy",
					Namespace:   "default",
					Annotations: tt.inputAnnotations,
				},
				Data: tt.inputData,
			}

			kbsConfig := &confidentialcontainersorgv1alpha1.KbsConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-kbsconfig",
					Namespace: "default",
				},
				Spec: confidentialcontainersorgv1alpha1.KbsConfigSpec{
					KbsResourcePolicyConfigMapName: "test-resource-policy",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(configMap, kbsConfig).
				Build()

			// Create reconciler
			r := &KbsConfigReconciler{
				Client:    fakeClient,
				Scheme:    scheme,
				Recorder:  &events.FakeRecorder{},
				kbsConfig: kbsConfig,
				log:       logr.Discard(),
				namespace: "default",
			}

			// Run migration
			err := r.migrateResourcePolicyConfigMap(context.Background())
			if err != nil {
				t.Fatalf("migrateResourcePolicyConfigMap() error = %v", err)
			}

			// Verify migration behavior
			updatedConfigMap := &corev1.ConfigMap{}
			err = fakeClient.Get(context.Background(),
				types.NamespacedName{Name: "test-resource-policy", Namespace: "default"},
				updatedConfigMap)

			if tt.expectDeleted {
				// ConfigMap without migration annotation should be deleted
				if !k8serrors.IsNotFound(err) {
					t.Errorf("Expected ConfigMap to be deleted for migration, but it still exists")
				}
			}

			if tt.expectPreserved {
				// ConfigMap with migration annotation should be preserved
				if err != nil {
					t.Fatalf("Failed to get ConfigMap: %v", err)
				}
				if updatedConfigMap.Annotations[MigrationAnnotation] != MigrationVersion {
					t.Errorf("Expected migration annotation to be preserved, got %v", updatedConfigMap.Annotations)
				}
			}
		})
	}
}
