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

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// type=plugin Triggers (docs/api/plugin-contract.md, subscriber role): the
// plugin pod watches Trigger CRDs and owns the whole subscription lifecycle.
// The operator's only job is to validate that the Trigger's integrationRef
// resolves to an existing Integration of type "plugin" and surface a clear
// status condition if not. Nothing else (no informer, gateway, or per-Trigger
// state) is created, so reconcile cost stays O(1).

const (
	// triggerTypePlugin is the TriggerSpec.Type value "plugin".
	triggerTypePlugin = "plugin"

	// conditionTypePluginIntegrationInvalid is negative-polarity (matching
	// CredentialResolutionFailed): True means spec.plugin.integrationRef is
	// currently unusable; False means it resolved to a type=plugin Integration.
	conditionTypePluginIntegrationInvalid = "PluginIntegrationInvalid"

	reasonPluginIntegrationInvalid   = "PluginIntegrationInvalid"
	reasonPluginIntegrationNotFound  = "PluginIntegrationNotFound"
	reasonPluginIntegrationWrongType = "PluginIntegrationWrongType"
	reasonPluginSpecMissing          = "PluginSpecMissing"
	reasonPluginIntegrationValid     = "PluginIntegrationValid"

	// pluginIntegrationRecheckInterval is how often a Trigger with an invalid
	// integrationRef is re-validated (Integrations are not watched).
	pluginIntegrationRecheckInterval = 30 * time.Second
)

// syncPluginIntegrationRefCondition validates trg.Spec.Plugin.IntegrationRef
// and sets conditionTypePluginIntegrationInvalid on trg.Status in place (the
// caller persists status). It returns a non-empty human-readable message when
// the reference is invalid, "" when valid. Only transient API errors are
// returned as err. Idempotent: derived fresh from the live Integration.
//
// No Event is emitted: the controller holds no events:create grant, and adding
// one is a role.yaml change outside this story. The condition (and Ready=False)
// is the visibility surface.
func (r *TriggerReconciler) syncPluginIntegrationRefCondition(ctx context.Context, trg *automationv1alpha1.Trigger) (string, error) {
	reason, msg := "", ""

	if trg.Spec.Plugin == nil || trg.Spec.Plugin.IntegrationRef.Name == "" {
		reason = reasonPluginSpecMissing
		msg = "spec.plugin.integrationRef.name is required when type is plugin"
	} else {
		name := trg.Spec.Plugin.IntegrationRef.Name
		var integ automationv1alpha1.Integration
		err := r.Get(ctx, client.ObjectKey{Namespace: trg.Namespace, Name: name}, &integ)
		switch {
		case apierrors.IsNotFound(err):
			reason = reasonPluginIntegrationNotFound
			msg = fmt.Sprintf("Integration %q not found in namespace %q", name, trg.Namespace)
		case err != nil:
			return "", fmt.Errorf("getting Integration %s/%s for plugin Trigger: %w", trg.Namespace, name, err)
		case integ.Spec.Type != integrationTypePlugin:
			reason = reasonPluginIntegrationWrongType
			msg = fmt.Sprintf("Integration %q has type %q; a plugin Trigger requires type %q", name, integ.Spec.Type, integrationTypePlugin)
		}
	}

	cond := metav1.Condition{
		Type:               conditionTypePluginIntegrationInvalid,
		ObservedGeneration: trg.Generation,
	}
	if reason != "" {
		cond.Status, cond.Reason, cond.Message = metav1.ConditionTrue, reason, msg
	} else {
		cond.Status, cond.Reason = metav1.ConditionFalse, reasonPluginIntegrationValid
		cond.Message = "Integration reference resolves to a plugin Integration"
	}
	apimeta.SetStatusCondition(&trg.Status.Conditions, cond)
	return msg, nil
}
