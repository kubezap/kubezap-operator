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
	"fmt"
	"net/http"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// TriggerAuthAdmission emits an admission warning (non-blocking) when a webhook
// Trigger is created or updated without spec.webhook.auth configured.
// This raises visibility for operators who may not realise the endpoint is open
// to any caller, without preventing the resource from being created.
type TriggerAuthAdmission struct {
	decoder admission.Decoder
}

// Handle implements admission.Handler.
func (h *TriggerAuthAdmission) Handle(_ context.Context, req admission.Request) admission.Response {
	trigger := &automationv1alpha1.Trigger{}
	if err := h.decoder.Decode(req, trigger); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if trigger.Spec.Type == "webhook" {
		if trigger.Spec.Webhook == nil || trigger.Spec.Webhook.Auth == nil {
			msg := fmt.Sprintf(
				"Trigger %s/%s has type webhook but no spec.webhook.auth configured — "+
					"requests will be accepted from any source. "+
					"Configure spec.webhook.auth for production use.",
				trigger.Namespace,
				trigger.Name,
			)
			return admission.Allowed("").WithWarnings(msg)
		}
	}

	return admission.Allowed("")
}

// SetupTriggerWebhook registers the TriggerAuthAdmission handler with the controller-runtime
// manager. The handler is mounted at the standard kubebuilder validating webhook path for the
// Trigger resource. The caller (cmd/main.go wiring step) is responsible for registering the
// corresponding ValidatingWebhookConfiguration via make manifests.
//
// +kubebuilder:webhook:path=/validate-automation-kubezap-io-v1alpha1-trigger,mutating=false,failurePolicy=ignore,sideEffects=None,groups=automation.kubezap.io,resources=triggers,verbs=create;update,versions=v1alpha1,name=vtrigger.kb.io,admissionReviewVersions=v1
func SetupTriggerWebhook(mgr ctrl.Manager) error {
	decoder := admission.NewDecoder(mgr.GetScheme())
	mgr.GetWebhookServer().Register(
		"/validate-automation-kubezap-io-v1alpha1-trigger",
		&admission.Webhook{Handler: &TriggerAuthAdmission{decoder: decoder}},
	)
	return nil
}
