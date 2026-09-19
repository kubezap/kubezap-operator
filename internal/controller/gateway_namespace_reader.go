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

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// kindClusterRole is the RBAC Kind value used in the RoleRef of the
// dynamically-maintained gateway namespace-reader ClusterRoleBindings below.
const kindClusterRole = "ClusterRole"

// clusterRoleGatewayNamespaceReader is the name of the static ClusterRole
// (see config/rbac/gateway_namespace_reader_role.yaml and
// charts/kubezap-operator/templates/clusterrole-gateway-namespace-reader.yaml)
// granting `get` on Namespace objects. Both manifests use this exact literal
// name — not a Helm-release-templated one — precisely so this Go constant can
// reference it as a fixed RoleRef target regardless of install method. See
// docs/design/allnamespaces-gateway-namespace-read-rbac.md.
const clusterRoleGatewayNamespaceReader = "kubezap-gateway-namespace-reader"

// clusterRoleBindingGatewayNamespaceReader is the dynamically-maintained
// ClusterRoleBinding whose Subjects list accumulates one ServiceAccount entry
// per namespace for the shared "kubezap-gateway" ServiceAccount name used by
// the kafka/amqp/nats gateways. Maintained by reconcileKafkaGateway/
// reconcileAmqpGateway/reconcileNatsGateway in integration_controller.go,
// gated on IntegrationReconciler.AllNamespacesMode.
const clusterRoleBindingGatewayNamespaceReader = "kubezap-gateway-namespace-reader"

// clusterRoleBindingWebhookGatewayNamespaceReader is the counterpart
// ClusterRoleBinding for the webhook gateway's own ServiceAccount name
// (webhookGatewayDeploymentName in gateway_deployment.go). No reconcile path
// calls ensureGatewayNamespaceReaderBinding with this name yet: the webhook
// gateway's own allNamespacesMode is currently dead code (WATCH_NAMESPACES is
// not propagated into its container), tracked separately as STORY-056. This
// constant — and the fact that ensureGatewayNamespaceReaderBinding below is
// already generic over binding/SA name — exists so STORY-056 does not need to
// invent a name or duplicate this maintenance logic; it only needs to add one
// call site once the webhook gateway's AllNamespaces mode becomes reachable.
const clusterRoleBindingWebhookGatewayNamespaceReader = "kubezap-webhook-gateway-namespace-reader"

// ensureGatewayNamespaceReaderBinding idempotently ensures that the
// ClusterRoleBinding named bindingName exists, references the
// kubezap-gateway-namespace-reader ClusterRole, and includes a Subject entry
// for the ServiceAccount {saName, saNamespace}.
//
// This is an idempotent get-and-append: re-running it for the same
// (bindingName, saName, saNamespace) is a no-op once the Subject is already
// present, and running it for a second saNamespace only appends a second
// Subject, never removing the first. See
// docs/design/allnamespaces-gateway-namespace-read-rbac.md's "stale subjects
// are accepted, not cleaned up" tradeoff — this function never removes a
// Subject.
//
// A concurrent reconcile (for a different namespace, touching the same shared
// ClusterRoleBinding) can race this call via either a resource-version
// conflict on Update, or an AlreadyExists on the initial Create; both are
// retried via retry.OnError.
func ensureGatewayNamespaceReaderBinding(ctx context.Context, c client.Client, bindingName, saName, saNamespace string) error {
	desiredSubject := rbacv1.Subject{
		Kind:      kindServiceAccount,
		Name:      saName,
		Namespace: saNamespace,
	}

	err := retry.OnError(retry.DefaultRetry, func(err error) bool {
		return apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err)
	}, func() error {
		var crb rbacv1.ClusterRoleBinding
		getErr := c.Get(ctx, client.ObjectKey{Name: bindingName}, &crb)
		switch {
		case apierrors.IsNotFound(getErr):
			crb = rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: bindingName},
				RoleRef: rbacv1.RoleRef{
					APIGroup: apiGroupRBAC,
					Kind:     kindClusterRole,
					Name:     clusterRoleGatewayNamespaceReader,
				},
				Subjects: []rbacv1.Subject{desiredSubject},
			}
			return c.Create(ctx, &crb)
		case getErr != nil:
			return getErr
		}

		for _, s := range crb.Subjects {
			if s.Kind == kindServiceAccount && s.Name == saName && s.Namespace == saNamespace {
				// Already present — nothing to do.
				return nil
			}
		}
		crb.Subjects = append(crb.Subjects, desiredSubject)
		return c.Update(ctx, &crb)
	})
	if err != nil {
		return fmt.Errorf("ensuring ClusterRoleBinding %q has Subject %s/%s: %w", bindingName, saNamespace, saName, err)
	}
	return nil
}
