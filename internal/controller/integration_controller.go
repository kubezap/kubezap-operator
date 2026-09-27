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
	"os"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// integrationTypeKafka is the IntegrationSpec.Type / TriggerSpec.Type value "kafka".
const integrationTypeKafka = "kafka"

// kafkaGatewayMetricsPort must match cmd/kafka-gateway/main.go's --metrics-port
// default (:9090), which is what the binary actually listens on for /metrics —
// see docs/guides/observability.md.
const kafkaGatewayMetricsPort = int32(9090)

// componentKafkaGateway is the "kafka-gateway" value used for the labelComponent
// label and the gateway container name, mirroring componentWebhookGateway's
// convention in gateway_deployment.go.
const componentKafkaGateway = "kafka-gateway"

// integrationTypeHTTP is the IntegrationSpec.Type value "http" (an HTTP
// Integration providing shared base URL/auth/default headers to HTTP steps).
const integrationTypeHTTP = "http"

// triggerTypeWebhook is the TriggerSpec.Type value "webhook".
const triggerTypeWebhook = "webhook"

// integrationTypePlugin is the IntegrationSpec.Type value "plugin".
const integrationTypePlugin = "plugin"

// sharedGatewayServiceAccountName is the name of the ServiceAccount (and
// matching Role/RoleBinding) shared by the kafka, amqp, and nats gateway
// Deployments in a namespace.
const sharedGatewayServiceAccountName = "kubezap-gateway"

// resourceIntegrations is the RBAC resource name for the Integration CRD,
// granted read-only to the kafka/amqp/nats gateway Roles so they can resolve
// their own Integration's config.
const resourceIntegrations = "integrations"

// Env var names injected into plugin/gateway Deployments.
const (
	envVarKubezapNamespace       = "KUBEZAP_NAMESPACE"
	envVarKubezapIntegrationName = "KUBEZAP_INTEGRATION_NAME"
	envVarWatchNamespaces        = "WATCH_NAMESPACES"
	envVarLogLevel               = "LOG_LEVEL"
	defaultLogLevel              = "info"
)

// KUBEZAP_MTLS_* env vars injected into a plugin Deployment when
// spec.plugin.mtls.enabled is true. See docs/api/plugin-contract.md's mTLS
// section for the contract a plugin must implement to consume these.
const (
	envVarMTLSEnabled    = "KUBEZAP_MTLS_ENABLED"
	envVarMTLSCertFile   = "KUBEZAP_MTLS_CERT_FILE"
	envVarMTLSKeyFile    = "KUBEZAP_MTLS_KEY_FILE"
	envVarMTLSCAFile     = "KUBEZAP_MTLS_CA_FILE"
	envVarMTLSHealthPort = "KUBEZAP_MTLS_HEALTH_PORT"

	// pluginMTLSMountPath is where the per-Integration mTLS Secret (tls.crt,
	// tls.key, ca.crt) is mounted into the plugin container.
	pluginMTLSMountPath = "/etc/kubezap/mtls"

	// defaultPluginMTLSHealthPort is the plain-HTTP port a plugin Deployment
	// must serve GET /healthz on when spec.plugin.mtls.enabled=true. It is a
	// separate port from PublisherPort because a TLS listener configured
	// with tls.RequireAndVerifyClientCert rejects the handshake before the
	// HTTP layer ever sees the request path — kubelet's readinessProbe
	// httpGet never presents a client certificate, so GET /healthz cannot be
	// exempted "by path" on the same mTLS-protected port. This mirrors the
	// controller<->http-executor channel's executorHealthPort split (see
	// executor_reconciler.go's doc comment on that constant).
	defaultPluginMTLSHealthPort = int32(8091)

	// pluginMTLSVolumeName is the Volume/VolumeMount name for the mounted
	// per-Integration mTLS Secret.
	pluginMTLSVolumeName = "mtls-certs"

	// portNameMTLSHealth is the plugin Deployment's health-check ContainerPort
	// name when mTLS is enabled.
	portNameMTLSHealth = "mtls-health"
)

// pluginMTLSSecretName returns the name of the per-Integration Secret holding
// the plugin's mTLS server cert + CA (kubezap-plugin-<name>-mtls). Owner-
// referenced to the Integration so it is garbage-collected on deletion.
func pluginMTLSSecretName(integrationName string) string {
	return "kubezap-plugin-" + integrationName + "-mtls"
}

// scaledObjectKind is the KEDA ScaledObject Kind, used both in the
// unstructured object's "kind" field and its GroupVersionKind.
const scaledObjectKind = "ScaledObject"

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations/status,verbs=get;update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=keda.sh,resources=scaledobjects,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete

// IntegrationReconciler reconciles an Integration object.
type IntegrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// PluginMTLSStore tracks per-Integration mTLS CA/cert bundles for plugin
	// Integrations with spec.plugin.mtls.enabled=true. Lazily initialized by
	// pluginMTLSStore() if left nil (e.g. in tests, or before cmd/main.go is
	// updated to share one instance with the /publish caller — see
	// plugin_mtls.go's PluginMTLSStore doc comment).
	PluginMTLSStore *PluginMTLSStore
}

// pluginMTLSStore returns r.PluginMTLSStore, lazily initializing it on first
// use. IntegrationReconciler.Reconcile runs with controller-runtime's default
// concurrency (MaxConcurrentReconciles=1, see SetupWithManager below), so this
// lazy check-and-set is safe without an additional mutex.
func (r *IntegrationReconciler) pluginMTLSStore() *PluginMTLSStore {
	if r.PluginMTLSStore == nil {
		r.PluginMTLSStore = NewPluginMTLSStore()
	}
	return r.PluginMTLSStore
}

