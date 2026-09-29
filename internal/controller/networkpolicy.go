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
	"net/url"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NetworkPolicy names for the KBS operand.
const (
	kbsNetworkPolicyDenyAll           = "kbs-deny-all"
	kbsNetworkPolicyAllowIngress      = "kbs-allow-ingress"
	kbsNetworkPolicyAllowEgressDNS    = "kbs-allow-egress-dns"
	kbsNetworkPolicyAllowEgressAttest = "kbs-allow-egress-attestation"

	// Cluster DNS namespaces. OpenShift runs CoreDNS in openshift-dns; vanilla
	// Kubernetes runs it in kube-system. Selected at runtime via IsOpenShift.
	openShiftDNSNamespace = "openshift-dns"
	upstreamDNSNamespace  = "kube-system"

	// openShiftRouterNamespaceLabel selects the OpenShift ingress router namespace.
	// The router is host-networked, so it is not covered by an "any in-cluster pod"
	// ingress rule and must be admitted explicitly. There is no equivalent on
	// vanilla Kubernetes.
	openShiftRouterNamespaceLabel = "policy-group.network.openshift.io/ingress"
)

// The KBS operand pods (Deployment/Service) are selected by this label,
// see newKbsDeployment/newKbsService.
var kbsPodSelector = metav1.LabelSelector{
	MatchLabels: map[string]string{
		"app": "kbs",
	},
}

// deployOrUpdateKbsNetworkPolicies creates or updates the set of operator-owned
// NetworkPolicies that protect the KBS operand pods.
//
// The policies are scoped to the operand pods by label
// (app=kbs) rather than to the whole namespace. Every policy is owner-referenced to the
// KbsConfig CR and watched via Owns() in SetupWithManager, so a user who edits or
// deletes one has it restored on the next reconcile.
func (r *KbsConfigReconciler) deployOrUpdateKbsNetworkPolicies(ctx context.Context) error {
	policies, err := r.newKbsNetworkPolicies(ctx)
	if err != nil {
		return err
	}

	for _, desired := range policies {
		found := &networkingv1.NetworkPolicy{}
		err := r.Get(ctx, client.ObjectKey{
			Namespace: desired.Namespace,
			Name:      desired.Name,
		}, found)

		if err != nil && k8serrors.IsNotFound(err) {
			// Create the NetworkPolicy
			r.log.Info("Creating a new NetworkPolicy", "NetworkPolicy.Namespace", desired.Namespace, "NetworkPolicy.Name", desired.Name)
			if err := r.Create(ctx, desired); err != nil {
				r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeWarning, "NetworkPolicyCreateFailed", "NetworkPolicyCreateFailed", err.Error())
				return err
			}
			r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeNormal, "NetworkPolicyCreated", "NetworkPolicyCreated", "KBS NetworkPolicy %s created successfully", desired.Name)
			continue
		} else if err != nil {
			return err
		}

		// NetworkPolicy already exists: reconcile it back to the desired spec if it
		// drifted (e.g. a user edited it). This is what enforces operator ownership.
		if apiequality.Semantic.DeepEqual(found.Spec, desired.Spec) {
			continue
		}
		r.log.Info("Updating NetworkPolicy", "NetworkPolicy.Namespace", desired.Namespace, "NetworkPolicy.Name", desired.Name)
		found.Spec = desired.Spec
		if err := r.Update(ctx, found); err != nil {
			r.Recorder.Eventf(r.kbsConfig, nil, corev1.EventTypeWarning, "NetworkPolicyUpdateFailed", "NetworkPolicyUpdateFailed", err.Error())
			return err
		}
	}
	return nil
}

