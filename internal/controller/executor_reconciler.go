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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

const (
	executorDeploymentName    = "kubezap-http-executor"
	executorNetworkPolicyName = "kubezap-executor-ingress"
	defaultExecutorImage      = "ghcr.io/kubezap/http-executor:latest"
	defaultExecutorPort       = int32(8091)
)

// executorLabels returns the standard label set applied to all executor-managed resources.
func executorLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "kubezap",
		"app.kubernetes.io/component":  "http-executor",
		"app.kubernetes.io/managed-by": "kubezap-operator",
	}
}

// ExecutorReconciler reconciles http-executor Deployments, Services, and
// NetworkPolicies in managed namespaces.
//
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
type ExecutorReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ExecutorImage string
	ExecutorPort  int32
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
	return defaultExecutorPort
}

// Reconcile ensures a kubezap-http-executor Deployment, Service, and NetworkPolicy
// exist in the namespace of the incoming FlowRun request.
func (r *ExecutorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Verify the FlowRun still exists — it may have been deleted between enqueue and reconcile.
	flowRun := &automationv1alpha1.FlowRun{}
	if err := r.Get(ctx, req.NamespacedName, flowRun); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

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
		desired.Spec = appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":      "kubezap",
					"app.kubernetes.io/component": "http-executor",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{
						{
							Name:            "http-executor",
							Image:           r.executorImage(),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            []string{"--port=8091"},
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: port,
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
										Path:   "/healthz",
										Port:   intstr.FromInt32(port),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       15,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/healthz",
										Port:   intstr.FromInt32(port),
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
				"app.kubernetes.io/name":      "kubezap",
				"app.kubernetes.io/component": "http-executor",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
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

// reconcileExecutorNetworkPolicy ensures only the controller pod can reach the executor.
func (r *ExecutorReconciler) reconcileExecutorNetworkPolicy(ctx context.Context, namespace string) error {
	port := r.executorPort()
	protocol := corev1.ProtocolTCP
	portVal := intstr.FromInt32(port)

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
					"app.kubernetes.io/name":      "kubezap",
					"app.kubernetes.io/component": "http-executor",
				},
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app.kubernetes.io/name":      "kubezap",
									"app.kubernetes.io/component": "controller",
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
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
		}
		return nil
	})
	return err
}

// SetupWithManager registers the ExecutorReconciler with the controller manager.
// It watches FlowRun objects so the executor is guaranteed to be present in any
// namespace before FlowRun execution begins.
func (r *ExecutorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.FlowRun{}).
		Complete(r)
}
