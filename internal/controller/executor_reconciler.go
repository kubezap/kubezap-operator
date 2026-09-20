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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	executorhttp "github.com/kubezap/kubezap-operator/internal/executor/http"
)

const (
	executorDeploymentName    = "kubezap-http-executor"
	executorNetworkPolicyName = "kubezap-executor-ingress"
	executorMTLSSecretName    = "kubezap-executor-mtls-cert"
	defaultExecutorImage      = "ghcr.io/kubezap/http-executor:latest"
	DefaultExecutorPort       = int32(8091)

	// executorHealthPort is always plain HTTP, even when MTLSEnabled — see
	// cmd/http-executor/main.go's --health-port flag doc: kubelet's httpGet probes
	// can never present a client cert, so /healthz on the main mTLS-protected port
	// would permanently fail readiness/liveness checks.
	executorHealthPort = int32(8092)
	portNameHealth     = "health"

	// labelAppKubernetesIOName / labelAppKubernetesIOComponent are the
	// app.kubernetes.io/* convention label keys used for both the executor's
	// full label set (executorLabels) and its selector-only label subsets.
	labelAppKubernetesIOName      = "app.kubernetes.io/name"
	labelAppKubernetesIOComponent = "app.kubernetes.io/component"

	// appNameKubezap / componentHTTPExecutor are the values paired with the
	// two label keys above (and, for componentHTTPExecutor, also used as the
	// executor container's name).
	appNameKubezap        = "kubezap"
	componentHTTPExecutor = "http-executor"
)

// executorLabels returns the standard label set applied to all executor-managed resources.
func executorLabels() map[string]string {
	return map[string]string{
		labelAppKubernetesIOName:       appNameKubezap,
		labelAppKubernetesIOComponent:  componentHTTPExecutor,
		"app.kubernetes.io/managed-by": "kubezap-operator",
	}
}

// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete

// ExecutorReconciler reconciles http-executor Deployments, Services, and
// NetworkPolicies in managed namespaces.
type ExecutorReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ExecutorImage string
	ExecutorPort  int32

	// MTLSEnabled controls whether the executor channel is secured with mTLS.
	// When true, reconcileExecutorCertSecret injects the cert Secret into each
	// managed namespace and reconcileExecutorDeployment mounts it into the pod.
	MTLSEnabled bool

	// MTLSBundle is set by cmd/main.go when --executor-mtls=true.
	// The bundle is generated once at startup and rotated every 23h.
	// cmd/main.go is responsible for:
	//   1. Generating the initial MTLSBundle via controller.GenerateMTLSBundle(dnsSANs)
	//   2. Setting ExecutorReconciler.MTLSBundle and FlowRunReconciler.ExecutorTLSConfig
	//   3. Starting a goroutine that calls NeedsRotation() and regenerates when needed
	MTLSBundle *MTLSBundle

	// SSRFAllowClusterInternal passes --ssrf-allow-in-cluster=true to the executor
	// Deployment args. For dev/test environments where HTTP steps must call in-cluster
	// services. Mirrors FlowRunReconciler.SSRFAllowClusterInternal.
	SSRFAllowClusterInternal bool

	// OperatorNamespace is the namespace the controller-manager pod itself runs in
	// (POD_NAMESPACE). The executor NetworkPolicy's ingress rule must reference this
	// namespace explicitly: the executor is reconciled into the *watched* namespace
	// (req.Namespace, e.g. "default"), which is frequently different from the
	// operator's own namespace (e.g. "kubezap-system") — a NetworkPolicyPeer's bare
	// PodSelector only matches pods in the same namespace as the NetworkPolicy itself,
	// so cross-namespace ingress requires a NamespaceSelector too.
	OperatorNamespace string
}

func (r *ExecutorReconciler) executorImage() string {
	if r.ExecutorImage != "" {
		return r.ExecutorImage
	}
	return defaultExecutorImage
}