// newKbsNetworkPolicies builds the full set of NetworkPolicies for the KBS operand.
// Each policy selects the operand pods (app=kbs) in the controller namespace and is
// owner-referenced to the KbsConfig CR so it is garbage collected with the operand.
func (r *KbsConfigReconciler) newKbsNetworkPolicies(ctx context.Context) ([]*networkingv1.NetworkPolicy, error) {
	tcp := corev1.ProtocolTCP
	udp := corev1.ProtocolUDP
	port8080 := intstr.FromInt(8080)
	port53 := intstr.FromInt(53)
	port5353 := intstr.FromInt(5353)
	port443 := intstr.FromInt(443)

	// 1. Default-deny baseline. Selecting the operand pods and enabling both policy
	// types with NO rules denies all ingress and egress; every allowed flow below is
	// re-opened explicitly by a sibling policy.
	denyAll := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kbsNetworkPolicyDenyAll,
			Namespace: r.namespace,
			Labels:    standardLabels(r.kbsConfig.Name, "network-policy"),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: kbsPodSelector,
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
		},
	}

	// 2. Ingress to the KBS attestation/resource endpoint (:8080). KBS is the
	// guest-facing trust endpoint: guest attestation agents and clients live in
	// arbitrary namespaces, so ingress is allowed from any in-cluster peer.
	ingressRules := []networkingv1.NetworkPolicyIngressRule{
		{
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: &tcp, Port: &port8080},
			},
		},
	}
	// On OpenShift, the ingress router is host-networked, which the any-client
	// rule above does not cover on OVN-Kubernetes; admit the router namespace
	// so external access via kbs-route works. Vanilla Kubernetes has no such router.
	if r.IsOpenShift {
		ingressRules = append(ingressRules, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							openShiftRouterNamespaceLabel: "",
						},
					},
					// The ingress router runs hostNetwork: true. On OVN-Kubernetes a
					// namespaceSelector alone does not match hostNetwork pods; an empty
					// podSelector must be paired with it to admit the router pods.
					PodSelector: &metav1.LabelSelector{},
				},
			},
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: &tcp, Port: &port8080},
			},
		})
	}
	allowIngress := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kbsNetworkPolicyAllowIngress,
			Namespace: r.namespace,
			Labels:    standardLabels(r.kbsConfig.Name, "network-policy"),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: kbsPodSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     ingressRules,
		},
	}

	// 3. Egress to cluster DNS. KBS needs name resolution to reach external
	// attestation peers and any in-cluster service it is configured against. The DNS
	// namespace differs by platform (openshift-dns vs kube-system).
	dnsNamespace := upstreamDNSNamespace
	if r.IsOpenShift {
		dnsNamespace = openShiftDNSNamespace
	}
	allowEgressDNS := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kbsNetworkPolicyAllowEgressDNS,
			Namespace: r.namespace,
			Labels:    standardLabels(r.kbsConfig.Name, "network-policy"),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: kbsPodSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": dnsNamespace,
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &udp, Port: &port5353},
						{Protocol: &tcp, Port: &port5353},
						{Protocol: &udp, Port: &port53},
						{Protocol: &tcp, Port: &port53},
					},
				},
			},
		},
	}

	// 4. Egress to the external attestation/verification peers (HTTPS :443) used in
	// the connected profile: AMD KDS, Intel PCS/Trust Authority, NVIDIA NRAS.
	// NetworkPolicy cannot match destinations by DNS name, so this allows :443 to any
	// destination as a portable baseline.
	attestationPorts := []networkingv1.NetworkPolicyPort{
		{Protocol: &tcp, Port: &port443},
	}
	// When a cluster-wide or user-specified proxy is configured, KBS reaches the
	// external peers through the proxy host:port instead of directly on :443.
	// Proxies commonly listen on a non-443 port (e.g. 3128, 8080), which the :443
	// rule above would not admit, so open the proxy port(s) too. As with :443, the
	// proxy host cannot be matched by DNS name, so only the port is opened.
	proxyEnv := r.getEffectiveProxyEnvVars(ctx)
	for _, p := range proxyEgressPorts(proxyEnv) {
		if p == 443 {
			continue // already covered by the baseline rule
		}
		proxyPort := intstr.FromInt(int(p))
		attestationPorts = append(attestationPorts, networkingv1.NetworkPolicyPort{Protocol: &tcp, Port: &proxyPort})
	}
	allowEgressAttestation := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kbsNetworkPolicyAllowEgressAttest,
			Namespace: r.namespace,
			Labels:    standardLabels(r.kbsConfig.Name, "network-policy"),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: kbsPodSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					Ports: attestationPorts,
				},
			},
		},
	}

	policies := []*networkingv1.NetworkPolicy{
		denyAll,
		allowIngress,
		allowEgressDNS,
		allowEgressAttestation,
	}

	// Set KbsConfig instance as the owner and controller of every policy so they are
	// garbage collected with the operand and restored by Owns() if tampered with.
	for _, np := range policies {
		if err := ctrl.SetControllerReference(r.kbsConfig, np, r.Scheme); err != nil {
			r.log.Info("Error in setting the controller reference for the KBS NetworkPolicy", "NetworkPolicy.Name", np.Name, "err", err)
			return nil, err
		}
	}

	return policies, nil
}

// proxyEgressPorts returns the distinct TCP ports the KBS operand must be allowed
// to reach in order to use the configured proxies. It inspects the HTTPS and HTTP
// proxy settings (both case variants) and returns their ports in ascending order.
// Returns nil when no usable proxy is configured. NO_PROXY is intentionally
// ignored: it only narrows which destinations bypass the proxy and does not
// require any additional egress port.
func proxyEgressPorts(proxyEnv map[string]string) []int32 {
	seen := make(map[int32]struct{})
	var ports []int32
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		raw := proxyEnv[key]
		if raw == "" {
			continue
		}
		port, ok := parseProxyPort(raw)
		if !ok {
			continue
		}
		if _, dup := seen[port]; dup {
			continue
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	slices.Sort(ports)
	return ports
}

// parseProxyPort extracts the TCP port from a proxy URL. Proxy values may omit
// the scheme (e.g. "proxy.example.com:3128"); in that case a default scheme is
// assumed so the host:port can be parsed. When no explicit port is present the
// scheme default is used (http -> 80, https -> 443). It returns false when the
// value cannot be parsed into a valid port.
func parseProxyPort(raw string) (int32, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return 0, false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return 0, false
		}
		return int32(n), true
	}
	switch u.Scheme {
	case "https":
		return 443, true
	case "http":
		return 80, true
	default:
		return 0, false
	}
}
