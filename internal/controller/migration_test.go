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
			name: "Already migrated (has both old and new)",
			inputData: map[string]string{
				"reference-values.json": `[{"name": "svn", "value": 1}]`,
				"reference_value":       `{"svn": {"value": 1}}`,
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

[attestation_service]
type = "coco_as_builtin"

  [attestation_service.rvps_config]
  type = "BuiltIn"

    [attestation_service.rvps_config.storage]
    type = "LocalJson"
    file_path = "/opt/confidential-containers/rvps/reference-values/reference-values.json"

[[plugins]]
name = "resource"
type = "LocalFs"
dir_path = "/opt/confidential-containers/kbs/repository"

[policy_engine]
policy_path = "/opt/confidential-containers/opa/policy.rego"`

	tests := []struct {
		name           string
		inputData      map[string]string
		expectMigrated bool
		checkNewPaths  bool
	}{
		{
			name: "Old format KBS config with v1.1.0 paths",
			inputData: map[string]string{
				"kbs-config.toml": oldKbsConfigToml,
			},
			expectMigrated: true,
			checkNewPaths:  true,
		},
		{
			name: "Already migrated KBS config",
			inputData: map[string]string{
				"kbs-config.toml": `dir_path = "/opt/confidential-containers/storage/repository"`,
			},
			expectMigrated: true,
			checkNewPaths:  false,
		},
		{
			name: "Empty KBS config",
			inputData: map[string]string{
				"kbs-config.toml": "",
			},
			expectMigrated: true,
			checkNewPaths:  false,
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
					Name:      "test-kbs-config",
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

			// Verify migration annotation
			updatedConfigMap := &corev1.ConfigMap{}
			err = fakeClient.Get(context.Background(),
				types.NamespacedName{Name: "test-kbs-config", Namespace: "default"},
				updatedConfigMap)
			if err != nil {
				t.Fatalf("Failed to get updated ConfigMap: %v", err)
			}

			if tt.expectMigrated {
				if updatedConfigMap.Annotations[MigrationAnnotation] != MigrationVersion {
					t.Errorf("Expected migration annotation, got %v", updatedConfigMap.Annotations)
				}
			}

			// Verify new paths are present if migration happened
			if tt.checkNewPaths {
				toml := updatedConfigMap.Data["kbs-config.toml"]

				// Check that old paths are replaced with new ones
				if containsString(toml, `/opt/confidential-containers/kbs/repository`) {
					t.Errorf("Old repository path still present in migrated TOML")
				}
				if containsString(toml, `/opt/confidential-containers/opa/policy.rego`) {
					t.Errorf("Old policy path still present in migrated TOML")
				}
				if containsString(toml, `type = "LocalJson"`) && !containsString(toml, `storage_type`) {
					t.Errorf("Old RVPS type field not migrated to storage_type")
				}

				// Check new paths are present
				if !containsString(toml, `/opt/confidential-containers/storage/repository`) {
					t.Errorf("New repository path not found in migrated TOML")
				}
				if !containsString(toml, `/opt/confidential-containers/storage/kbs/resource-policy.rego`) {
					t.Errorf("New policy path not found in migrated TOML")
				}
				if !containsString(toml, `storage_type = "LocalJson"`) {
					t.Errorf("New RVPS storage_type field not found in migrated TOML")
				}
				if !containsString(toml, `file_dir_path`) {
					t.Errorf("New RVPS file_dir_path field not found in migrated TOML")
				}
			}
		})
	}
}

func TestMigrateResourcePolicyConfigMap(t *testing.T) {
	tests := []struct {
		name           string
		inputData      map[string]string
		expectMigrated bool
		expectNewKey   bool
	}{
		{
			name: "Old format with policy.rego",
			inputData: map[string]string{
				"policy.rego": "package policy\ndefault allow = false",
			},
			expectMigrated: true,
			expectNewKey:   true,
		},
		{
			name: "New format with resource-policy.rego",
			inputData: map[string]string{
				"resource-policy.rego": "package policy\ndefault allow = false",
			},
			expectMigrated: true,
			expectNewKey:   false,
		},
		{
			name: "Both old and new keys present",
			inputData: map[string]string{
				"policy.rego":          "package policy\ndefault allow = false",
				"resource-policy.rego": "package policy\ndefault allow = false",
			},
			expectMigrated: true,
			expectNewKey:   false,
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
					Name:      "test-resource-policy",
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

			// Verify migration
			updatedConfigMap := &corev1.ConfigMap{}
			err = fakeClient.Get(context.Background(),
				types.NamespacedName{Name: "test-resource-policy", Namespace: "default"},
				updatedConfigMap)
			if err != nil {
				t.Fatalf("Failed to get updated ConfigMap: %v", err)
			}

			if tt.expectMigrated {
				if updatedConfigMap.Annotations[MigrationAnnotation] != MigrationVersion {
					t.Errorf("Expected migration annotation, got %v", updatedConfigMap.Annotations)
				}
			}

			if tt.expectNewKey {
				if _, hasNew := updatedConfigMap.Data["resource-policy.rego"]; !hasNew {
					t.Errorf("Expected new format key 'resource-policy.rego' to be created")
				}
			}
		})
	}
}
