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
package utils

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// discoveryTimeout bounds a single API discovery attempt so an unresponsive API
// server cannot block the operator before the manager starts.
const discoveryTimeout = 10 * time.Second

// PlatformCapabilities reports which OpenShift-specific API capabilities the
// cluster serves. The two flags are independent because a cluster may expose
// one API group without the other, and different call sites depend on
// different groups (e.g. Route creation vs. reading the APIServer TLS profile).
type PlatformCapabilities struct {
	// HasRouteAPI is true when route.openshift.io is served, i.e. OpenShift
	// Routes can be created (used for operand exposure and OpenShift-specific
	// NetworkPolicy peers such as openshift-dns).
	HasRouteAPI bool
	// HasConfigAPI is true when config.openshift.io is served, i.e. the cluster
	// config API (including the APIServer CR that carries the TLS profile) is
	// available.
	HasConfigAPI bool
}

// DetectPlatformCapabilities queries the cluster's API groups and reports which
// OpenShift API capabilities are available. The discovery attempt is bounded by
// discoveryTimeout, and the call also returns promptly if ctx is cancelled.
// Because the per-attempt timeout is bounded, the discovery goroutine cannot run
// indefinitely even after the caller has returned.
func DetectPlatformCapabilities(ctx context.Context, config *rest.Config) (PlatformCapabilities, error) {
	caps := PlatformCapabilities{}

	// DiscoveryClient.ServerGroups() has no context-aware variant in this
	// client-go version (it uses context.TODO() internally), so a client-side
	// timeout on a copied config is what actually bounds each HTTP request.
	// Copy the config to avoid mutating the caller's.
	cfg := rest.CopyConfig(config)
	if cfg.Timeout <= 0 || cfg.Timeout > discoveryTimeout {
		cfg.Timeout = discoveryTimeout
	}

	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return caps, err
	}

	// Run the (context-unaware) discovery call in a goroutine so we can return
	// promptly when ctx is cancelled; the per-attempt timeout guarantees the
	// goroutine cannot leak indefinitely.
	type result struct {
		groups *metav1.APIGroupList
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		groups, err := dc.ServerGroups()
		ch <- result{groups: groups, err: err}
	}()

	select {
	case <-ctx.Done():
		return caps, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return caps, res.err
		}
		for _, group := range res.groups.Groups {
			switch group.Name {
			case "route.openshift.io":
				caps.HasRouteAPI = true
			case "config.openshift.io":
				caps.HasConfigAPI = true
			}
		}
		return caps, nil
	}
}