func (r *IntegrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var integration automationv1alpha1.Integration
	if err := r.Get(ctx, req.NamespacedName, &integration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Validate spec based on type.
	if err := validateIntegrationSpec(integration.Spec); err != nil {
		log.Info("Integration spec validation failed", "integration", req.NamespacedName, "error", err)
		cond := metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidSpec",
			Message:            err.Error(),
			ObservedGeneration: integration.Generation,
		}
		apimeta.SetStatusCondition(&integration.Status.Conditions, cond)
		if err := r.Status().Update(ctx, &integration); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// requeueAfter is non-zero only when this pass needs a periodic follow-up
	// reconcile independent of any spec/status change — currently just the
	// plugin mTLS rotation recheck below.
	var requeueAfter time.Duration

	// Type-specific reconciliation.
	switch integration.Spec.Type {
	case integrationTypePlugin:
		deploymentName := "kubezap-plugin-" + integration.Name
		if err := r.reconcilePluginRBAC(ctx, &integration); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling plugin RBAC: %w", err)
		}
		var mtlsErr error
		requeueAfter, mtlsErr = r.reconcilePluginMTLS(ctx, &integration)
		if mtlsErr != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling plugin mTLS: %w", mtlsErr)
		}
		if err := r.reconcilePluginDeployment(ctx, &integration); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling plugin deployment: %w", err)
		}
		integration.Status.GatewayDeploymentName = deploymentName
	case integrationTypeKafka:
		deploymentName, err := r.reconcileKafkaGateway(ctx, &integration)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling kafka gateway: %w", err)
		}
		if err := r.reconcileKafkaScaledObject(ctx, &integration); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling kafka scaledobject: %w", err)
		}
		integration.Status.GatewayDeploymentName = deploymentName
		apimeta.SetStatusCondition(&integration.Status.Conditions,
			r.gatewayAvailableCondition(ctx, integration.Namespace, deploymentName, "Kafka", integration.Generation))
	case "amqp":
		deploymentName, err := r.reconcileAmqpGateway(ctx, &integration)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling amqp gateway: %w", err)
		}
		integration.Status.GatewayDeploymentName = deploymentName
		apimeta.SetStatusCondition(&integration.Status.Conditions,
			r.gatewayAvailableCondition(ctx, integration.Namespace, deploymentName, "AMQP", integration.Generation))
	case "nats":
		deploymentName, err := r.reconcileNatsGateway(ctx, &integration)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling nats gateway: %w", err)
		}
		integration.Status.GatewayDeploymentName = deploymentName
		apimeta.SetStatusCondition(&integration.Status.Conditions,
			r.gatewayAvailableCondition(ctx, integration.Namespace, deploymentName, "NATS", integration.Generation))
	}

	// Set Ready=True after successful reconcile.
	now := metav1.Now()
	cond := metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             "IntegrationReady",
		Message:            "Integration is configured and ready",
		ObservedGeneration: integration.Generation,
	}
	apimeta.SetStatusCondition(&integration.Status.Conditions, cond)
	integration.Status.LastReconciledTime = &now

	if err := r.Status().Update(ctx, &integration); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// gatewayAvailableCondition fetches the named Deployment and returns a
// GatewayAvailable metav1.Condition reflecting whether it has available replicas.
// gatewayType is a human-readable label used in the condition message (e.g. "Kafka").
func (r *IntegrationReconciler) gatewayAvailableCondition(
	ctx context.Context,
	namespace, deploymentName, gatewayType string,
	generation int64,
) metav1.Condition {
	existingDep := &appsv1.Deployment{}
	depKey := client.ObjectKey{Name: deploymentName, Namespace: namespace}
	if err := r.Get(ctx, depKey, existingDep); err == nil && existingDep.Status.AvailableReplicas > 0 {
		return metav1.Condition{
			Type:               "GatewayAvailable",
			Status:             metav1.ConditionTrue,
			Reason:             "DeploymentAvailable",
			Message:            gatewayType + " gateway Deployment has available replicas",
			ObservedGeneration: generation,
		}
	}
	return metav1.Condition{
		Type:               "GatewayAvailable",
		Status:             metav1.ConditionFalse,
		Reason:             "DeploymentUnavailable",
		Message:            gatewayType + " gateway Deployment has no available replicas yet",
		ObservedGeneration: generation,
	}
}

// validateIntegrationSpec validates the Integration spec and returns the first error found.
func validateIntegrationSpec(spec automationv1alpha1.IntegrationSpec) error {
	switch spec.Type {
	case integrationTypeKafka:
		if spec.Kafka == nil {
			return fmt.Errorf("spec.kafka must be set when type=kafka")
		}
		if len(spec.Kafka.BootstrapServers) == 0 {
			return fmt.Errorf("spec.kafka.bootstrapServers must be non-empty when type=kafka")
		}
	case "amqp":
		if spec.Amqp == nil {
			return fmt.Errorf("spec.amqp must be set when type=amqp")
		}
		if spec.Amqp.URL == "" {
			return fmt.Errorf("spec.amqp.url must be non-empty")
		}
	case "nats":
		if spec.Nats == nil {
			return fmt.Errorf("spec.nats must be set when type=nats")
		}
		if len(spec.Nats.Servers) == 0 {
			return fmt.Errorf("spec.nats.servers must be non-empty")
		}
	case integrationTypeHTTP:
		if spec.HTTP == nil {
			return fmt.Errorf("spec.http must be set when type=http")
		}
	case integrationTypePlugin:
		if spec.Plugin == nil {
			return fmt.Errorf("spec.plugin must be set when type=plugin")
		}
		if spec.Plugin.Image == "" {
			return fmt.Errorf("spec.plugin.image must be non-empty when type=plugin")
		}
	default:
		return fmt.Errorf("unknown integration type %q", spec.Type)
	}
	return nil
}

