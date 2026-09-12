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

package webhook

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

func newWebhookGatewayConfigCreateRequest(t *testing.T, name, namespace string) admission.Request {
	t.Helper()

	cfg := &automationv1alpha1.WebhookGatewayConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "automation.kubezap.io/v1alpha1",
			Kind:       "WebhookGatewayConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshaling WebhookGatewayConfig: %v", err)
	}

	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Namespace: namespace,
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}

// newTestValidator builds a WebhookGatewayConfigValidator backed by a fake client seeded
// with existing (the WebhookGatewayConfig objects "already in the cluster" for the test).
func newTestValidator(t *testing.T, existing ...*automationv1alpha1.WebhookGatewayConfig) *WebhookGatewayConfigValidator {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := automationv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding automationv1alpha1 to scheme: %v", err)
	}

	objs := make([]runtime.Object, 0, len(existing))
	for _, e := range existing {
		objs = append(objs, e)
	}

	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithRuntimeObjects(objs...)
	}

	return &WebhookGatewayConfigValidator{
		Client:  builder.Build(),
		decoder: admission.NewDecoder(scheme),
	}
}

func TestWebhookGatewayConfigValidator_AllowsFirstCreateInNamespace(t *testing.T) {
	v := newTestValidator(t)
	req := newWebhookGatewayConfigCreateRequest(t, "config-a", "team-a")

	resp := v.Handle(context.Background(), req)

	if !resp.Allowed {
		t.Fatalf("expected first create in an empty namespace to be allowed, got denied: %s", resp.Result.Message)
	}
}

func TestWebhookGatewayConfigValidator_DeniesSecondCreateInSameNamespace(t *testing.T) {
	existing := &automationv1alpha1.WebhookGatewayConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "config-a", Namespace: "team-a"},
	}
	v := newTestValidator(t, existing)
	req := newWebhookGatewayConfigCreateRequest(t, "config-b", "team-a")

	resp := v.Handle(context.Background(), req)

	if resp.Allowed {
		t.Fatalf("expected second create in a namespace that already has a WebhookGatewayConfig to be denied, got allowed")
	}
}

func TestWebhookGatewayConfigValidator_AllowsCreateInDifferentNamespace(t *testing.T) {
	existing := &automationv1alpha1.WebhookGatewayConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "config-a", Namespace: "team-a"},
	}
	v := newTestValidator(t, existing)
	req := newWebhookGatewayConfigCreateRequest(t, "config-b", "team-b")

	resp := v.Handle(context.Background(), req)

	if !resp.Allowed {
		t.Fatalf("expected create in a different, empty namespace to be allowed, got denied: %s", resp.Result.Message)
	}
}

func TestWebhookGatewayConfigValidator_AllowsUpdateEvenWithExistingObject(t *testing.T) {
	existing := &automationv1alpha1.WebhookGatewayConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "config-a", Namespace: "team-a"},
	}
	v := newTestValidator(t, existing)
	req := newWebhookGatewayConfigCreateRequest(t, "config-a", "team-a")
	req.Operation = admissionv1.Update

	resp := v.Handle(context.Background(), req)

	if !resp.Allowed {
		t.Fatalf("expected update requests to always be allowed (singleton rule only guards create), got denied: %s", resp.Result.Message)
	}
}
