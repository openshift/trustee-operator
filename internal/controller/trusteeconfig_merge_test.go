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
	"testing"

	confidentialcontainersorgv1alpha1 "github.com/confidential-containers/trustee-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMergeKbsConfigSpecs_SecretResources(t *testing.T) {
	tests := []struct {
		name                     string
		generatedSecretResources []string
		manualSecretResources    []string
		expectedSecretResources  []string
		description              string
	}{
		{
			name:                     "Merge operator-generated and manual secrets",
			generatedSecretResources: []string{"attestation-status"},
			manualSecretResources:    []string{"my-custom-secret"},
			expectedSecretResources:  []string{"attestation-status", "my-custom-secret"},
			description:              "Should contain both operator-generated attestation-status and manual secret",
		},
		{
			name:                     "Preserve operator-generated secret when empty manual",
			generatedSecretResources: []string{"attestation-status"},
			manualSecretResources:    []string{},
			expectedSecretResources:  []string{"attestation-status"},
			description:              "Should preserve operator-generated secret when no manual secrets",
		},
		{
			name:                     "Preserve manual secrets when empty generated",
			generatedSecretResources: []string{},
			manualSecretResources:    []string{"my-custom-secret"},
			expectedSecretResources:  []string{"my-custom-secret"},
			description:              "Should preserve manual secrets when no generated secrets",
		},
		{
			name:                     "Deduplicate when same secret in both",
			generatedSecretResources: []string{"attestation-status", "shared-secret"},
			manualSecretResources:    []string{"shared-secret", "my-custom-secret"},
			expectedSecretResources:  []string{"attestation-status", "shared-secret", "my-custom-secret"},
			description:              "Should deduplicate when same secret appears in both lists",
		},
		{
			name:                     "Handle multiple operator-generated secrets",
			generatedSecretResources: []string{"attestation-status", "another-operator-secret"},
			manualSecretResources:    []string{"my-custom-secret"},
			expectedSecretResources:  []string{"attestation-status", "another-operator-secret", "my-custom-secret"},
			description:              "Should merge all secrets from both lists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client for the reconciler
			scheme := runtime.NewScheme()
			_ = confidentialcontainersorgv1alpha1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			// Create reconciler
			r := &TrusteeConfigReconciler{
				Client: fakeClient,
				Scheme: scheme,
				log:    logr.Discard(),
			}

			// Create generated and manual specs
			generatedSpec := confidentialcontainersorgv1alpha1.KbsConfigSpec{
				KbsSecretResources: tt.generatedSecretResources,
			}

			manualSpec := confidentialcontainersorgv1alpha1.KbsConfigSpec{
				KbsSecretResources: tt.manualSecretResources,
			}

			// Run merge
			merged := r.mergeKbsConfigSpecs(generatedSpec, manualSpec)

			// Verify that all expected secrets are present
			mergedMap := make(map[string]bool)
			for _, secret := range merged.KbsSecretResources {
				mergedMap[secret] = true
			}

			for _, expectedSecret := range tt.expectedSecretResources {
				if !mergedMap[expectedSecret] {
					t.Errorf("%s: expected secret %q not found in merged list. Got: %v",
						tt.description, expectedSecret, merged.KbsSecretResources)
				}
			}

			// Verify no unexpected secrets
			if len(merged.KbsSecretResources) != len(tt.expectedSecretResources) {
				t.Errorf("%s: expected %d secrets, got %d. Expected: %v, Got: %v",
					tt.description,
					len(tt.expectedSecretResources),
					len(merged.KbsSecretResources),
					tt.expectedSecretResources,
					merged.KbsSecretResources)
			}
		})
	}
}