// reconcilePluginRBAC ensures a ServiceAccount, Role, and RoleBinding exist for a plugin Integration
// and are kept up to date on every reconcile pass. All three resources are owner-referenced to the
// Integration so they are garbage-collected when the Integration is deleted.
func (r *IntegrationReconciler) reconcilePluginRBAC(ctx context.Context, integration *automationv1alpha1.Integration) error {
	log := logf.FromContext(ctx)
	resourceName := "kubezap-plugin-" + integration.Name

	desiredRules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{apiGroupAutomation},
			Resources: []string{resourceTriggers},
			Verbs:     []string{verbGet, verbList, verbWatch},
		},
		{
			APIGroups: []string{apiGroupAutomation},
			Resources: []string{resourceFlowRuns},
			Verbs:     []string{verbGet, verbList, "create", "update", "patch"},
		},
	}
	desiredRoleRef := rbacv1.RoleRef{
		APIGroup: apiGroupRBAC,
		Kind:     kindRole,
		Name:     resourceName,
	}
	desiredSubjects := []rbacv1.Subject{
		{
			Kind:      kindServiceAccount,
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
	}

	// --- ServiceAccount ---
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
	}
	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		// ServiceAccount has no spec fields to reconcile beyond metadata/owner reference.
		return ctrl.SetControllerReference(integration, sa, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("upserting plugin ServiceAccount: %w", err)
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled plugin ServiceAccount", "name", resourceName, "namespace", integration.Namespace, "result", result)
	}

	// --- Role ---
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
	}
	result, err = controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		// Always overwrite Rules to pick up any permission changes.
		role.Rules = desiredRules
		return ctrl.SetControllerReference(integration, role, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("upserting plugin Role: %w", err)
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled plugin Role", "name", resourceName, "namespace", integration.Namespace, "result", result)
	}

	// --- RoleBinding ---
	// RoleRef is immutable after creation. If it has changed, delete and recreate.
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
	}
	result, err = controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		// If the RoleBinding already exists with a different RoleRef we must delete and recreate
		// because RoleRef is immutable. Signal this by returning a typed sentinel.
		if rb.ResourceVersion != "" && rb.RoleRef != desiredRoleRef {
			return errRoleRefChanged
		}
		rb.RoleRef = desiredRoleRef
		rb.Subjects = desiredSubjects
		return ctrl.SetControllerReference(integration, rb, r.Scheme)
	})
	if err != nil {
		if err == errRoleRefChanged {
			// Delete the old RoleBinding and create a fresh one with the correct RoleRef.
			if delErr := r.Delete(ctx, rb); delErr != nil && !apierrors.IsNotFound(delErr) {
				return fmt.Errorf("deleting stale plugin RoleBinding: %w", delErr)
			}
			rb = &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: integration.Namespace,
				},
				RoleRef:  desiredRoleRef,
				Subjects: desiredSubjects,
			}
			if err := ctrl.SetControllerReference(integration, rb, r.Scheme); err != nil {
				return fmt.Errorf("setting owner reference on recreated RoleBinding: %w", err)
			}
			if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("recreating plugin RoleBinding: %w", err)
			}
			log.Info("recreated plugin RoleBinding (RoleRef changed)", "name", resourceName, "namespace", integration.Namespace)
		} else {
			return fmt.Errorf("upserting plugin RoleBinding: %w", err)
		}
	} else if result != controllerutil.OperationResultNone {
		log.Info("reconciled plugin RoleBinding", "name", resourceName, "namespace", integration.Namespace, "result", result)
	}

	return nil
}

// errRoleRefChanged is a sentinel error returned from a CreateOrUpdate mutate function when the
// existing RoleBinding's RoleRef does not match the desired value. Because RoleRef is immutable in
// Kubernetes, the RoleBinding must be deleted and recreated rather than updated.
var errRoleRefChanged = fmt.Errorf("rolebinding RoleRef has changed and must be recreated")

// pluginMTLSRecheckInterval is how often reconcilePluginMTLS asks
// IntegrationReconciler.Reconcile to be re-invoked for an mTLS-enabled plugin
// Integration, purely so PluginMTLSStore.GetOrGenerate gets a chance to
// notice NeedsRotation() and rotate the bundle. This mirrors the 5-minute
// ticker cmd/main.go runs for the executor channel's MTLSBundle rotation
// (see cmd/main.go's "Start mTLS rotation goroutine" comment) — the
// per-Integration bundle here is instead rotated inline by the reconciler
// itself, since a single global background goroutine has no natural way to
// iterate "every opted-in Integration" without its own Integration lister.
const pluginMTLSRecheckInterval = 5 * time.Minute

// reconcilePluginMTLS reconciles the per-Integration mTLS Secret for a plugin
// Integration and returns the RequeueAfter duration the caller should apply
// (zero when mTLS is disabled for this Integration, since no periodic
// rotation recheck is needed in that case).
//
// When spec.plugin.mtls.enabled is true: generates (or rotates, transparently
// via PluginMTLSStore.GetOrGenerate) a per-Integration CA + server/client
// cert pair, and upserts a Secret containing the server cert + CA that
// desiredPluginDeployment mounts into the plugin container. The controller's
// own client cert stays in r.pluginMTLSStore() only — it is never written to
// a Secret or to etcd.
//
// When false (the default): removes any tracked bundle from the store and
// best-effort deletes a previously-created mTLS Secret, so disabling mTLS on
// an Integration cleans up after itself rather than leaving an orphaned
// Secret and an unrotated bundle sitting in memory forever.
func (r *IntegrationReconciler) reconcilePluginMTLS(ctx context.Context, integration *automationv1alpha1.Integration) (time.Duration, error) {
	log := logf.FromContext(ctx)
	plugin := integration.Spec.Plugin

	enabled := plugin != nil && plugin.Mtls != nil && plugin.Mtls.Enabled
	if !enabled {
		r.pluginMTLSStore().Remove(integration.Namespace, integration.Name)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pluginMTLSSecretName(integration.Name),
				Namespace: integration.Namespace,
			},
		}
		if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
			return 0, fmt.Errorf("deleting stale plugin mTLS secret: %w", err)
		}
		return 0, nil
	}

	deploymentName := "kubezap-plugin-" + integration.Name
	serverDNSNames := []string{
		fmt.Sprintf("%s.%s.svc", deploymentName, integration.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", deploymentName, integration.Namespace),
	}

	bundle, err := r.pluginMTLSStore().GetOrGenerate(integration.Namespace, integration.Name, serverDNSNames)
	if err != nil {
		return 0, fmt.Errorf("generating plugin mTLS bundle: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pluginMTLSSecretName(integration.Name),
			Namespace: integration.Namespace,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = map[string][]byte{
			"tls.crt": bundle.ServerCertPEM(),
			"tls.key": bundle.ServerKeyPEM(),
			"ca.crt":  bundle.CACertPEM(),
		}
		return ctrl.SetControllerReference(integration, secret, r.Scheme)
	})
	if err != nil {
		return 0, fmt.Errorf("upserting plugin mTLS secret: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled plugin mTLS secret", "name", secret.Name, "namespace", integration.Namespace, "result", op)
	}

	apimeta.SetStatusCondition(&integration.Status.Conditions, metav1.Condition{
		Type:               "PluginMTLSReady",
		Status:             metav1.ConditionTrue,
		Reason:             "CertBundleReconciled",
		Message:            fmt.Sprintf("mTLS cert bundle reconciled, expires %s", bundle.ExpiresAt.Format(time.RFC3339)),
		ObservedGeneration: integration.Generation,
	})

	return pluginMTLSRecheckInterval, nil
}

