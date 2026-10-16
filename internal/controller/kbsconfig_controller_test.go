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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func envVarValue(env []corev1.EnvVar, name string) (string, bool) {
	for _, e := range env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

// TestBuildEnvVars_ClusterProxyCR verifies that the proxy settings injected into
// the KBS operand are read from the cluster Proxy CR, so the operand is correctly
// configured on the first reconcile (before the operator restart that makes OLM
// inject those settings as env vars). This keeps the operand env consistent with
// the NetworkPolicy egress rules.
func TestBuildEnvVars_ClusterProxyCR(t *testing.T) {
	// Isolate from any proxy vars inherited by the test process, which
	// getClusterProxyEnvVars would otherwise layer on top of the Proxy CR.
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")

	proxy := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.ProxySpec{
			HTTPProxy:  "http://proxy.example.com:3128",
			HTTPSProxy: "http://proxy.example.com:3128",
			NoProxy:    ".svc,localhost",
		},
	}
	r, _ := newNetworkPolicyTestReconciler(t, true, proxy)

	env := buildEnvVars(context.Background(), r)

	for _, tc := range []struct {
		name string
		want string
	}{
		{"HTTP_PROXY", "http://proxy.example.com:3128"},
		{"http_proxy", "http://proxy.example.com:3128"},
		{"HTTPS_PROXY", "http://proxy.example.com:3128"},
		{"https_proxy", "http://proxy.example.com:3128"},
		{"NO_PROXY", ".svc,localhost"},
		{"no_proxy", ".svc,localhost"},
	} {
		got, ok := envVarValue(env, tc.name)
		if !ok {
			t.Errorf("expected env var %q to be set from the cluster Proxy CR", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("env var %q: expected %q, got %q", tc.name, tc.want, got)
		}
	}
}

// TestBuildEnvVars_ClusterProxyStatusNoProxy verifies that the effective no-proxy
// list from status (which OpenShift augments with cluster CIDRs) takes precedence
// over the user-specified Spec.NoProxy.
func TestBuildEnvVars_ClusterProxyStatusNoProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")

	proxy := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.ProxySpec{
			HTTPSProxy: "http://proxy.example.com:3128",
			NoProxy:    ".svc",
		},
		Status: configv1.ProxyStatus{
			NoProxy: ".svc,10.128.0.0/14,172.30.0.0/16,169.254.169.254",
		},
	}
	r, _ := newNetworkPolicyTestReconciler(t, true, proxy)

	env := buildEnvVars(context.Background(), r)

	want := ".svc,10.128.0.0/14,172.30.0.0/16,169.254.169.254"
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		if got, _ := envVarValue(env, k); got != want {
			t.Errorf("%s: expected effective status no-proxy %q, got %q", k, want, got)
		}
	}
}

// TestBuildEnvVars_KbsEnvVarsOverrideProxy verifies that user-supplied KbsEnvVars
// take precedence over the cluster Proxy CR settings.
func TestBuildEnvVars_KbsEnvVarsOverrideProxy(t *testing.T) {
	proxy := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.ProxySpec{
			HTTPSProxy: "http://proxy.example.com:3128",
		},
	}
	r, _ := newNetworkPolicyTestReconciler(t, true, proxy)
	r.kbsConfig.Spec.KbsEnvVars = map[string]string{
		"HTTPS_PROXY": "https://user-proxy.internal:8080",
		"RUST_LOG":    "debug",
	}

	env := buildEnvVars(context.Background(), r)

	if got, _ := envVarValue(env, "HTTPS_PROXY"); got != "https://user-proxy.internal:8080" {
		t.Errorf("KbsEnvVars must override the cluster proxy, got %q", got)
	}
	if got, _ := envVarValue(env, "RUST_LOG"); got != "debug" {
		t.Errorf("KbsEnvVars must be propagated, got %q", got)
	}
}

// TestBuildEnvVars_NoProxy verifies no proxy env vars are injected when neither
// the cluster Proxy CR nor KbsEnvVars configure one.
func TestBuildEnvVars_NoProxy(t *testing.T) {
	// Isolate from any proxy vars inherited by the test process.
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")

	r, _ := newNetworkPolicyTestReconciler(t, false)

	env := buildEnvVars(context.Background(), r)

	for _, k := range proxyEnvVarKeys {
		if _, ok := envVarValue(env, k); ok {
			t.Errorf("did not expect proxy env var %q to be set", k)
		}
	}
}
