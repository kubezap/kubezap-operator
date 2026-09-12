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

package metrics

import (
	"context"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	TriggerFirings = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubezap_trigger_firings_total",
		Help: "Total number of trigger firings.",
	}, []string{"namespace", "trigger", "type", "result"})
	// result values: "success", "rate_limited", "error"

	FlowRunDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubezap_flowrun_duration_seconds",
		Help:    "Duration of FlowRun execution from start to terminal phase.",
		Buckets: prometheus.DefBuckets,
	}, []string{"namespace", "flow", "phase"})
	// phase values: "Succeeded", "Failed"

	StepDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubezap_step_duration_seconds",
		Help:    "Duration of individual step execution.",
		Buckets: prometheus.DefBuckets,
	}, []string{"namespace", "flow", "step_type", "outcome"})
	// outcome values: "Succeeded", "Failed"

	// WebhookIPBlocked counts requests rejected by the IP allowlist auth handler.
	// source_range is the /24 (IPv4) or /48 (IPv6) CIDR bucket of the source IP —
	// never the full IP — to bound Prometheus label cardinality.
	WebhookIPBlocked = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubezap_webhook_ip_blocked_total",
		Help: "Total requests blocked by the webhook IP allowlist, labelled by /24 (IPv4) or /48 (IPv6) source CIDR bucket.",
	}, []string{"trigger", "source_range"})

	// WebhookRateLimited counts requests suppressed by the cooldown window policy.
	WebhookRateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubezap_webhook_rate_limited_total",
		Help: "Total requests suppressed by webhook cooldown window policy.",
	}, []string{"trigger", "namespace"})

	// WebhookRequestDuration tracks the end-to-end latency of webhook HTTP requests.
	// result values: "accepted", "rejected", "rate_limited"
	WebhookRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubezap_webhook_request_duration_seconds",
		Help:    "Duration of webhook HTTP requests in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"trigger", "result"})

	// FlowRunQueueDuration measures time from FlowRun creation to first transition to Running.
	// Indicates scheduling latency / controller backpressure.
	FlowRunQueueDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubezap_flowrun_queue_duration_seconds",
		Help:    "Time from FlowRun creation to first transition to Running phase.",
		Buckets: prometheus.DefBuckets,
	}, []string{"namespace", "flow"})

	// WhenExpressionErrors counts CEL 'when' expression evaluation failures per flow.
	// reason values: "compile_error", "eval_error", "type_error"
	WhenExpressionErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubezap_when_expression_errors_total",
		Help: "Total CEL 'when' expression evaluation failures, by flow and error reason.",
	}, []string{"flow", "reason"})

	// SecretAccesses counts every Kubernetes Secret read performed during FlowRun execution.
	// Used for PCI-DSS/SOC2 compliance audit trails. Labels:
	//   namespace   — namespace the secret lives in
	//   secret_name — name of the Kubernetes Secret object (never the key or value)
	SecretAccesses = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubezap_secret_accesses_total",
		Help: "Total number of Kubernetes Secret reads performed during FlowRun step execution, by namespace and secret name.",
	}, []string{"namespace", "secret_name"})
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		TriggerFirings,
		FlowRunDuration,
		StepDuration,
		WebhookIPBlocked,
		WebhookRateLimited,
		WebhookRequestDuration,
		FlowRunQueueDuration,
		WhenExpressionErrors,
		SecretAccesses,
	)
}

// flowRunsActiveDesc is the Prometheus descriptor for the kubezap_flowruns_active gauge.
var flowRunsActiveDesc = prometheus.NewDesc(
	"kubezap_flowruns_active",
	"Number of FlowRuns currently in Running or Pending phase, by namespace and phase.",
	[]string{"namespace", "phase"},
	nil,
)

// FlowRunActiveCollector is a custom prometheus.Collector that lists FlowRuns from
// the controller-runtime cache at scrape time to report active (Running/Pending) counts.
// This avoids stale gauge values across controller restarts.
type FlowRunActiveCollector struct {
	Client client.Client
}

// RegisterFlowRunActiveCollector registers the custom FlowRunActiveCollector with the
// controller-runtime metrics registry. Call this once from SetupWithManager.
func RegisterFlowRunActiveCollector(c client.Client) {
	ctrlmetrics.Registry.MustRegister(&FlowRunActiveCollector{Client: c})
}

func (col *FlowRunActiveCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- flowRunsActiveDesc
}

func (col *FlowRunActiveCollector) Collect(ch chan<- prometheus.Metric) {
	var list automationv1alpha1.FlowRunList
	if err := col.Client.List(context.Background(), &list); err != nil {
		// Emit no metrics rather than a stale value on list failure.
		return
	}

	// counts[namespace][phase] → count
	counts := make(map[string]map[string]int)
	for i := range list.Items {
		phase := list.Items[i].Status.Phase
		if phase != automationv1alpha1.FlowRunPhaseRunning && phase != automationv1alpha1.FlowRunPhasePending && phase != "" {
			continue
		}
		if phase == "" {
			phase = automationv1alpha1.FlowRunPhasePending
		}
		ns := list.Items[i].Namespace
		if counts[ns] == nil {
			counts[ns] = make(map[string]int)
		}
		counts[ns][string(phase)]++
	}

	for ns, phases := range counts {
		for phase, n := range phases {
			ch <- prometheus.MustNewConstMetric(
				flowRunsActiveDesc,
				prometheus.GaugeValue,
				float64(n),
				ns, phase,
			)
		}
	}
}