// reconcilePluginDeployment ensures the plugin Deployment exists and is up to date.
// It also records the resolved image reference in the PluginDeployed condition for auditability.
func (r *IntegrationReconciler) reconcilePluginDeployment(ctx context.Context, integration *automationv1alpha1.Integration) error {
	log := logf.FromContext(ctx)
	desired := desiredPluginDeployment(integration)

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		// desired is populated with the live object by CreateOrUpdate before this func
		// is called. Overwrite the full spec from the helper so that env vars,
		// security contexts, resource limits, probes, args, and service account
		// all stay in sync and do not drift silently.
		desired.Spec = desiredPluginDeployment(integration).Spec
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create/update plugin deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled plugin deployment", "deployment", desired.Name, "namespace", integration.Namespace, "result", op)
	}

	// Record the resolved image reference in a condition for operator auditability.
	imageRef := pluginImageRef(integration.Spec.Plugin)
	pluginDeployedCond := metav1.Condition{
		Type:               "PluginDeployed",
		Status:             metav1.ConditionTrue,
		Reason:             "DeploymentReconciled",
		Message:            fmt.Sprintf("Plugin Deployment reconciled with image %s", imageRef),
		ObservedGeneration: integration.Generation,
	}
	apimeta.SetStatusCondition(&integration.Status.Conditions, pluginDeployedCond)

	return nil
}

// pluginImageRef returns the fully-resolved container image reference for a plugin.
// When ImageDigest is set the reference is constructed as "image@sha256:<digest>" to
// pin the Deployment to an exact content-addressed layer and prevent silent tag overwrites.
func pluginImageRef(plugin *automationv1alpha1.PluginIntegrationSpec) string {
	if plugin.ImageDigest != "" {
		return plugin.Image + "@sha256:" + plugin.ImageDigest
	}
	return plugin.Image
}

// desiredPluginDeployment returns the desired Deployment for a plugin Integration.
func desiredPluginDeployment(integration *automationv1alpha1.Integration) *appsv1.Deployment {
	plugin := integration.Spec.Plugin
	publisherPort := plugin.PublisherPort
	if publisherPort == 0 {
		publisherPort = 8090
	}

	deploymentName := "kubezap-plugin-" + integration.Name
	labels := map[string]string{
		labelApp: deploymentName,
	}
	replicas := int32(1)

	envVars := []corev1.EnvVar{
		{Name: envVarKubezapNamespace, Value: integration.Namespace},
		{Name: envVarKubezapIntegrationName, Value: integration.Name},
		{Name: "KUBEZAP_PUBLISHER_PORT", Value: fmt.Sprintf("%d", publisherPort)},
		{Name: "KUBEZAP_LOG_LEVEL", Value: defaultLogLevel},
	}
	// Append any user-specified env vars.
	envVars = append(envVars, plugin.Env...)

	// NOTE: The operator does not auto-grant the plugin Deployment permission to read these
	// Secrets. The cluster administrator must create a Role + RoleBinding granting
	// the plugin ServiceAccount access to the referenced Secrets.

	// Inject secret-derived env vars from spec.plugin.secretRefs
	for _, secretRef := range plugin.SecretRefs {
		for secretKey, envVarName := range secretRef.EnvVarMappings {
			envVars = append(envVars, corev1.EnvVar{
				Name: envVarName,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: secretRef.SecretName},
						Key:                  secretKey,
					},
				},
			})
		}
	}

	ports := []corev1.ContainerPort{
		{Name: "publisher", ContainerPort: publisherPort, Protocol: corev1.ProtocolTCP},
	}

	// healthPort/healthScheme default to the publisher port over plain HTTP
	// (today's behavior, unchanged when mTLS is not enabled).
	healthPort := publisherPort
	var volumes []corev1.Volume
	var volumeMounts []corev1.VolumeMount

	mtlsEnabled := plugin.Mtls != nil && plugin.Mtls.Enabled
	if mtlsEnabled {
		// The main publisher port now requires client certs (plugin-side TLS
		// listener with RequireAndVerifyClientCert), so kubelet's readiness
		// probe — which never presents a client cert — must hit a separate
		// plain-HTTP health port instead. See defaultPluginMTLSHealthPort's
		// doc comment.
		healthPort = defaultPluginMTLSHealthPort

		envVars = append(envVars,
			corev1.EnvVar{Name: envVarMTLSEnabled, Value: "true"},
			corev1.EnvVar{Name: envVarMTLSCertFile, Value: pluginMTLSMountPath + "/tls.crt"},
			corev1.EnvVar{Name: envVarMTLSKeyFile, Value: pluginMTLSMountPath + "/tls.key"},
			corev1.EnvVar{Name: envVarMTLSCAFile, Value: pluginMTLSMountPath + "/ca.crt"},
			corev1.EnvVar{Name: envVarMTLSHealthPort, Value: fmt.Sprintf("%d", healthPort)},
		)

		ports = append(ports, corev1.ContainerPort{
			Name:          portNameMTLSHealth,
			ContainerPort: healthPort,
			Protocol:      corev1.ProtocolTCP,
		})

		volumeMounts = []corev1.VolumeMount{
			{
				Name:      pluginMTLSVolumeName,
				MountPath: pluginMTLSMountPath,
				ReadOnly:  true,
			},
		}
		volumes = []corev1.Volume{
			{
				Name: pluginMTLSVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: pluginMTLSSecretName(integration.Name),
					},
				},
			},
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: integration.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: deploymentName,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
					},
					Volumes: volumes,
					Containers: []corev1.Container{
						{
							Name:         "plugin",
							Image:        pluginImageRef(plugin),
							Ports:        ports,
							Env:          envVars,
							VolumeMounts: volumeMounts,
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             ptr.To(true),
								ReadOnlyRootFilesystem:   ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
							},
							// GET /healthz is always plain HTTP, even when mtlsEnabled
							// — see healthPort's assignment above. This is the plugin
							// contract's explicit exemption of the health check from
							// the client-cert requirement (kubelet readinessProbe
							// compatibility): kubelet's httpGet probe never presents a
							// client certificate, so it can only ever succeed against a
							// port that does not require one.
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   healthzPath,
										Port:   intstr.FromInt32(healthPort),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
						},
					},
				},
			},
		},
	}
}

