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
	"github.com/prometheus/client_golang/prometheus"
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
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		TriggerFirings,
		FlowRunDuration,
		StepDuration,
		WebhookIPBlocked,
		WebhookRateLimited,
		WebhookRequestDuration,
	)
}
