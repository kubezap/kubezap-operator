/*
Copyright 2026.

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

package ui

import (
	"context"
	"fmt"
	"log"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Runnable integrates the UI HTTP server with the controller-runtime manager lifecycle.
// It implements manager.Runnable so the server starts after the manager cache is synced
// and shuts down cleanly when the context is cancelled.
//
// When Namespace is set, Runnable also ensures a Kubernetes Service exists exposing the
// UI port — the operator manages this Service automatically when --enable-ui is set.
type Runnable struct {
	Server    *Server
	Port      int
	Namespace string // POD_NAMESPACE; if empty, no Service is managed
	Client    client.Client
}

// Start implements manager.Runnable.
func (r *Runnable) Start(ctx context.Context) error {
	if r.Namespace != "" {
		r.ensureService(ctx)
	}
	return r.Server.Start(ctx, fmt.Sprintf(":%d", r.Port))
}

// ensureService creates or updates the kubezap-ui Service in the operator namespace.
// It is a best-effort operation — failure is logged but does not stop the server.
func (r *Runnable) ensureService(ctx context.Context) {
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubezap-ui",
			Namespace: r.Namespace,
			Labels: map[string]string{
				"control-plane":                "controller-manager",
				"app.kubernetes.io/name":       "kubezap",
				"app.kubernetes.io/component":  "ui",
				"app.kubernetes.io/managed-by": "kubezap",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"control-plane":          "controller-manager",
				"app.kubernetes.io/name": "kubezap",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "ui",
					Port:       int32(r.Port),
					TargetPort: intstr.FromInt32(int32(r.Port)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	existing := &corev1.Service{}
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		if createErr := r.Client.Create(ctx, desired); createErr != nil {
			log.Printf("ui: failed to create Service %s/%s: %v", r.Namespace, desired.Name, createErr)
		} else {
			log.Printf("ui: created Service %s/%s (port %d)", r.Namespace, desired.Name, r.Port)
		}
		return
	}
	if err != nil {
		log.Printf("ui: failed to get Service %s/%s: %v", r.Namespace, desired.Name, err)
		return
	}
	// Update port if it has changed.
	existing.Spec.Ports = desired.Spec.Ports
	if updateErr := r.Client.Update(ctx, existing); updateErr != nil {
		log.Printf("ui: failed to update Service %s/%s: %v", r.Namespace, desired.Name, updateErr)
	}
}