// reconcileKafkaGateway ensures the Kafka gateway ServiceAccount, Role, RoleBinding, and
// Deployment exist and are up to date. It returns the Deployment name.
func (r *IntegrationReconciler) reconcileKafkaGateway(ctx context.Context, integration *automationv1alpha1.Integration) (string, error) {
	log := logf.FromContext(ctx)

	ns := integration.Namespace

	// Ensure ServiceAccount (shared kubezap-gateway SA with amqp/nats gateways).
	// No owner reference: shared across all broker-type integrations in the namespace.
	// Setting an owner ref to this Integration would GC the SA when this Integration
	// is deleted, even if amqp or nats integrations still exist and need the SA.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns}}
	saResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting kafka gateway ServiceAccount: %w", err)
	}
	if saResult != controllerutil.OperationResultNone {
		log.Info("reconciled kafka gateway ServiceAccount", "namespace", ns, "result", saResult)
	}

	// Ensure Role (shared kubezap-gateway Role).
	kafkaGatewayRules := []rbacv1.PolicyRule{
		{APIGroups: []string{apiGroupAutomation}, Resources: []string{resourceTriggers}, Verbs: []string{verbGet, verbList, verbWatch}},
		// list/watch (not just get) let the gateway notice an Integration's
		// secretRef being repointed to a different Secret without the
		// referencing Trigger itself being touched — see
		// docs/design/integration-secretref-change-detection.md.
		{APIGroups: []string{apiGroupAutomation}, Resources: []string{resourceIntegrations}, Verbs: []string{verbGet, verbList, verbWatch}},
		{APIGroups: []string{apiGroupAutomation}, Resources: []string{resourceFlowRuns}, Verbs: []string{verbCreate}},
		// Required to resolve Integration SASL/TLS secrets, and to watch them so a
		// rotated credential is picked up without waiting for the Trigger or
		// Integration to be reconciled again for an unrelated reason.
		{APIGroups: []string{""}, Resources: []string{resourceSecrets}, Verbs: []string{verbGet, verbList, verbWatch}},
	}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns}}
	roleResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = kafkaGatewayRules
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting kafka gateway Role: %w", err)
	}
	if roleResult != controllerutil.OperationResultNone {
		log.Info("reconciled kafka gateway Role", "namespace", ns, "result", roleResult)
	}

	// Ensure RoleBinding (shared kubezap-gateway RoleBinding).
	// RoleRef is immutable — if it has changed the binding must be deleted and recreated.
	kafkaDesiredRoleRef := rbacv1.RoleRef{APIGroup: apiGroupRBAC, Kind: kindRole, Name: sharedGatewayServiceAccountName}
	kafkaDesiredSubjects := []rbacv1.Subject{{Kind: kindServiceAccount, Name: sharedGatewayServiceAccountName, Namespace: ns}}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns}}
	rbResult, rbErr := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if rb.ResourceVersion != "" && rb.RoleRef != kafkaDesiredRoleRef {
			return errRoleRefChanged
		}
		rb.RoleRef = kafkaDesiredRoleRef
		rb.Subjects = kafkaDesiredSubjects
		return nil
	})
	if rbErr != nil {
		if rbErr == errRoleRefChanged {
			if delErr := r.Delete(ctx, rb); delErr != nil && !apierrors.IsNotFound(delErr) {
				return "", fmt.Errorf("deleting stale kafka gateway RoleBinding: %w", delErr)
			}
			rb = &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns},
				RoleRef:    kafkaDesiredRoleRef,
				Subjects:   kafkaDesiredSubjects,
			}
			if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
				return "", fmt.Errorf("recreating kafka gateway RoleBinding: %w", err)
			}
			log.Info("recreated kafka gateway RoleBinding (RoleRef changed)", "namespace", ns)
		} else {
			return "", fmt.Errorf("upserting kafka gateway RoleBinding: %w", rbErr)
		}
	} else if rbResult != controllerutil.OperationResultNone {
		log.Info("reconciled kafka gateway RoleBinding", "namespace", ns, "result", rbResult)
	}

	desired := desiredKafkaGatewayDeployment(integration)

	if err := ctrl.SetControllerReference(integration, desired, r.Scheme); err != nil {
		return "", fmt.Errorf("setting owner reference on kafka gateway Deployment: %w", err)
	}

	deploymentName := desired.Name
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		// desired is populated with the live object by CreateOrUpdate before this func
		// is called. Overwrite the full spec so that env vars, security contexts,
		// resource limits, probes, args, and service account do not drift silently.
		desired.Spec = desiredKafkaGatewayDeployment(integration).Spec
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to create/update kafka gateway deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled kafka gateway deployment", "deployment", deploymentName, "namespace", integration.Namespace, "result", op)
	}

	desiredSvc := desiredKafkaGatewayService(integration)
	if err := ctrl.SetControllerReference(integration, desiredSvc, r.Scheme); err != nil {
		return "", fmt.Errorf("setting owner reference on kafka gateway Service: %w", err)
	}
	svcOp, err := controllerutil.CreateOrUpdate(ctx, r.Client, desiredSvc, func() error {
		// Preserve the ClusterIP/other server-assigned fields CreateOrUpdate
		// populates from the live object; only the ports need to stay in sync.
		desiredSvc.Spec.Ports = desiredKafkaGatewayService(integration).Spec.Ports
		desiredSvc.Spec.Selector = desiredKafkaGatewayService(integration).Spec.Selector
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to create/update kafka gateway service: %w", err)
	}
	if svcOp != controllerutil.OperationResultNone {
		log.Info("reconciled kafka gateway service", "service", desiredSvc.Name, "namespace", integration.Namespace, "result", svcOp)
	}

	return deploymentName, nil
}

// desiredKafkaGatewayService returns the desired metrics-scraping Service for a kafka
// Integration's gateway Deployment. The kafka gateway is a pull-based consumer with no
// inbound application traffic, so this Service exists solely so a ServiceMonitor (see
// docs/guides/observability.md) has a stable endpoint to scrape /metrics from.
func desiredKafkaGatewayService(integration *automationv1alpha1.Integration) *corev1.Service {
	name := "kubezap-kafka-gateway-" + integration.Name
	selector := map[string]string{
		labelApp:       name,
		labelComponent: componentKafkaGateway,
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: integration.Namespace,
			Labels:    selector,
		},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports: []corev1.ServicePort{
				{
					Name:       portNameMetrics,
					Protocol:   corev1.ProtocolTCP,
					Port:       kafkaGatewayMetricsPort,
					TargetPort: intstr.FromInt32(kafkaGatewayMetricsPort),
				},
			},
		},
	}
}