func TestMergeKbsConfigSpecs_UpgradeMigrationScenario(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = confidentialcontainersorgv1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &TrusteeConfigReconciler{
		Client: fakeClient,
		Scheme: scheme,
		log:    logr.Discard(),
	}

	// Simulate upgrade from v1.1 to v1.2:
	// - v1.1 KbsConfig had user's custom secrets
	// - v1.2 TrusteeConfig generates spec with attestation-status
	// - After merge, both should be present

	// This is what TrusteeConfig generates in v1.2 (new format with attestation-status)
	generatedSpec := confidentialcontainersorgv1alpha1.KbsConfigSpec{
		KbsSecretResources: []string{"attestation-status"},
	}

	// This is what was in the existing v1.1 KbsConfig (user's custom secrets)
	manualSpec := confidentialcontainersorgv1alpha1.KbsConfigSpec{
		KbsSecretResources: []string{"user-secret-1", "user-secret-2"},
	}

	// Run merge
	merged := r.mergeKbsConfigSpecs(generatedSpec, manualSpec)

	// Verify all secrets are present
	expectedSecrets := []string{"attestation-status", "user-secret-1", "user-secret-2"}
	mergedMap := make(map[string]bool)
	for _, secret := range merged.KbsSecretResources {
		mergedMap[secret] = true
	}

	for _, expectedSecret := range expectedSecrets {
		if !mergedMap[expectedSecret] {
			t.Errorf("Upgrade scenario failed: expected secret %q not found in merged list. Got: %v",
				expectedSecret, merged.KbsSecretResources)
		}
	}

	if len(merged.KbsSecretResources) != len(expectedSecrets) {
		t.Errorf("Upgrade scenario failed: expected %d secrets, got %d. Expected: %v, Got: %v",
			len(expectedSecrets),
			len(merged.KbsSecretResources),
			expectedSecrets,
			merged.KbsSecretResources)
	}
}

func TestMergeKbsConfigSpecs_RestrictedProfileMigration(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = confidentialcontainersorgv1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &TrusteeConfigReconciler{
		Client: fakeClient,
		Scheme: scheme,
		log:    logr.Discard(),
	}

	// Exact scenario from the cluster:
	// - Generated spec (from Restricted profile): ["attestation-status"]
	// - Manual spec (from v1.1 KbsConfig): ["kbsres1", "security-policy"]
	// - Expected after merge: all three secrets

	generatedSpec := confidentialcontainersorgv1alpha1.KbsConfigSpec{
		KbsSecretResources: []string{"attestation-status"},
	}

	manualSpec := confidentialcontainersorgv1alpha1.KbsConfigSpec{
		KbsSecretResources: []string{"kbsres1", "security-policy"},
	}

	merged := r.mergeKbsConfigSpecs(generatedSpec, manualSpec)

	// Verify all three secrets are present
	expectedSecrets := []string{"attestation-status", "kbsres1", "security-policy"}
	mergedMap := make(map[string]bool)
	for _, secret := range merged.KbsSecretResources {
		mergedMap[secret] = true
	}

	for _, expectedSecret := range expectedSecrets {
		if !mergedMap[expectedSecret] {
			t.Errorf("Restricted profile migration failed: expected secret %q not found. Got: %v",
				expectedSecret, merged.KbsSecretResources)
		}
	}

	if len(merged.KbsSecretResources) != len(expectedSecrets) {
		t.Errorf("Restricted profile migration failed: expected %d secrets, got %d. Expected: %v, Got: %v",
			len(expectedSecrets),
			len(merged.KbsSecretResources),
			expectedSecrets,
			merged.KbsSecretResources)
	}
}

func TestMergeKbsConfigSpecs_PreservesOtherFields(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = confidentialcontainersorgv1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &TrusteeConfigReconciler{
		Client: fakeClient,
		Scheme: scheme,
		log:    logr.Discard(),
	}

	// Test that secret resource merging doesn't affect other fields
	generatedSpec := confidentialcontainersorgv1alpha1.KbsConfigSpec{
		KbsConfigMapName:   "generated-config",
		KbsSecretResources: []string{"attestation-status"},
		KbsEnvVars: map[string]string{
			"RUST_LOG": "debug",
		},
	}

	manualSpec := confidentialcontainersorgv1alpha1.KbsConfigSpec{
		KbsConfigMapName:   "manual-config",
		KbsSecretResources: []string{"my-custom-secret"},
		KbsEnvVars: map[string]string{
			"CUSTOM_VAR": "custom_value",
		},
	}

	merged := r.mergeKbsConfigSpecs(generatedSpec, manualSpec)

	// Verify secret resources are merged
	if len(merged.KbsSecretResources) != 2 {
		t.Errorf("Expected 2 secret resources, got %d: %v",
			len(merged.KbsSecretResources), merged.KbsSecretResources)
	}

	// Verify generated ConfigMap name is preserved (manual should not override)
	if merged.KbsConfigMapName != generatedSpec.KbsConfigMapName {
		t.Errorf("Expected KbsConfigMapName to be %q, got %q",
			generatedSpec.KbsConfigMapName, merged.KbsConfigMapName)
	}

	// Verify env vars are merged
	if merged.KbsEnvVars["RUST_LOG"] != "debug" {
		t.Errorf("Expected RUST_LOG env var to be preserved from generated spec")
	}
	if merged.KbsEnvVars["CUSTOM_VAR"] != "custom_value" {
		t.Errorf("Expected CUSTOM_VAR env var to be preserved from manual spec")
	}
}
