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
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	confidentialcontainersorgv1alpha1 "github.com/confidential-containers/trustee-operator/api/v1alpha1"
)

func TestGetTLSConfigFromTlsConfig_Nil(t *testing.T) {
	result := GetTLSConfigFromTlsConfig(nil)

	if result.TlsProfile != "intermediate" {
		t.Errorf("Expected TlsProfile to be 'intermediate', got '%s'", result.TlsProfile)
	}
	if result.TlsMinVersion != "" {
		t.Errorf("Expected TlsMinVersion to be empty, got '%s'", result.TlsMinVersion)
	}
	if result.TlsMaxVersion != "" {
		t.Errorf("Expected TlsMaxVersion to be empty, got '%s'", result.TlsMaxVersion)
	}
	if result.TlsCiphers != "" {
		t.Errorf("Expected TlsCiphers to be empty, got '%s'", result.TlsCiphers)
	}
	if result.TlsGroups != "" {
		t.Errorf("Expected TlsGroups to be empty, got '%s'", result.TlsGroups)
	}
}

func TestGetTLSConfigFromTlsConfig_Modern(t *testing.T) {
	tlsConfig := &confidentialcontainersorgv1alpha1.TlsConfig{
		Profile: "modern",
	}

	result := GetTLSConfigFromTlsConfig(tlsConfig)

	if result.TlsProfile != "modern" {
		t.Errorf("Expected TlsProfile to be 'modern', got '%s'", result.TlsProfile)
	}
	if result.TlsMinVersion != "" {
		t.Errorf("Expected TlsMinVersion to be empty, got '%s'", result.TlsMinVersion)
	}
	if result.TlsMaxVersion != "" {
		t.Errorf("Expected TlsMaxVersion to be empty, got '%s'", result.TlsMaxVersion)
	}
}

func TestGetTLSConfigFromTlsConfig_EmptyProfile(t *testing.T) {
	tlsConfig := &confidentialcontainersorgv1alpha1.TlsConfig{
		Profile: "",
	}

	result := GetTLSConfigFromTlsConfig(tlsConfig)

	if result.TlsProfile != "intermediate" {
		t.Errorf("Expected empty profile to default to 'intermediate', got '%s'", result.TlsProfile)
	}
}