// desiredKafkaGatewayDeployment returns the desired Deployment for a kafka Integration.
func desiredKafkaGatewayDeployment(integration *automationv1alpha1.Integration) *appsv1.Deployment {
	image := os.Getenv("KAFKA_GATEWAY_IMAGE")
	if image == "" {
		image = "ghcr.io/kubezap/kafka-gateway:latest"
	}

	deploymentName := "kubezap-kafka-gateway-" + integration.Name
	labels := map[string]string{
		labelApp:       deploymentName,
		labelComponent: componentKafkaGateway,
	}

	envVars := append([]corev1.EnvVar{
		{Name: envVarWatchNamespaces, Value: os.Getenv(envVarWatchNamespaces)},
		{Name: envVarKubezapNamespace, Value: integration.Namespace},
		{Name: envVarKubezapIntegrationName, Value: integration.Name},
		{Name: envVarLogLevel, Value: defaultLogLevel},
	}, otelPassthroughEnv()...)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: integration.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: sharedGatewayServiceAccountName,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
					},
					Containers: []corev1.Container{
						{
							Name:            componentKafkaGateway,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            []string{"--namespace=" + integration.Namespace},
							Env:             envVars,
							Ports: []corev1.ContainerPort{
								{Name: portNameMetrics, ContainerPort: kafkaGatewayMetricsPort, Protocol: corev1.ProtocolTCP},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             ptr.To(true),
								ReadOnlyRootFilesystem:   ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: healthzPath,
										Port: intstr.FromInt32(8090),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: healthzPath,
										Port: intstr.FromInt32(8090),
									},
								},
								InitialDelaySeconds: 3,
								PeriodSeconds:       5,
								FailureThreshold:    3,
							},
						},
					},
				},
			},
		},
	}
}

// kafkaTopicCGPair holds a single (topic, consumerGroup) pair for KEDA ScaledObject trigger construction.
type kafkaTopicCGPair struct {
	topic string
	cg    string
}

// reconcileKafkaScaledObject ensures a KEDA ScaledObject exists for the Kafka gateway Deployment.
// If KEDA is not installed, the function logs a warning and returns nil (graceful degradation).
func (r *IntegrationReconciler) reconcileKafkaScaledObject(ctx context.Context, integration *automationv1alpha1.Integration) error {
	log := logf.FromContext(ctx)

	// List all Triggers in this namespace and filter for kafka triggers that reference this Integration.
	triggerList := &automationv1alpha1.TriggerList{}
	if err := r.List(ctx, triggerList, client.InNamespace(integration.Namespace)); err != nil {
		return fmt.Errorf("listing triggers: %w", err)
	}

	// Collect unique (topic, consumerGroup) pairs — one per Trigger, not a Cartesian product.
	pairSet := make(map[kafkaTopicCGPair]struct{})

	for _, trigger := range triggerList.Items {
		if trigger.Spec.Type != integrationTypeKafka {
			continue
		}
		k := trigger.Spec.Kafka
		if k == nil {
			continue
		}
		if k.IntegrationRef.Name != integration.Name {
			continue
		}
		cg := k.ConsumerGroup
		if cg == "" {
			cg = "kubezap-" + trigger.Name
		}
		pairSet[kafkaTopicCGPair{topic: k.Topic, cg: cg}] = struct{}{}
	}

	pairs := make([]kafkaTopicCGPair, 0, len(pairSet))
	for p := range pairSet {
		pairs = append(pairs, p)
	}

	scaledObjName := "kubezap-kafka-gateway-" + integration.Name

	scaledObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "keda.sh/v1alpha1",
			"kind":       scaledObjectKind,
			"metadata": map[string]interface{}{
				"name":      scaledObjName,
				"namespace": integration.Namespace,
			},
			"spec": map[string]interface{}{
				"scaleTargetRef": map[string]interface{}{
					"name": scaledObjName,
				},
				"minReplicaCount": int64(0),
				"maxReplicaCount": int64(10),
				"triggers":        buildKafkaTriggers(integration, pairs),
			},
		},
	}
	scaledObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "keda.sh",
		Version: "v1alpha1",
		Kind:    scaledObjectKind,
	})

	if err := ctrl.SetControllerReference(integration, scaledObj, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on ScaledObject: %w", err)
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "keda.sh",
		Version: "v1alpha1",
		Kind:    scaledObjectKind,
	})

	err := r.Get(ctx, client.ObjectKey{Name: scaledObjName, Namespace: integration.Namespace}, existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			// Check if KEDA CRD is missing (not installed).
			if apimeta.IsNoMatchError(err) || strings.Contains(err.Error(), "no kind is registered") {
				log.Info("KEDA not installed, skipping ScaledObject", "integration", integration.Name)
				return nil
			}
			return fmt.Errorf("getting ScaledObject: %w", err)
		}
		// NotFound — create it.
		if createErr := r.Create(ctx, scaledObj); createErr != nil {
			if apimeta.IsNoMatchError(createErr) || strings.Contains(createErr.Error(), "no kind is registered") {
				log.Info("KEDA not installed, skipping ScaledObject", "integration", integration.Name)
				return nil
			}
			if !apierrors.IsAlreadyExists(createErr) {
				return fmt.Errorf("creating ScaledObject: %w", createErr)
			}
		}
		log.Info("created KEDA ScaledObject", "name", scaledObjName, "namespace", integration.Namespace)
		return nil
	}

	// Already exists — update the spec.
	scaledObj.SetResourceVersion(existing.GetResourceVersion())
	if updateErr := r.Update(ctx, scaledObj); updateErr != nil {
		return fmt.Errorf("updating ScaledObject: %w", updateErr)
	}
	log.Info("updated KEDA ScaledObject", "name", scaledObjName, "namespace", integration.Namespace)
	return nil
}