func (r *ExecutorReconciler) executorPort() int32 {
	if r.ExecutorPort != 0 {
		return r.ExecutorPort
	}
	return DefaultExecutorPort
}

// egressExceptCIDRs returns the IPv4 ranges excluded from the executor's egress
// allowlist, i.e. the ranges Flow steps may NOT reach. Empty when
// SSRFAllowClusterInternal is set, since that dev/test flag already disables the
// equivalent software-layer check — see the Egress rule comment above.
func (r *ExecutorReconciler) egressExceptCIDRs() []string {
	if r.SSRFAllowClusterInternal {
		return nil
	}
	return executorhttp.SSRFBlockedCIDRsV4
}

// egressExceptCIDRsV6 is the IPv6 equivalent of egressExceptCIDRs.
func (r *ExecutorReconciler) egressExceptCIDRsV6() []string {
	if r.SSRFAllowClusterInternal {
		return nil
	}
	return executorhttp.SSRFBlockedCIDRsV6
}

// operatorNamespace returns the namespace the controller-manager pod itself runs in.
// Falls back to the project's conventional default (config/default's namespace) if
// unset, so an ExecutorReconciler constructed without it (e.g. in tests) still
// produces a well-formed, restrictive NamespaceSelector rather than an empty one.
func (r *ExecutorReconciler) operatorNamespace() string {
	if r.OperatorNamespace != "" {
		return r.OperatorNamespace
	}
	return "kubezap-system"
}

// Reconcile ensures a kubezap-http-executor Deployment, Service, and NetworkPolicy
// exist in the namespace of the incoming request. It is triggered by both FlowRun
// and Trigger events so the executor is pre-provisioned as soon as a Trigger is
// created — before the first FlowRun runs.
func (r *ExecutorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	namespace := req.Namespace
	log.Info("Reconciling http-executor resources", "namespace", namespace)

	if err := r.reconcileExecutorDeployment(ctx, namespace); err != nil {
		log.Error(err, "Failed to reconcile executor Deployment", "namespace", namespace)
		return ctrl.Result{}, err
	}

	if err := r.reconcileExecutorService(ctx, namespace); err != nil {
		log.Error(err, "Failed to reconcile executor Service", "namespace", namespace)
		return ctrl.Result{}, err
	}

	if err := r.reconcileExecutorNetworkPolicy(ctx, namespace); err != nil {
		log.Error(err, "Failed to reconcile executor NetworkPolicy", "namespace", namespace)
		return ctrl.Result{}, err
	}

	if r.MTLSEnabled {
		if err := r.reconcileExecutorCertSecret(ctx, namespace); err != nil {
			log.Error(err, "Failed to reconcile executor mTLS cert Secret", "namespace", namespace)
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// reconcileExecutorDeployment ensures the http-executor Deployment exists with the correct spec.
func (r *ExecutorReconciler) reconcileExecutorDeployment(ctx context.Context, namespace string) error {
	labels := executorLabels()
	port := r.executorPort()
	replicas := int32(1)

	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      executorDeploymentName,
			Namespace: namespace,
			Labels:    labels,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.Labels = labels

		// Build container args and optional volume/mounts for mTLS.
		containerArgs := []string{
			fmt.Sprintf("--port=%d", port),
			fmt.Sprintf("--health-port=%d", executorHealthPort),
		}
		if r.SSRFAllowClusterInternal {
			containerArgs = append(containerArgs, "--ssrf-allow-in-cluster=true")
		}
		var volumeMounts []corev1.VolumeMount
		var volumes []corev1.Volume
		if r.MTLSEnabled {
			containerArgs = append(containerArgs,
				"--mtls=true",
				"--tls-cert-file=/etc/kubezap/tls/tls.crt",
				"--tls-key-file=/etc/kubezap/tls/tls.key",
				"--tls-ca-file=/etc/kubezap/tls/ca.crt",
			)
			volumeMounts = []corev1.VolumeMount{
				{
					Name:      "mtls-certs",
					MountPath: "/etc/kubezap/tls",
					ReadOnly:  true,
				},
			}
			volumes = []corev1.Volume{
				{
					Name: "mtls-certs",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: executorMTLSSecretName,
						},
					},
				},
			}
		}

		desired.Spec = appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelAppKubernetesIOName:      appNameKubezap,
					labelAppKubernetesIOComponent: componentHTTPExecutor,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken:  ptr.To(false),
					TerminationGracePeriodSeconds: ptr.To(int64(30)),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Volumes: volumes,
					Containers: []corev1.Container{
						{
							Name:            componentHTTPExecutor,
							Image:           r.executorImage(),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            containerArgs,
							Env:             otelPassthroughEnv(),
							VolumeMounts:    volumeMounts,
							Ports: []corev1.ContainerPort{
								{
									Name:          portNameHTTP,
									ContainerPort: port,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          portNameHealth,
									ContainerPort: executorHealthPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								ReadOnlyRootFilesystem:   ptr.To(true),
								RunAsNonRoot:             ptr.To(true),
								RunAsUser:                ptr.To(int64(65534)),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   healthzPath,
										Port:   intstr.FromInt32(executorHealthPort),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       15,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   healthzPath,
										Port:   intstr.FromInt32(executorHealthPort),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 2,
								PeriodSeconds:       10,
							},
						},
					},
				},
			},
		}
		return nil
	})
	return err
}

