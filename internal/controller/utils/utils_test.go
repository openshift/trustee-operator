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
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsRetriableDiscoveryErr(t *testing.T) {
	gr := schema.GroupResource{Group: "", Resource: "apigroups"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		// Transient failures worth retrying at startup.
		{"server timeout", apierrors.NewServerTimeout(gr, "list", 1), true},
		{"too many requests", apierrors.NewTooManyRequestsError("slow down"), true},
		{"service unavailable", apierrors.NewServiceUnavailable("api server unavailable"), true},
		{"internal error", apierrors.NewInternalError(errors.New("boom")), true},
		{"request timeout", apierrors.NewTimeoutError("timed out", 1), true},

		// Permanent failures: retrying only delays an inevitable crash.
		{"forbidden", apierrors.NewForbidden(gr, "", errors.New("rbac")), false},
		{"unauthorized", apierrors.NewUnauthorized("no token"), false},
		{"not found", apierrors.NewNotFound(gr, ""), false},
		{"plain error", errors.New("some non-apierror"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetriableDiscoveryErr(tt.err); got != tt.want {
				t.Errorf("isRetriableDiscoveryErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
