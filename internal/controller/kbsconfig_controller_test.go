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
	"errors"
	"os"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func envVarValue(env []corev1.EnvVar, name string) (string, bool) {
	for _, e := range env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

// clearProxyEnv makes the proxy variables truly absent for the duration of the
// test (and restores them afterwards). Unlike t.Setenv(k, ""), which leaves the
// variable present-but-empty, this ensures getClusterProxyEnvVars does not pick
// them up as explicit overrides of the cluster Proxy CR.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
		"NO_PROXY", "no_proxy",
	} {
		t.Setenv(k, "") // register restore of the original value on cleanup
		os.Unsetenv(k)   // then make it absent for this test
	}
}

// TestBuildEnvVars_ClusterProxyCR verifies that the proxy settings injected into
// the KBS operand are read from the cluster Proxy CR, so the operand is correctly
// configured on the first reconcile (before the operator restart that makes OLM
// inject those settings as env vars). This keeps the operand env consistent with
// the NetworkPolicy egress rules.
func TestBuildEnvVars_ClusterProxyCR(t *testing.T) {
	clearProxyEnv(t)

	proxy := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.ProxySpec{
			HTTPProxy:  "http://proxy.example.com:3128",
			HTTPSProxy: "http://proxy.example.com:3128",
			NoProxy:    ".svc,localhost",
		},
	}
	r, _ := newNetworkPolicyTestReconciler(t, true, proxy)

	env, err := buildEnvVars(context.Background(), r)
	if err != nil {
		t.Fatalf("buildEnvVars returned error: %v", err)
	}

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
	clearProxyEnv(t)

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

	env, err := buildEnvVars(context.Background(), r)
	if err != nil {
		t.Fatalf("buildEnvVars returned error: %v", err)
	}

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

	env, err := buildEnvVars(context.Background(), r)
	if err != nil {
		t.Fatalf("buildEnvVars returned error: %v", err)
	}

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
	clearProxyEnv(t)

	r, _ := newNetworkPolicyTestReconciler(t, false)

	env, err := buildEnvVars(context.Background(), r)
	if err != nil {
		t.Fatalf("buildEnvVars returned error: %v", err)
	}

	for _, k := range proxyEnvVarKeys {
		if _, ok := envVarValue(env, k); ok {
			t.Errorf("did not expect proxy env var %q to be set", k)
		}
	}
}

// TestBuildEnvVars_ProxyCRAbsent verifies that a missing cluster Proxy CR is
// treated as "no proxy" (not an error), so the operand is still deployed.
func TestBuildEnvVars_ProxyCRAbsent(t *testing.T) {
	clearProxyEnv(t)

	// OpenShift, but no Proxy CR present: the fake client returns NotFound.
	r, _ := newNetworkPolicyTestReconciler(t, true)

	env, err := buildEnvVars(context.Background(), r)
	if err != nil {
		t.Fatalf("absent Proxy CR must not be an error, got: %v", err)
	}
	for _, k := range proxyEnvVarKeys {
		if _, ok := envVarValue(env, k); ok {
			t.Errorf("did not expect proxy env var %q to be set", k)
		}
	}
}

// TestBuildEnvVars_ProxyReadError verifies that a non-NotFound error reading the
// cluster Proxy CR is propagated, so reconciliation requeues instead of deploying
// the operand without the required proxy configuration.
func TestBuildEnvVars_ProxyReadError(t *testing.T) {
	clearProxyEnv(t)

	r, _ := newNetworkPolicyTestReconciler(t, true)
	// Wrap the client so reading the Proxy CR fails with a transient API error.
	r.Client = fake.NewClientBuilder().
		WithScheme(r.Scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*configv1.Proxy); ok {
					return k8serrors.NewInternalError(errors.New("boom"))
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	if _, err := buildEnvVars(context.Background(), r); err == nil {
		t.Fatal("expected buildEnvVars to propagate a failed Proxy CR read")
	}
}

// TestProxyExplicitEmptyOverride verifies that an OLM-injected proxy variable set
// to an explicit empty value (OpenShift's documented way to unset a proxy) overrides
// the cluster Proxy CR value in both resolution paths: the operand env vars and the
// NetworkPolicy egress ports.
func TestProxyExplicitEmptyOverride(t *testing.T) {
	clearProxyEnv(t)
	// Simulate the OLM Subscription explicitly unsetting HTTPS_PROXY. t.Setenv leaves
	// the variable present-but-empty, which LookupEnv reports as set.
	t.Setenv("HTTPS_PROXY", "")

	proxy := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.ProxySpec{
			HTTPSProxy: "http://proxy.example.com:3128",
		},
	}
	r, _ := newNetworkPolicyTestReconciler(t, true, proxy)

	// Operand env: the explicit empty value must override the CR value.
	env, err := buildEnvVars(context.Background(), r)
	if err != nil {
		t.Fatalf("buildEnvVars returned error: %v", err)
	}
	if got, ok := envVarValue(env, "HTTPS_PROXY"); !ok || got != "" {
		t.Errorf("explicit empty HTTPS_PROXY must override the Proxy CR, got %q (set=%v)", got, ok)
	}

	// NetworkPolicy: with the proxy unset, the proxy port must not be opened.
	policies, err := r.newKbsNetworkPolicies(context.Background())
	if err != nil {
		t.Fatalf("newKbsNetworkPolicies returned error: %v", err)
	}
	np := findPolicy(t, policies, kbsNetworkPolicyAllowEgressAttest)
	if egressRuleHasPort(np.Spec.Egress[0], corev1.ProtocolTCP, 3128) {
		t.Error("attestation egress must not open the proxy port when the proxy is explicitly unset")
	}
}
