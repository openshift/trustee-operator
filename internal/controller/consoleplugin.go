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

	"github.com/go-logr/logr"
	consolev1 "github.com/openshift/api/console/v1"
	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	ConsolePluginName       = "trustee-openshift-console-plugin"
	consolePluginPort       = 9443
	consolePluginCertSecret = "trustee-openshift-console-plugin-cert"
)

// ConsolePluginReconciler deploys and maintains the Trustee console plugin
// independently of any CR. It starts at operator boot and self-heals if
// any managed resource is modified or deleted.
type ConsolePluginReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	namespace string
	log       logr.Logger
}

//+kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,verbs=get;list;watch;create;update;patch;delete

func (r *ConsolePluginReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	r.log.Info("Reconciling console plugin")

	image := os.Getenv("CONSOLE_PLUGIN_IMAGE")
	if image == "" {
		r.log.Info("CONSOLE_PLUGIN_IMAGE not set, skipping console plugin deployment")
		return ctrl.Result{}, nil
	}

	if err := r.ensureConfigMap(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin ConfigMap: %w", err)
	}
	if err := r.ensureDeployment(ctx, image); err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin Deployment: %w", err)
	}
	if err := r.ensureService(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin Service: %w", err)
	}
	if err := r.ensureConsolePluginCR(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("ConsolePlugin CR: %w", err)
	}
	if err := r.ensureNetworkPolicy(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("console plugin NetworkPolicy: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *ConsolePluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.namespace = os.Getenv("POD_NAMESPACE")
	if r.namespace == "" {
		r.namespace = KbsOperatorNamespace
	}
	r.log = ctrl.Log.WithName("consoleplugin-controller")

	// Channel source fires once after informer caches sync, seeding the
	// initial reconcile that creates the plugin resources.
	ch := make(chan event.GenericEvent, 1)
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		if !mgr.GetCache().WaitForCacheSync(ctx) {
			return nil
		}
		ch <- event.GenericEvent{Object: &consolev1.ConsolePlugin{
			ObjectMeta: metav1.ObjectMeta{Name: ConsolePluginName},
		}}
		return nil
	})); err != nil {
		return err
	}

	mapToRequest := handler.EnqueueRequestsFromMapFunc(
		func(_ context.Context, _ client.Object) []reconcile.Request {
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: ConsolePluginName}}}
		},
	)

	nameFilter := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == ConsolePluginName
	})
	nsNameFilter := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == ConsolePluginName && obj.GetNamespace() == r.namespace
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named("consoleplugin").
		WatchesRawSource(source.Channel(ch, &handler.EnqueueRequestForObject{})).
		Watches(&consolev1.ConsolePlugin{}, mapToRequest, builder.WithPredicates(nameFilter)).
		Watches(&appsv1.Deployment{}, mapToRequest, builder.WithPredicates(nsNameFilter)).
		Watches(&corev1.Service{}, mapToRequest, builder.WithPredicates(nsNameFilter)).
		Watches(&corev1.ConfigMap{}, mapToRequest, builder.WithPredicates(nsNameFilter)).
		Watches(&networkingv1.NetworkPolicy{}, mapToRequest, builder.WithPredicates(nsNameFilter)).
		Complete(r)
}

// --- labels ---

func consolePluginLabels() map[string]string {
	return map[string]string{
		"app":                          ConsolePluginName,
		"app.kubernetes.io/name":       ConsolePluginName,
		"app.kubernetes.io/managed-by": "trustee-operator",
		"app.kubernetes.io/part-of":    "trustee",
		"app.kubernetes.io/component":  "console-plugin",
	}
}

// --- ConfigMap (nginx.conf) ---

func (r *ConsolePluginReconciler) ensureConfigMap(ctx context.Context) error {
	desired := r.desiredConfigMap(ctx)
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

func (r *ConsolePluginReconciler) desiredConfigMap(ctx context.Context) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConsolePluginName,
			Namespace: r.namespace,
			Labels:    consolePluginLabels(),
		},
		Data: map[string]string{
			"nginx.conf": r.buildNginxConf(ctx),
		},
	}
}

func (r *ConsolePluginReconciler) buildNginxConf(ctx context.Context) string {
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

// --- TLS profile → nginx directives ---

type nginxTLSDirectives struct {
	protocols string
	ciphers   string
	ecdhCurve string
}

var pqcECDHCurves = []string{"SecP256r1MLKEM768", "SecP384r1MLKEM1024"}
var defaultClassicCurves = []string{"secp256r1", "secp384r1"}

func (r *ConsolePluginReconciler) nginxTLSFromCluster(ctx context.Context) nginxTLSDirectives {
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
	nd.protocols = nginxProtocols(spec.MinTLSVersion)

	ciphers := filterDHECiphers(spec.Ciphers)
	if len(ciphers) > 0 {
		nd.ciphers = strings.Join(ciphers, ":")
	} else {
		nd.ciphers = "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305"
	}

	nd.ecdhCurve = strings.Join(append(pqcECDHCurves, defaultClassicCurves...), ":")
	return nd
}

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

func (r *ConsolePluginReconciler) ensureDeployment(ctx context.Context, image string) error {
	desired := r.desiredDeployment(image)
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

func (r *ConsolePluginReconciler) desiredDeployment(image string) *appsv1.Deployment {
	replicas := int32(2)
	labels := consolePluginLabels()

	return &appsv1.Deployment{
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
}

// --- Service ---

func (r *ConsolePluginReconciler) ensureService(ctx context.Context) error {
	desired := r.desiredService()
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

func (r *ConsolePluginReconciler) desiredService() *corev1.Service {
	return &corev1.Service{
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
}

// --- ConsolePlugin CR ---

func (r *ConsolePluginReconciler) ensureConsolePluginCR(ctx context.Context) error {
	desired := r.desiredConsolePluginCR()
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

func (r *ConsolePluginReconciler) desiredConsolePluginCR() *consolev1.ConsolePlugin {
	return &consolev1.ConsolePlugin{
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
}

// --- NetworkPolicy ---

func (r *ConsolePluginReconciler) ensureNetworkPolicy(ctx context.Context) error {
	desired := r.desiredNetworkPolicy()
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

func (r *ConsolePluginReconciler) desiredNetworkPolicy() *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt32(consolePluginPort)

	return &networkingv1.NetworkPolicy{
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
}

// --- helpers ---

func boolPtr(b bool) *bool   { return &b }
func int32Ptr(i int32) *int32 { return &i }

func resourceMustParse(s string) resource.Quantity {
	return resource.MustParse(s)
}
