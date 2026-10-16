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
	"fmt"
	"os"
	"strings"

	consolev1 "github.com/openshift/api/console/v1"
	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ConsolePluginName       = "trustee-openshift-console-plugin"
	consolePluginPort       = 9443
	consolePluginCertSecret = "trustee-openshift-console-plugin-cert"
)

// consolePluginLabels returns the standard labels for console plugin resources.
func consolePluginLabels() map[string]string {
	return map[string]string{
		"app":                          ConsolePluginName,
		"app.kubernetes.io/name":       ConsolePluginName,
		"app.kubernetes.io/managed-by": "trustee-operator",
		"app.kubernetes.io/part-of":    "trustee",
		"app.kubernetes.io/component":  "console-plugin",
	}
}

// deployOrUpdateConsolePlugin creates or updates all resources for the console plugin:
// Deployment, Service, ConfigMap (nginx.conf), ConsolePlugin CR, and NetworkPolicy.
func (r *KbsConfigReconciler) deployOrUpdateConsolePlugin(ctx context.Context) error {
	image := consolePluginImage()
	if image == "" {
		r.log.Info("CONSOLE_PLUGIN_IMAGE not set, skipping console plugin deployment")
		return nil
	}

	if err := r.deployOrUpdateConsolePluginConfigMap(ctx); err != nil {
		return fmt.Errorf("console plugin ConfigMap: %w", err)
	}
	if err := r.deployOrUpdateConsolePluginDeployment(ctx, image); err != nil {
		return fmt.Errorf("console plugin Deployment: %w", err)
	}
	if err := r.deployOrUpdateConsolePluginService(ctx); err != nil {
		return fmt.Errorf("console plugin Service: %w", err)
	}
	if err := r.deployOrUpdateConsolePluginCR(ctx); err != nil {
		return fmt.Errorf("ConsolePlugin CR: %w", err)
	}
	if err := r.deployOrUpdateConsolePluginNetworkPolicy(ctx); err != nil {
		return fmt.Errorf("console plugin NetworkPolicy: %w", err)
	}
	return nil
}

func consolePluginImage() string {
	return os.Getenv("CONSOLE_PLUGIN_IMAGE")
}

// --- ConfigMap (nginx.conf) ---

func (r *KbsConfigReconciler) deployOrUpdateConsolePluginConfigMap(ctx context.Context) error {
	desired := r.newConsolePluginConfigMap(ctx)
	found := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), found)
	if err != nil && k8serrors.IsNotFound(err) {
		r.log.Info("Creating console plugin ConfigMap")
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	if apiequality.Semantic.DeepEqual(found.Data, desired.Data) {
		return nil
	}
	r.log.Info("Updating console plugin ConfigMap")
	found.Data = desired.Data
	return r.Update(ctx, found)
}

func (r *KbsConfigReconciler) newConsolePluginConfigMap(ctx context.Context) *corev1.ConfigMap {
	nginxConf := r.buildNginxConf(ctx)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConsolePluginName,
			Namespace: r.namespace,
			Labels:    consolePluginLabels(),
		},
		Data: map[string]string{
			"nginx.conf": nginxConf,
		},
	}
	_ = ctrl.SetControllerReference(r.kbsConfig, cm, r.Scheme)
	return cm
}

func (r *KbsConfigReconciler) buildNginxConf(ctx context.Context) string {
	nd := r.nginxTLSFromCluster(ctx)
	return fmt.Sprintf(`error_log /dev/stdout info;
events {}
http {
  access_log         /dev/stdout;
  include            /etc/nginx/mime.types;
  default_type       application/octet-stream;
  keepalive_timeout  65;
  server {
    listen              %d ssl;
    listen              [::]:%d ssl;
    ssl_certificate     /var/cert/tls.crt;
    ssl_certificate_key /var/cert/tls.key;
    ssl_protocols       %s;
    ssl_ciphers         %s;
    ssl_prefer_server_ciphers on;
    ssl_ecdh_curve      %s;
    root                /usr/share/nginx/html;
  }
}
`, consolePluginPort, consolePluginPort, nd.protocols, nd.ciphers, nd.ecdhCurve)
}

// nginxTLSDirectives holds the nginx ssl_* directive values derived from the
// cluster TLS profile.
type nginxTLSDirectives struct {
	protocols string
	ciphers   string
	ecdhCurve string
}

// pqcECDHCurves are the post-quantum ML-KEM hybrid groups that ubi10/nginx-126
// (OpenSSL 3.5) supports. Prepended to the curve list so PQC key exchange is
// preferred when both sides support it.
var pqcECDHCurves = []string{"SecP256r1MLKEM768", "SecP384r1MLKEM1024"}