// reconcileExecutorCertSecret creates or updates the mTLS cert Secret in the given namespace.
// The Secret contains three keys: tls.crt (server cert PEM), tls.key (server key PEM),
// and ca.crt (CA cert PEM). The executor pod mounts this Secret at /etc/kubezap/tls.
//
// This method is a no-op when MTLSBundle is nil (e.g. during initial startup before the
// first bundle is generated). cmd/main.go sets MTLSBundle before starting the manager.
func (r *ExecutorReconciler) reconcileExecutorCertSecret(ctx context.Context, namespace string) error {
	if r.MTLSBundle == nil {
		return fmt.Errorf("mTLS enabled but MTLSBundle is nil — bundle must be set before reconciliation")
	}

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      executorMTLSSecretName,
			Namespace: namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.Type = corev1.SecretTypeOpaque
		desired.Data = map[string][]byte{
			"tls.crt": r.MTLSBundle.ServerCertPEM(),
			"tls.key": r.MTLSBundle.ServerKeyPEM(),
			"ca.crt":  r.MTLSBundle.CACertPEM(),
		}
		return nil
	})
	return err
}

// reconcileExecutorService ensures the http-executor ClusterIP Service exists.
func (r *ExecutorReconciler) reconcileExecutorService(ctx context.Context, namespace string) error {
	labels := executorLabels()
	port := r.executorPort()

	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      executorDeploymentName,
			Namespace: namespace,
			Labels:    labels,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.Labels = labels
		desired.Spec = corev1.ServiceSpec{
			Selector: map[string]string{
				labelAppKubernetesIOName:      appNameKubezap,
				labelAppKubernetesIOComponent: componentHTTPExecutor,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       portNameHTTP,
					Protocol:   corev1.ProtocolTCP,
					Port:       port,
					TargetPort: intstr.FromInt32(port),
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		}
		return nil
	})
	return err
}

// egressExceptCIDRs/egressExceptCIDRsV6 (above) source this NetworkPolicy
// egress rule's "except" ranges from executorhttp.SSRFBlockedCIDRsV4/V6 —
// the same authoritative CIDR list internal/executor/http/ssrf.go uses for
// the software SSRF blocklist (see that var's doc comment for the full
// rationale). This NetworkPolicy egress rule is defense-in-depth against the
// software SSRF blocklist's inherent DNS-rebinding gap (resolve, validate,
// then let the HTTP transport re-resolve and connect — an attacker who
// controls the target hostname's DNS can return a safe address for the first
// lookup and a blocked one for the second). NetworkPolicy filters the actual
// destination IP of the packet the executor sends, so DNS trickery cannot
// defeat it the way it defeats the app-level check. See
// docs/design/executor-egress-networkpolicy.md. Requires a
// NetworkPolicy-enforcing CNI (Calico, Cilium, most managed-Kubernetes
// defaults); on a non-enforcing CNI (e.g. plain Flannel) this provides no
// additional protection — see docs/guides/security-checklist.md.