// buildKafkaTriggers constructs the KEDA trigger entries for a ScaledObject.
// Each pair is one (topic, consumerGroup) from a distinct Trigger — no Cartesian product.
func buildKafkaTriggers(integration *automationv1alpha1.Integration, pairs []kafkaTopicCGPair) []interface{} {
	brokers := strings.Join(integration.Spec.Kafka.BootstrapServers, ",")
	triggers := make([]interface{}, 0, len(pairs))
	for _, p := range pairs {
		triggers = append(triggers, map[string]interface{}{
			"type": "kafka",
			"metadata": map[string]interface{}{
				"bootstrapServers": brokers,
				"consumerGroup":    p.cg,
				"topic":            p.topic,
				"lagThreshold":     "10",
			},
		})
	}
	return triggers
}

// reconcileAmqpGateway ensures the AMQP gateway ServiceAccount, Role, RoleBinding, and
// Deployment exist and are up to date. It returns the Deployment name.
func (r *IntegrationReconciler) reconcileAmqpGateway(ctx context.Context, integration *automationv1alpha1.Integration) (string, error) {
	log := logf.FromContext(ctx)

	ns := integration.Namespace

	// Ensure ServiceAccount (shared kubezap-gateway SA with kafka/nats gateways).
	// No owner reference: this SA is shared across all broker-type integrations in the
	// namespace. Setting an owner ref to a single Integration would GC the SA when that
	// Integration is deleted, even if other broker integrations still exist.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns}}
	saResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting amqp gateway ServiceAccount: %w", err)
	}
	if saResult != controllerutil.OperationResultNone {
		log.Info("reconciled amqp gateway ServiceAccount", "namespace", ns, "result", saResult)
	}

	// Ensure Role (shared kubezap-gateway Role).
	amqpGatewayRules := []rbacv1.PolicyRule{
		{APIGroups: []string{apiGroupAutomation}, Resources: []string{resourceTriggers}, Verbs: []string{verbGet, verbList, verbWatch}},
		// list/watch (not just get) let the gateway notice an Integration's
		// secretRef being repointed to a different Secret without the
		// referencing Trigger itself being touched — see
		// docs/design/integration-secretref-change-detection.md.
		{APIGroups: []string{apiGroupAutomation}, Resources: []string{resourceIntegrations}, Verbs: []string{verbGet, verbList, verbWatch}},
		{APIGroups: []string{apiGroupAutomation}, Resources: []string{resourceFlowRuns}, Verbs: []string{verbCreate}},
		// Required to resolve Integration SASL/TLS secrets, and to watch them so a
		// rotated credential is picked up without waiting for the Trigger or
		// Integration to be reconciled again for an unrelated reason.
		{APIGroups: []string{""}, Resources: []string{resourceSecrets}, Verbs: []string{verbGet, verbList, verbWatch}},
	}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns}}
	roleResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = amqpGatewayRules
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting amqp gateway Role: %w", err)
	}
	if roleResult != controllerutil.OperationResultNone {
		log.Info("reconciled amqp gateway Role", "namespace", ns, "result", roleResult)
	}

	// Ensure RoleBinding (shared kubezap-gateway RoleBinding).
	amqpDesiredRoleRef := rbacv1.RoleRef{APIGroup: apiGroupRBAC, Kind: kindRole, Name: sharedGatewayServiceAccountName}
	amqpDesiredSubjects := []rbacv1.Subject{{Kind: kindServiceAccount, Name: sharedGatewayServiceAccountName, Namespace: ns}}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns}}
	rbResult, rbErr := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if rb.ResourceVersion != "" && rb.RoleRef != amqpDesiredRoleRef {
			return errRoleRefChanged
		}
		rb.RoleRef = amqpDesiredRoleRef
		rb.Subjects = amqpDesiredSubjects
		return nil
	})
	if rbErr != nil {
		if rbErr == errRoleRefChanged {
			if delErr := r.Delete(ctx, rb); delErr != nil && !apierrors.IsNotFound(delErr) {
				return "", fmt.Errorf("deleting stale amqp gateway RoleBinding: %w", delErr)
			}
			rb = &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns},
				RoleRef:    amqpDesiredRoleRef,
				Subjects:   amqpDesiredSubjects,
			}
			if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
				return "", fmt.Errorf("recreating amqp gateway RoleBinding: %w", err)
			}
			log.Info("recreated amqp gateway RoleBinding (RoleRef changed)", "namespace", ns)
		} else {
			return "", fmt.Errorf("upserting amqp gateway RoleBinding: %w", rbErr)
		}
	} else if rbResult != controllerutil.OperationResultNone {
		log.Info("reconciled amqp gateway RoleBinding", "namespace", ns, "result", rbResult)
	}

	desired := desiredAmqpGatewayDeployment(integration)

	if err := ctrl.SetControllerReference(integration, desired, r.Scheme); err != nil {
		return "", fmt.Errorf("setting owner reference on amqp gateway Deployment: %w", err)
	}

	deploymentName := desired.Name
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		// desired is populated with the live object by CreateOrUpdate before this func
		// is called. Overwrite the full spec so that env vars, security contexts,
		// resource limits, probes, args, and service account do not drift silently.
		desired.Spec = desiredAmqpGatewayDeployment(integration).Spec
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to create/update amqp gateway deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled amqp gateway deployment", "deployment", deploymentName, "namespace", integration.Namespace, "result", op)
	}
	return deploymentName, nil
}

// desiredAmqpGatewayDeployment returns the desired Deployment for an amqp Integration.
func desiredAmqpGatewayDeployment(integration *automationv1alpha1.Integration) *appsv1.Deployment {
	image := os.Getenv("AMQP_GATEWAY_IMAGE")
	if image == "" {
		image = "ghcr.io/kubezap/amqp-gateway:latest"
	}

	deploymentName := "kubezap-amqp-gateway-" + integration.Name
	labels := map[string]string{
		labelApp:       deploymentName,
		labelComponent: "amqp-gateway",
	}

	envVars := []corev1.EnvVar{
		{Name: envVarWatchNamespaces, Value: os.Getenv(envVarWatchNamespaces)},
		{Name: envVarKubezapNamespace, Value: integration.Namespace},
		{Name: envVarKubezapIntegrationName, Value: integration.Name},
		{Name: envVarLogLevel, Value: defaultLogLevel},
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: integration.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: sharedGatewayServiceAccountName,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
					},
					Containers: []corev1.Container{
						{
							Name:            "amqp-gateway",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            []string{"--namespace=" + integration.Namespace},
							Env:             envVars,
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             ptr.To(true),
								ReadOnlyRootFilesystem:   ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: healthzPath,
										Port: intstr.FromInt32(8090),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: healthzPath,
										Port: intstr.FromInt32(8090),
									},
								},
								InitialDelaySeconds: 3,
								PeriodSeconds:       5,
								FailureThreshold:    3,
							},
						},
					},
				},
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *IntegrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.Integration{}).
		Named("integration").
		Complete(r)
}

