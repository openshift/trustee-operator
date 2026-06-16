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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	confidentialcontainersorgv1alpha1 "github.com/confidential-containers/trustee-operator/api/v1alpha1"
	controller "github.com/confidential-containers/trustee-operator/internal/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(confidentialcontainersorgv1alpha1.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(routev1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var secureMetrics bool
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Cancellable context: SecurityProfileWatcher cancels it when the TLS profile changes,
	// triggering a graceful shutdown so the operator restarts with the new profile.
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	defer cancel()

	cfg := ctrl.GetConfigOrDie()

	// Detect if running on OpenShift
	isOpenShift, err := isOpenShiftCluster(cfg)
	if err != nil {
		setupLog.Error(err, "unable to detect cluster type")
		os.Exit(1)
	}

	// Temporary client used only to fetch the initial TLS profile before the manager starts.
	tempClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "unable to create temporary client")
		os.Exit(1)
	}

	// On non-OpenShift clusters the config.openshift.io/APIServer CRD is absent, so skip
	// the fetch and leave tlsOptsList empty to use controller-runtime's TLS defaults.
	var (
		tlsProfileSpec configv1.TLSProfileSpec
		tlsOptsList    []func(*tls.Config)
	)
	if isOpenShift {
		tlsProfileSpec, err = openshifttls.FetchAPIServerTLSProfile(ctx, tempClient)
		if err != nil {
			setupLog.Error(err, "unable to fetch TLS profile from APIServer CR")
			os.Exit(1)
		}
		tlsOpt, unsupportedCiphers := openshifttls.NewTLSConfigFromProfile(tlsProfileSpec)
		if len(unsupportedCiphers) > 0 {
			setupLog.Info("TLS profile contains ciphers not supported by Go crypto/tls, ignoring them", "ciphers", unsupportedCiphers)
		}
		tlsOptsList = []func(*tls.Config){tlsOpt}
		setupLog.Info("Loaded TLS profile from cluster", "minTLSVersion", tlsProfileSpec.MinTLSVersion, "ciphersCount", len(tlsProfileSpec.Ciphers))
	} else {
		setupLog.Info("Non-OpenShift cluster detected, using controller-runtime TLS defaults")
	}

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOptsList,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// Configure webhook server with TLS profile (if webhooks are enabled in the future)
	webhookServerOptions := webhook.Options{
		Port:    9443,
		TLSOpts: tlsOptsList,
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhook.NewServer(webhookServerOptions),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "178dc119.confidentialcontainers.org",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = controller.KbsOperatorNamespace
	}

	// Install SecurityProfileWatcher to detect TLS profile changes on OpenShift
	if isOpenShift {
		watcher := &openshifttls.SecurityProfileWatcher{
			Client:                mgr.GetClient(),
			InitialTLSProfileSpec: tlsProfileSpec,
			OnProfileChange: func(_ context.Context, oldProfile, newProfile configv1.TLSProfileSpec) {
				setupLog.Info("TLS security profile changed, triggering graceful restart",
					"oldMinTLSVersion", oldProfile.MinTLSVersion,
					"newMinTLSVersion", newProfile.MinTLSVersion,
					"oldCipherCount", len(oldProfile.Ciphers),
					"newCipherCount", len(newProfile.Ciphers))
				cancel() // Triggers graceful shutdown which will cause the pod to restart
			},
		}
		if err := watcher.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to set up TLS profile watcher")
			os.Exit(1)
		}
		setupLog.Info("SecurityProfileWatcher installed - will restart on TLS profile changes")
	}

	err = labelNamespace(ctx, mgr, namespace)
	if err != nil {
		setupLog.Error(err, "unable to add labels to namespace")
		os.Exit(1)
	}

	if err = (&controller.KbsConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "KbsConfig")
		os.Exit(1)
	}

	if err = (&controller.TrusteeConfigReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		TLSProfileSpec: tlsProfileSpec,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TrusteeConfig")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// isOpenShiftCluster detects if running on OpenShift by checking for the APIServer CRD
func isOpenShiftCluster(cfg *rest.Config) (bool, error) {
	tempClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return false, err
	}

	apiServer := &configv1.APIServer{}
	err = tempClient.Get(context.Background(), client.ObjectKey{Name: "cluster"}, apiServer)
	if err != nil {
		if k8serrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			// Not an OpenShift cluster
			return false, nil
		}
		// Some other error occurred
		return false, err
	}
	return true, nil
}

func labelNamespace(ctx context.Context, mgr manager.Manager, nsName string) error {

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
		},
	}
	err := mgr.GetAPIReader().Get(ctx, client.ObjectKeyFromObject(ns), ns)
	if err != nil {
		setupLog.Error(err, "Unable to retrieve namespace details. Can't add label to the namespace")
		return err
	}

	setupLog.Info("Labelling Namespace")
	setupLog.Info("Labels: ", "Labels", ns.Labels)
	// Add namespace label to allow privilege pods via Pod Security Admission controller
	ns.Labels["pod-security.kubernetes.io/enforce"] = "privileged"
	ns.Labels["pod-security.kubernetes.io/audit"] = "privileged"
	ns.Labels["pod-security.kubernetes.io/warn"] = "privileged"

	return mgr.GetClient().Update(ctx, ns)
}