// reconcileExecutorNetworkPolicy ensures only the controller pod can reach the executor,
// and that the executor cannot egress to internal/link-local ranges (SSRF defense-in-depth).
func (r *ExecutorReconciler) reconcileExecutorNetworkPolicy(ctx context.Context, namespace string) error {
	port := r.executorPort()
	protocol := corev1.ProtocolTCP
	portVal := intstr.FromInt32(port)
	dnsPort := intstr.FromInt32(53)
	dnsUDP := corev1.ProtocolUDP
	dnsTCP := corev1.ProtocolTCP

	desired := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      executorNetworkPolicyName,
			Namespace: namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.Spec = networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelAppKubernetesIOName:      appNameKubezap,
					labelAppKubernetesIOComponent: componentHTTPExecutor,
				},
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							// Matches on control-plane=controller-manager alone (not also
							// app.kubernetes.io/name=kubezap): the raw kustomize manifests
							// (config/manager/manager.yaml) and the Helm chart
							// (charts/kubezap-operator) label the controller pod with
							// different app.kubernetes.io/name values (Helm's chart-name
							// convention gives it "kubezap-operator", not "kubezap"), but
							// both agree on control-plane=controller-manager, which is
							// unique enough on its own within the operator's namespace. A
							// namespace selector is required alongside it: the
							// controller-manager pod normally runs in a different namespace
							// (its own, e.g. "kubezap-system") than this NetworkPolicy (the
							// watched namespace, e.g. "default").
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"control-plane": "controller-manager",
								},
							},
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": r.operatorNamespace(),
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &protocol,
							Port:     &portVal,
						},
					},
				},
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					// Outbound calls for Flow steps, minus the blocked ranges. No port
					// restriction: Flow HTTP steps and integrations legitimately target
					// arbitrary ports (internal APIs and dev/test mocks are frequently
					// not on 80/443), and the design intent here (see
					// docs/design/executor-egress-networkpolicy.md) is a
					// destination-CIDR blocklist, not a port allowlist.
					// When SSRFAllowClusterInternal is set (dev/test only — see
					// config/dev/manager_dev_patch.yaml), the software SSRF check already
					// permits in-cluster hostnames; mirror that here by dropping the
					// RFC1918/link-local exceptions so this network-layer rule doesn't
					// silently block the same in-cluster calls (e.g. Flow steps hitting an
					// in-cluster Mockoon service) the dev flag was meant to allow.
					To: []networkingv1.NetworkPolicyPeer{
						{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: r.egressExceptCIDRs()}},
						{IPBlock: &networkingv1.IPBlock{CIDR: "::/0", Except: r.egressExceptCIDRsV6()}},
					},
				},
				{
					// DNS resolution — always allowed regardless of destination.
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &dnsUDP, Port: &dnsPort},
						{Protocol: &dnsTCP, Port: &dnsPort},
					},
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
		}
		return nil
	})
	return err
}

// SetupWithManager registers the ExecutorReconciler with the controller manager.
// It watches both FlowRun and Trigger objects so the executor is pre-provisioned
// as soon as a Trigger is created in a namespace — before the first FlowRun runs.
func (r *ExecutorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("executor").
		For(&automationv1alpha1.FlowRun{}).
		Watches(
			&automationv1alpha1.Trigger{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
				return []reconcile.Request{{
					NamespacedName: types.NamespacedName{
						Name:      "executor-preprovisioning",
						Namespace: obj.GetNamespace(),
					},
				}}
			}),
		).
		Complete(r)
}