// defaultClassicCurves are the classical ECDH curves used when the cluster
// profile does not specify groups.
var defaultClassicCurves = []string{"secp256r1", "secp384r1"}

// nginxTLSFromCluster reads the cluster's TLS security profile and translates
// it into nginx ssl_* directive values. PQC ML-KEM hybrid groups are always
// prepended to the ECDH curve list.
func (r *KbsConfigReconciler) nginxTLSFromCluster(ctx context.Context) nginxTLSDirectives {
	defaults := nginxTLSDirectives{
		protocols: "TLSv1.2 TLSv1.3",
		ciphers:   "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305",
		ecdhCurve: strings.Join(append(pqcECDHCurves, defaultClassicCurves...), ":"),
	}

	apiServer := &configv1.APIServer{}
	if err := r.Get(ctx, client.ObjectKey{Name: "cluster"}, apiServer); err != nil {
		r.log.Info("Cannot read APIServer TLS profile, using defaults", "err", err)
		return defaults
	}

	profile := apiServer.Spec.TLSSecurityProfile
	if profile == nil {
		return defaults
	}

	return tlsProfileToNginx(profile)
}

// tlsProfileToNginx converts an OpenShift TLSSecurityProfile to nginx
// ssl_protocols, ssl_ciphers, and ssl_ecdh_curve values.
func tlsProfileToNginx(profile *configv1.TLSSecurityProfile) nginxTLSDirectives {
	var spec configv1.TLSProfileSpec
	switch profile.Type {
	case configv1.TLSProfileCustomType:
		if profile.Custom != nil {
			spec = profile.Custom.TLSProfileSpec
		} else {
			spec = *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
		}
	default:
		profileType := profile.Type
		if profileType == "" {
			profileType = configv1.TLSProfileIntermediateType
		}
		s, ok := configv1.TLSProfiles[profileType]
		if !ok {
			s = configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
		}
		spec = *s
	}

	nd := nginxTLSDirectives{}

	// Protocols: derive from MinTLSVersion
	nd.protocols = nginxProtocols(spec.MinTLSVersion)

	// Ciphers: filter DHE, convert to OpenSSL names
	ciphers := filterDHECiphers(spec.Ciphers)
	if len(ciphers) > 0 {
		nd.ciphers = strings.Join(ciphers, ":")
	} else {
		nd.ciphers = "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305"
	}

	// ECDH curves: PQC ML-KEM hybrids + classical curves
	nd.ecdhCurve = strings.Join(append(pqcECDHCurves, defaultClassicCurves...), ":")

	return nd
}

// nginxProtocols returns the nginx ssl_protocols string for a given minimum
// TLS version.
func nginxProtocols(minVersion configv1.TLSProtocolVersion) string {
	switch minVersion {
	case configv1.VersionTLS10:
		return "TLSv1 TLSv1.1 TLSv1.2 TLSv1.3"
	case configv1.VersionTLS11:
		return "TLSv1.1 TLSv1.2 TLSv1.3"
	case configv1.VersionTLS13:
		return "TLSv1.3"
	default:
		return "TLSv1.2 TLSv1.3"
	}
}

// --- Deployment ---

func (r *KbsConfigReconciler) deployOrUpdateConsolePluginDeployment(ctx context.Context, image string) error {
	desired := r.newConsolePluginDeployment(image)
	found := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), found)
	if err != nil && k8serrors.IsNotFound(err) {
		r.log.Info("Creating console plugin Deployment")
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	if apiequality.Semantic.DeepEqual(found.Spec.Template, desired.Spec.Template) &&
		apiequality.Semantic.DeepEqual(found.Spec.Replicas, desired.Spec.Replicas) {
		return nil
	}
	r.log.Info("Updating console plugin Deployment")
	found.Spec.Template = desired.Spec.Template
	found.Spec.Replicas = desired.Spec.Replicas
	return r.Update(ctx, found)
}

func (r *KbsConfigReconciler) newConsolePluginDeployment(image string) *appsv1.Deployment {
	replicas := int32(2)
	labels := consolePluginLabels()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConsolePluginName,
			Namespace: r.namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: boolPtr(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Name:            ConsolePluginName,
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Ports: []corev1.ContainerPort{{
							ContainerPort: consolePluginPort,
							Protocol:      corev1.ProtocolTCP,
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: boolPtr(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resourceMustParse("10m"),
								corev1.ResourceMemory: resourceMustParse("50Mi"),
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "cert",
								ReadOnly:  true,
								MountPath: "/var/cert",
							},
							{
								Name:      "nginx-conf",
								ReadOnly:  true,
								MountPath: "/etc/nginx/nginx.conf",
								SubPath:   "nginx.conf",
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "cert",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName:  consolePluginCertSecret,
									DefaultMode: int32Ptr(420),
								},
							},
						},
						{
							Name: "nginx-conf",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: ConsolePluginName,
									},
									DefaultMode: int32Ptr(420),
								},
							},
						},
					},
				},
			},
		},
	}
	_ = ctrl.SetControllerReference(r.kbsConfig, dep, r.Scheme)
	return dep
}