func TestGetTLSConfigFromTlsConfig_Custom(t *testing.T) {
	tlsConfig := &confidentialcontainersorgv1alpha1.TlsConfig{
		Profile:    "custom",
		MinVersion: "1.2",
		MaxVersion: "1.3",
		Ciphers: []string{
			"TLS_AES_128_GCM_SHA256",
			"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		},
		Groups: []string{"x25519", "secp256r1"},
	}

	result := GetTLSConfigFromTlsConfig(tlsConfig)

	if result.TlsProfile != "custom" {
		t.Errorf("Expected TlsProfile to be 'custom', got '%s'", result.TlsProfile)
	}
	if result.TlsMinVersion != "1.2" {
		t.Errorf("Expected TlsMinVersion to be '1.2', got '%s'", result.TlsMinVersion)
	}
	if result.TlsMaxVersion != "1.3" {
		t.Errorf("Expected TlsMaxVersion to be '1.3', got '%s'", result.TlsMaxVersion)
	}

	expectedCiphers := "TLS_AES_128_GCM_SHA256:ECDHE-RSA-AES128-GCM-SHA256"
	if result.TlsCiphers != expectedCiphers {
		t.Errorf("Expected TlsCiphers to be '%s', got '%s'", expectedCiphers, result.TlsCiphers)
	}

	expectedGroups := "x25519:secp256r1"
	if result.TlsGroups != expectedGroups {
		t.Errorf("Expected TlsGroups to be '%s', got '%s'", expectedGroups, result.TlsGroups)
	}
}

func TestGetTLSConfigFromTlsConfig_CustomWithoutOptionalFields(t *testing.T) {
	tlsConfig := &confidentialcontainersorgv1alpha1.TlsConfig{
		Profile:    "custom",
		MinVersion: "1.2",
	}

	result := GetTLSConfigFromTlsConfig(tlsConfig)

	if result.TlsProfile != "custom" {
		t.Errorf("Expected TlsProfile to be 'custom', got '%s'", result.TlsProfile)
	}
	if result.TlsMinVersion != "1.2" {
		t.Errorf("Expected TlsMinVersion to be '1.2', got '%s'", result.TlsMinVersion)
	}
	if result.TlsMaxVersion != "" {
		t.Errorf("Expected TlsMaxVersion to be empty, got '%s'", result.TlsMaxVersion)
	}
	if result.TlsCiphers != "" {
		t.Errorf("Expected TlsCiphers to be empty, got '%s'", result.TlsCiphers)
	}
	if result.TlsGroups != "" {
		t.Errorf("Expected TlsGroups to be empty, got '%s'", result.TlsGroups)
	}
}

func TestConvertCiphers_TLS13(t *testing.T) {
	ciphers := []string{
		"TLS_AES_128_GCM_SHA256",
		"TLS_AES_256_GCM_SHA384",
		"TLS_CHACHA20_POLY1305_SHA256",
	}

	result := convertCiphers(ciphers)

	expected := "TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256"
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertCiphers_TLS12(t *testing.T) {
	ciphers := []string{
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
	}

	result := convertCiphers(ciphers)

	expected := "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384"
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertCiphers_Mixed(t *testing.T) {
	ciphers := []string{
		"TLS_AES_128_GCM_SHA256",
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
	}

	result := convertCiphers(ciphers)

	expected := "TLS_AES_128_GCM_SHA256:ECDHE-RSA-AES128-GCM-SHA256"
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertCiphers_Empty(t *testing.T) {
	result := convertCiphers([]string{})
	if result != "" {
		t.Errorf("Expected empty string, got '%s'", result)
	}
}

func TestConvertSingleCipher_TLS13_AES(t *testing.T) {
	cipher := "TLS_AES_128_GCM_SHA256"
	result := convertSingleCipher(cipher)
	if result != cipher {
		t.Errorf("Expected TLS 1.3 cipher to be unchanged: '%s', got '%s'", cipher, result)
	}
}

func TestConvertSingleCipher_TLS13_ChaCha(t *testing.T) {
	cipher := "TLS_CHACHA20_POLY1305_SHA256"
	result := convertSingleCipher(cipher)
	if result != cipher {
		t.Errorf("Expected TLS 1.3 cipher to be unchanged: '%s', got '%s'", cipher, result)
	}
}

func TestConvertSingleCipher_TLS12_ECDHE_RSA(t *testing.T) {
	cipher := "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
	expected := "ECDHE-RSA-AES128-GCM-SHA256"
	result := convertSingleCipher(cipher)
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertSingleCipher_TLS12_ECDHE_ECDSA(t *testing.T) {
	cipher := "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384"
	expected := "ECDHE-ECDSA-AES256-GCM-SHA384"
	result := convertSingleCipher(cipher)
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertSingleCipher_TLS12_DHE_RSA(t *testing.T) {
	cipher := "TLS_DHE_RSA_WITH_AES_128_CBC_SHA"
	expected := "DHE-RSA-AES128-CBC-SHA"
	result := convertSingleCipher(cipher)
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertSingleCipher_TLS12_ChaCha20_ECDHE_RSA(t *testing.T) {
	cipher := "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256"
	expected := "ECDHE-RSA-CHACHA20-POLY1305"
	result := convertSingleCipher(cipher)
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertSingleCipher_TLS12_ChaCha20_ECDHE_ECDSA(t *testing.T) {
	cipher := "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256"
	expected := "ECDHE-ECDSA-CHACHA20-POLY1305"
	result := convertSingleCipher(cipher)
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertSingleCipher_TLS12_ChaCha20_DHE_RSA(t *testing.T) {
	cipher := "TLS_DHE_RSA_WITH_CHACHA20_POLY1305_SHA256"
	expected := "DHE-RSA-CHACHA20-POLY1305"
	result := convertSingleCipher(cipher)
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertCiphers_TLS12_WithChaCha20(t *testing.T) {
	ciphers := []string{
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
		"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
		"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
	}

	result := convertCiphers(ciphers)

	expected := "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305"
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertSingleCipher_TLS12_RSA_AES_CBC(t *testing.T) {
	cipher := "TLS_RSA_WITH_AES_128_CBC_SHA"
	expected := "AES128-SHA"
	result := convertSingleCipher(cipher)
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertSingleCipher_TLS12_RSA_AES_GCM(t *testing.T) {
	cipher := "TLS_RSA_WITH_AES_256_GCM_SHA384"
	expected := "AES256-GCM-SHA384"
	result := convertSingleCipher(cipher)
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertSingleCipher_TLS12_3DES(t *testing.T) {
	cipher := "TLS_RSA_WITH_3DES_EDE_CBC_SHA"
	expected := "DES-CBC3-SHA"
	result := convertSingleCipher(cipher)
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestConvertCiphers_WithRSACiphers(t *testing.T) {
	ciphers := []string{
		"TLS_RSA_WITH_AES_128_CBC_SHA",
		"TLS_RSA_WITH_AES_256_GCM_SHA384",
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
	}

	result := convertCiphers(ciphers)

	expected := "AES128-SHA:AES256-GCM-SHA384:ECDHE-RSA-AES128-GCM-SHA256"
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestGetTLSConfigFromCluster_NoAPIServer(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	fallback := &confidentialcontainersorgv1alpha1.TlsConfig{
		Profile: "modern",
	}

	result := GetTLSConfigFromCluster(context.Background(), client, fallback)

	if result.TlsProfile != "modern" {
		t.Errorf("Expected fallback profile 'modern', got '%s'", result.TlsProfile)
	}
}

func TestGetTLSConfigFromCluster_IntermediateProfile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()

	result := GetTLSConfigFromCluster(context.Background(), client, nil)

	if result.TlsProfile != "intermediate" {
		t.Errorf("Expected profile 'intermediate', got '%s'", result.TlsProfile)
	}
}

func TestGetTLSConfigFromCluster_ModernProfile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()

	result := GetTLSConfigFromCluster(context.Background(), client, nil)

	if result.TlsProfile != "modern" {
		t.Errorf("Expected profile 'modern', got '%s'", result.TlsProfile)
	}
}

func TestGetTLSConfigFromCluster_OldProfile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
				Old:  &configv1.OldTLSProfile{},
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()

	result := GetTLSConfigFromCluster(context.Background(), client, nil)

	if result.TlsProfile != "old" {
		t.Errorf("Expected profile 'old', got '%s'", result.TlsProfile)
	}
}

func TestGetTLSConfigFromCluster_CustomProfile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"ECDHE-ECDSA-AES128-GCM-SHA256",
						},
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()

	result := GetTLSConfigFromCluster(context.Background(), client, nil)

	if result.TlsProfile != "custom" {
		t.Errorf("Expected profile 'custom', got '%s'", result.TlsProfile)
	}
	if result.TlsMinVersion != "1.2" {
		t.Errorf("Expected MinVersion '1.2', got '%s'", result.TlsMinVersion)
	}

	expectedCiphers := "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256"
	if result.TlsCiphers != expectedCiphers {
		t.Errorf("Expected ciphers '%s', got '%s'", expectedCiphers, result.TlsCiphers)
	}
}

func TestConvertTLSVersion(t *testing.T) {
	tests := []struct {
		input    configv1.TLSProtocolVersion
		expected string
	}{
		{configv1.VersionTLS10, "1.0"},
		{configv1.VersionTLS11, "1.1"},
		{configv1.VersionTLS12, "1.2"},
		{configv1.VersionTLS13, "1.3"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		result := convertTLSVersion(tt.input)
		if result != tt.expected {
			t.Errorf("convertTLSVersion(%s) = %s, expected %s", tt.input, result, tt.expected)
		}
	}
}

func TestGetTLSConfigFromCluster_NeitherOpenShiftNorTrusteeConfig(t *testing.T) {
	// Test the case where neither OpenShift APIServer nor TrusteeConfig provide TLS params
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	// No APIServer object created, and nil fallback config
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	result := GetTLSConfigFromCluster(context.Background(), client, nil)

	// Should default to intermediate profile
	if result.TlsProfile != "intermediate" {
		t.Errorf("Expected default profile 'intermediate', got '%s'", result.TlsProfile)
	}

	// Other fields should be empty (profile handles defaults internally)
	if result.TlsMinVersion != "" {
		t.Errorf("Expected TlsMinVersion to be empty, got '%s'", result.TlsMinVersion)
	}
	if result.TlsMaxVersion != "" {
		t.Errorf("Expected TlsMaxVersion to be empty, got '%s'", result.TlsMaxVersion)
	}
	if result.TlsCiphers != "" {
		t.Errorf("Expected TlsCiphers to be empty, got '%s'", result.TlsCiphers)
	}
	if result.TlsGroups != "" {
		t.Errorf("Expected TlsGroups to be empty, got '%s'", result.TlsGroups)
	}
}

func TestGetTLSConfigFromCluster_OpenShiftAPIServerNilProfile(t *testing.T) {
	// Test the case where OpenShift APIServer exists but has no TLS profile set
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: nil, // No TLS profile configured
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()

	result := GetTLSConfigFromCluster(context.Background(), client, nil)

	// Should fall back to default intermediate profile
	if result.TlsProfile != "intermediate" {
		t.Errorf("Expected default profile 'intermediate', got '%s'", result.TlsProfile)
	}
}