// reconcileNatsGateway ensures the NATS gateway ServiceAccount, Role, RoleBinding, and
// Deployment exist and are up to date. It returns the Deployment name.
func (r *IntegrationReconciler) reconcileNatsGateway(ctx context.Context, integration *automationv1alpha1.Integration) (string, error) {
	log := logf.FromContext(ctx)

	ns := integration.Namespace

	// Ensure ServiceAccount (shared kubezap-gateway SA with kafka/amqp gateways).
	// No owner reference: shared across all broker-type integrations in the namespace.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns}}
	saResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting nats gateway ServiceAccount: %w", err)
	}
	if saResult != controllerutil.OperationResultNone {
		log.Info("reconciled nats gateway ServiceAccount", "namespace", ns, "result", saResult)
	}

	// Ensure Role (shared kubezap-gateway Role).
	natsGatewayRules := []rbacv1.PolicyRule{
		{APIGroups: []string{apiGroupAutomation}, Resources: []string{resourceTriggers}, Verbs: []string{verbGet, verbList, verbWatch}},
		// list/watch (not just get) let the gateway notice an Integration's
		// secretRef being repointed to a different Secret without the
		// referencing Trigger itself being touched — see
		// docs/design/integration-secretref-change-detection.md.
		{APIGroups: []string{apiGroupAutomation}, Resources: []string{resourceIntegrations}, Verbs: []string{verbGet, verbList, verbWatch}},
		{APIGroups: []string{apiGroupAutomation}, Resources: []string{resourceFlowRuns}, Verbs: []string{verbCreate}},
		// Required to resolve Integration SASL/TLS secrets, and to watch them so a
		// rotated credential is picked up without waiting for the Trigger or
		// Integration to be reconciled again for an unrelated reason.
		{APIGroups: []string{""}, Resources: []string{resourceSecrets}, Verbs: []string{verbGet, verbList, verbWatch}},
	}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns}}
	roleResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = natsGatewayRules
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting nats gateway Role: %w", err)
	}
	if roleResult != controllerutil.OperationResultNone {
		log.Info("reconciled nats gateway Role", "namespace", ns, "result", roleResult)
	}

	// Ensure RoleBinding (shared kubezap-gateway RoleBinding).
	natsDesiredRoleRef := rbacv1.RoleRef{APIGroup: apiGroupRBAC, Kind: kindRole, Name: sharedGatewayServiceAccountName}
	natsDesiredSubjects := []rbacv1.Subject{{Kind: kindServiceAccount, Name: sharedGatewayServiceAccountName, Namespace: ns}}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns}}
	rbResult, rbErr := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if rb.ResourceVersion != "" && rb.RoleRef != natsDesiredRoleRef {
			return errRoleRefChanged
		}
		rb.RoleRef = natsDesiredRoleRef
		rb.Subjects = natsDesiredSubjects
		return nil
	})
	if rbErr != nil {
		if rbErr == errRoleRefChanged {
			if delErr := r.Delete(ctx, rb); delErr != nil && !apierrors.IsNotFound(delErr) {
				return "", fmt.Errorf("deleting stale nats gateway RoleBinding: %w", delErr)
			}
			rb = &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: sharedGatewayServiceAccountName, Namespace: ns},
				RoleRef:    natsDesiredRoleRef,
				Subjects:   natsDesiredSubjects,
			}
			if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
				return "", fmt.Errorf("recreating nats gateway RoleBinding: %w", err)
			}
			log.Info("recreated nats gateway RoleBinding (RoleRef changed)", "namespace", ns)
		} else {
			return "", fmt.Errorf("upserting nats gateway RoleBinding: %w", rbErr)
		}
	} else if rbResult != controllerutil.OperationResultNone {
		log.Info("reconciled nats gateway RoleBinding", "namespace", ns, "result", rbResult)
	}

	desired := desiredNatsGatewayDeployment(integration)

	if err := ctrl.SetControllerReference(integration, desired, r.Scheme); err != nil {
		return "", fmt.Errorf("setting owner reference on nats gateway Deployment: %w", err)
	}

	deploymentName := desired.Name
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		// desired is populated with the live object by CreateOrUpdate before this func
		// is called. Overwrite the full spec so that env vars, security contexts,
		// resource limits, probes, args, and service account do not drift silently.
		desired.Spec = desiredNatsGatewayDeployment(integration).Spec
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to create/update nats gateway deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled nats gateway deployment", "deployment", deploymentName, "namespace", integration.Namespace, "result", op)
	}
	return deploymentName, nil
}

// desiredNatsGatewayDeployment returns the desired Deployment for a nats Integration.
func desiredNatsGatewayDeployment(integration *automationv1alpha1.Integration) *appsv1.Deployment {
	image := os.Getenv("NATS_GATEWAY_IMAGE")
	if image == "" {
		image = "ghcr.io/kubezap/nats-gateway:latest"
	}

	deploymentName := "kubezap-nats-gateway-" + integration.Name
	labels := map[string]string{
		labelApp:       deploymentName,
		labelComponent: "nats-gateway",
	}

	envVars := []corev1.EnvVar{
		{Name: envVarWatchNamespaces, Value: os.Getenv(envVarWatchNamespaces)},
		{Name: envVarKubezapNamespace, Value: integration.Namespace},
		{Name: envVarKubezapIntegrationName, Value: integration.Name},
		{Name: envVarLogLevel, Value: defaultLogLevel},
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: integration.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: sharedGatewayServiceAccountName,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
					},
					Containers: []corev1.Container{
						{
							Name:            "nats-gateway",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            []string{"--namespace=" + integration.Namespace},
							Env:             envVars,
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             ptr.To(true),
								ReadOnlyRootFilesystem:   ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: healthzPath,
										Port: intstr.FromInt32(8090),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: healthzPath,
										Port: intstr.FromInt32(8090),
									},
								},
								InitialDelaySeconds: 3,
								PeriodSeconds:       5,
								FailureThreshold:    3,
							},
						},
					},
				},
			},
		},
	}
}