// --- Service ---

func (r *KbsConfigReconciler) deployOrUpdateConsolePluginService(ctx context.Context) error {
	desired := r.newConsolePluginService()
	found := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), found)
	if err != nil && k8serrors.IsNotFound(err) {
		r.log.Info("Creating console plugin Service")
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	if apiequality.Semantic.DeepEqual(found.Spec.Ports, desired.Spec.Ports) &&
		apiequality.Semantic.DeepEqual(found.Spec.Selector, desired.Spec.Selector) {
		return nil
	}
	r.log.Info("Updating console plugin Service")
	found.Spec.Ports = desired.Spec.Ports
	found.Spec.Selector = desired.Spec.Selector
	return r.Update(ctx, found)
}

func (r *KbsConfigReconciler) newConsolePluginService() *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConsolePluginName,
			Namespace: r.namespace,
			Labels:    consolePluginLabels(),
			Annotations: map[string]string{
				"service.alpha.openshift.io/serving-cert-secret-name": consolePluginCertSecret,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: consolePluginLabels(),
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{{
				Name:       "https",
				Protocol:   corev1.ProtocolTCP,
				Port:       consolePluginPort,
				TargetPort: intstr.FromInt32(consolePluginPort),
			}},
		},
	}
	_ = ctrl.SetControllerReference(r.kbsConfig, svc, r.Scheme)
	return svc
}

// --- ConsolePlugin CR ---

func (r *KbsConfigReconciler) deployOrUpdateConsolePluginCR(ctx context.Context) error {
	desired := r.newConsolePluginCR()
	found := &consolev1.ConsolePlugin{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), found)
	if err != nil && k8serrors.IsNotFound(err) {
		r.log.Info("Creating ConsolePlugin CR")
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	if apiequality.Semantic.DeepEqual(found.Spec, desired.Spec) {
		return nil
	}
	r.log.Info("Updating ConsolePlugin CR")
	found.Spec = desired.Spec
	return r.Update(ctx, found)
}

func (r *KbsConfigReconciler) newConsolePluginCR() *consolev1.ConsolePlugin {
	cp := &consolev1.ConsolePlugin{
		ObjectMeta: metav1.ObjectMeta{
			Name:   ConsolePluginName,
			Labels: consolePluginLabels(),
		},
		Spec: consolev1.ConsolePluginSpec{
			DisplayName: "Trustee (Attestation)",
			I18n: consolev1.ConsolePluginI18n{
				LoadType: consolev1.Preload,
			},
			Backend: consolev1.ConsolePluginBackend{
				Type: consolev1.Service,
				Service: &consolev1.ConsolePluginService{
					Name:      ConsolePluginName,
					Namespace: r.namespace,
					Port:      consolePluginPort,
					BasePath:  "/",
				},
			},
		},
	}
	// ConsolePlugin is cluster-scoped — no owner reference (KbsConfig is namespaced).
	return cp
}

// --- NetworkPolicy ---

func (r *KbsConfigReconciler) deployOrUpdateConsolePluginNetworkPolicy(ctx context.Context) error {
	desired := r.newConsolePluginNetworkPolicy()
	found := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), found)
	if err != nil && k8serrors.IsNotFound(err) {
		r.log.Info("Creating console plugin NetworkPolicy")
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	if apiequality.Semantic.DeepEqual(found.Spec, desired.Spec) {
		return nil
	}
	r.log.Info("Updating console plugin NetworkPolicy")
	found.Spec = desired.Spec
	return r.Update(ctx, found)
}

func (r *KbsConfigReconciler) newConsolePluginNetworkPolicy() *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt32(consolePluginPort)

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConsolePluginName,
			Namespace: r.namespace,
			Labels:    consolePluginLabels(),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": ConsolePluginName},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"kubernetes.io/metadata.name": "openshift-console",
						},
					},
				}},
				Ports: []networkingv1.NetworkPolicyPort{{
					Protocol: &tcp,
					Port:     &port,
				}},
			}},
		},
	}
	_ = ctrl.SetControllerReference(r.kbsConfig, np, r.Scheme)
	return np
}

func boolPtr(b bool) *bool   { return &b }
func int32Ptr(i int32) *int32 { return &i }

func resourceMustParse(s string) resource.Quantity {
	return resource.MustParse(s)
}
