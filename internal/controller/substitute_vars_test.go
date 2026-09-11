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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

var _ = Describe("substituteVars", func() {
	Context("step result substitution", func() {
		It("substitutes a step result placeholder", func() {
			stepResults := map[string]map[string]string{
				"step1": {"url": "https://example.com/webhook"},
			}
			result := substituteVars("POST to $(steps.step1.results.url)", stepResults, nil, nil)
			Expect(result).To(Equal("POST to https://example.com/webhook"))
		})

		It("substitutes multiple step result placeholders in one string", func() {
			stepResults := map[string]map[string]string{
				"step1": {"id": "order-123", "status": "confirmed"},
			}
			result := substituteVars("id=$(steps.step1.results.id) status=$(steps.step1.results.status)", stepResults, nil, nil)
			Expect(result).To(Equal("id=order-123 status=confirmed"))
		})

		It("normalises hyphens to underscores in step names", func() {
			stepResults := map[string]map[string]string{
				// The key stored by the engine uses the original name; substituteVars
				// normalises hyphens to underscores both in the map key and in the placeholder.
				"my-step": {"output": "42"},
			}
			// Placeholder uses underscore form (as the CEL env also requires)
			result := substituteVars("value=$(steps.my_step.results.output)", stepResults, nil, nil)
			Expect(result).To(Equal("value=42"))
		})

		It("leaves an unresolvable step result placeholder verbatim", func() {
			result := substituteVars("$(steps.missing.results.key)", nil, nil, nil)
			Expect(result).To(Equal("$(steps.missing.results.key)"))
		})

		It("returns the original string unchanged when stepResults is nil and triggerData is nil", func() {
			result := substituteVars("no placeholders here", nil, nil, nil)
			Expect(result).To(Equal("no placeholders here"))
		})
	})

	Context("trigger body substitution", func() {
		It("substitutes a top-level JSON body field", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"orderId":"ord-999","amount":200}`,
			}
			result := substituteVars("order=$(trigger.body.orderId)", nil, td, nil)
			Expect(result).To(Equal("order=ord-999"))
		})

		It("substitutes the raw body via $(trigger.body)", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"raw":"payload"}`,
			}
			result := substituteVars("body=$(trigger.body)", nil, td, nil)
			Expect(result).To(Equal(`body={"raw":"payload"}`))
		})

		It("resolves $(trigger.body.<field>) BEFORE $(trigger.body) so raw replacement is not triggered first", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"orderId":"ord-777"}`,
			}
			// Both placeholders in the same string; field substitution must not corrupt raw body.
			result := substituteVars("id=$(trigger.body.orderId) raw=$(trigger.body)", nil, td, nil)
			Expect(result).To(Equal(`id=ord-777 raw={"orderId":"ord-777"}`))
		})

		It("resolves a nested body field via dot-path traversal", func() {
			// Dot-path access is now supported: $(trigger.body.order.id) traverses
			// body["order"]["id"] recursively.
			td := &automationv1alpha1.TriggerData{
				Body: `{"order":{"id":"nested-id"}}`,
			}
			result := substituteVars("nested=$(trigger.body.order.id)", nil, td, nil)
			Expect(result).To(Equal("nested=nested-id"))
		})

		It("leaves body field placeholder verbatim when body is empty", func() {
			td := &automationv1alpha1.TriggerData{Body: ""}
			result := substituteVars("$(trigger.body.orderId)", nil, td, nil)
			// Body is empty so no JSON parsing occurs; placeholder is left untouched
			// and the raw body substitution maps "$(trigger.body)" → "" — but the
			// field-specific placeholder "$(trigger.body.orderId)" does NOT match
			// the raw "$(trigger.body)" replacement, so it stays verbatim.
			Expect(result).To(Equal("$(trigger.body.orderId)"))
		})

		It("leaves body field placeholder verbatim when body is not valid JSON", func() {
			td := &automationv1alpha1.TriggerData{Body: "not-json"}
			result := substituteVars("$(trigger.body.field)", nil, td, nil)
			Expect(result).To(Equal("$(trigger.body.field)"))
		})

		It("substitutes numeric body fields as strings", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"amount":42}`,
			}
			result := substituteVars("amount=$(trigger.body.amount)", nil, td, nil)
			Expect(result).To(Equal("amount=42"))
		})

		It("renders a large integer as plain decimal, not scientific notation", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"bigId": 1234567, "ts": 1757600000000}`,
			}
			Expect(substituteVars("$(trigger.body.bigId)", nil, td, nil)).To(Equal("1234567"))
			Expect(substituteVars("$(trigger.body.ts)", nil, td, nil)).To(Equal("1757600000000"))
		})

		It("renders a nested object/array leaf as valid JSON, not Go syntax", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"obj":{"id":1},"arr":[1,2]}`,
			}
			Expect(substituteVars("$(trigger.body.obj)", nil, td, nil)).To(Equal(`{"id":1}`))
			Expect(substituteVars("$(trigger.body.arr)", nil, td, nil)).To(Equal(`[1,2]`))
		})

		It("does not hang and resolves exactly once when a body field's value echoes its own placeholder", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"a": "$(trigger.body.a)"}`,
			}
			done := make(chan string, 1)
			go func() { done <- substituteVars("x=$(trigger.body.a)", nil, td, nil) }()
			Eventually(done, "2s").Should(Receive(Equal("x=$(trigger.body.a)")))
		})
	})

	Context("form-urlencoded trigger body substitution", func() {
		It("substitutes a top-level form-urlencoded field", func() {
			td := &automationv1alpha1.TriggerData{
				ContentType: "application/x-www-form-urlencoded",
				Body:        "command=%2Fkubezap&text=deploy+staging&user_name=alice",
			}
			result := substituteVars("cmd=$(trigger.body.command) text=$(trigger.body.text)", nil, td, nil)
			Expect(result).To(Equal("cmd=/kubezap text=deploy staging"))
		})

		It("ignores charset and other parameters on the content type", func() {
			td := &automationv1alpha1.TriggerData{
				ContentType: "application/x-www-form-urlencoded; charset=UTF-8",
				Body:        "text=status+production",
			}
			result := substituteVars("$(trigger.body.text)", nil, td, nil)
			Expect(result).To(Equal("status production"))
		})

		It("substitutes multiple form fields used for Slack-style routing", func() {
			td := &automationv1alpha1.TriggerData{
				ContentType: "application/x-www-form-urlencoded",
				Body:        "channel_id=C123ABC&response_url=https%3A%2F%2Fhooks.slack.com%2Fx&user_name=bob",
			}
			result := substituteVars(
				"channel=$(trigger.body.channel_id) url=$(trigger.body.response_url) user=$(trigger.body.user_name)",
				nil, td, nil)
			Expect(result).To(Equal("channel=C123ABC url=https://hooks.slack.com/x user=bob"))
		})

		It("resolves a missing form field to an empty string", func() {
			td := &automationv1alpha1.TriggerData{
				ContentType: "application/x-www-form-urlencoded",
				Body:        "text=deploy",
			}
			result := substituteVars("$(trigger.body.missing)", nil, td, nil)
			Expect(result).To(Equal(""))
		})

		It("still substitutes the raw body verbatim via $(trigger.body) for form-encoded payloads", func() {
			td := &automationv1alpha1.TriggerData{
				ContentType: "application/x-www-form-urlencoded",
				Body:        "text=deploy+staging",
			}
			result := substituteVars("raw=$(trigger.body)", nil, td, nil)
			Expect(result).To(Equal("raw=text=deploy+staging"))
		})
	})

	Context("param substitution", func() {
		It("substitutes a resolved param value", func() {
			params := map[string]string{"orderId": "ord-123"}
			result := substituteVars("id=$(params.orderId)", nil, nil, params)
			Expect(result).To(Equal("id=ord-123"))
		})

		It("substitutes multiple param placeholders in one string", func() {
			params := map[string]string{"env": "production", "orderId": "ord-1"}
			result := substituteVars("env=$(params.env) order=$(params.orderId)", nil, nil, params)
			Expect(result).To(Equal("env=production order=ord-1"))
		})

		It("leaves an unresolvable param placeholder verbatim", func() {
			result := substituteVars("$(params.missing)", nil, nil, nil)
			Expect(result).To(Equal("$(params.missing)"))
		})

		It("applies param, step result, and trigger body substitutions together", func() {
			stepResults := map[string]map[string]string{"fetch": {"id": "item-7"}}
			td := &automationv1alpha1.TriggerData{Body: `{"tenant":"acme"}`}
			params := map[string]string{"env": "production"}

			result := substituteVars(
				"tenant=$(trigger.body.tenant) item=$(steps.fetch.results.id) env=$(params.env)",
				stepResults, td, params)
			Expect(result).To(Equal("tenant=acme item=item-7 env=production"))
		})
	})

	Context("trigger header substitution", func() {
		It("substitutes a header value case-insensitively", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{"X-Request-Id": "req-abc"},
			}
			// Lookup is case-insensitive on both sides.
			result := substituteVars("id=$(trigger.headers.x-request-id)", nil, td, nil)
			Expect(result).To(Equal("id=req-abc"))
		})

		It("substitutes using the original header casing in the placeholder", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{"Authorization": "Bearer tok123"},
			}
			result := substituteVars("auth=$(trigger.headers.Authorization)", nil, td, nil)
			Expect(result).To(Equal("auth=Bearer tok123"))
		})

		It("replaces a missing header placeholder with an empty string", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{},
			}
			result := substituteVars("$(trigger.headers.X-Missing)", nil, td, nil)
			Expect(result).To(Equal(""))
		})

		It("does not hang and resolves exactly once when a header's value echoes its own placeholder", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{"X-Echo": "$(trigger.headers.X-Echo)"},
			}
			done := make(chan string, 1)
			go func() { done <- substituteVars("x=$(trigger.headers.X-Echo)", nil, td, nil) }()
			Eventually(done, "2s").Should(Receive(Equal("x=$(trigger.headers.X-Echo)")))
		})
	})

	Context("secret placeholder isolation (interpolateTemplate)", func() {
		It("does not resolve a $(secrets.*) placeholder injected via attacker-controlled trigger body text", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"text":"$(secrets.evil.password)"}`,
			}
			tmpl := "token=$(secrets.auth.token) msg=$(trigger.body.text)"
			calledWith := []string{}
			resolveSecret := func(name, key string) (string, error) {
				calledWith = append(calledWith, name)
				return "REALTOKEN", nil
			}
			actual, display, err := interpolateTemplate(tmpl, nil, td, nil, resolveSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(Equal("token=REALTOKEN msg=$(secrets.evil.password)"))
			Expect(display).To(Equal("token=[REDACTED] msg=$(secrets.evil.password)"))
			Expect(calledWith).To(Equal([]string{"auth"}), "only the legitimate secret reference should trigger a lookup")
		})

		It("leaves $(secrets.*) as literal text when no secret resolver is supplied (substituteVars)", func() {
			result := substituteVars("$(secrets.foo.bar)", nil, nil, nil)
			Expect(result).To(Equal("$(secrets.foo.bar)"))
		})

		It("leaves a malformed $(secrets.name) placeholder (no key) verbatim and continues resolving the rest", func() {
			resolveSecret := func(name, key string) (string, error) { return "VAL", nil }
			actual, _, err := interpolateTemplate("a=$(secrets.name) b=$(secrets.real.key)", nil, nil, nil, resolveSecret)
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(Equal("a=$(secrets.name) b=VAL"))
		})

		It("aborts the entire substitution on a secret fetch error, with no partial result", func() {
			resolveSecret := func(name, key string) (string, error) {
				return "", fmt.Errorf("secret %q not found", name)
			}
			actual, display, err := interpolateTemplate("a=$(secrets.missing.key) b=literal", nil, nil, nil, resolveSecret)
			Expect(err).To(HaveOccurred())
			Expect(actual).To(Equal(""))
			Expect(display).To(Equal(""))
		})
	})

	Context("other trigger fields", func() {
		It("substitutes $(trigger.topic)", func() {
			td := &automationv1alpha1.TriggerData{Topic: "orders"}
			result := substituteVars("topic=$(trigger.topic)", nil, td, nil)
			Expect(result).To(Equal("topic=orders"))
		})

		It("substitutes $(trigger.partition) and $(trigger.offset)", func() {
			td := &automationv1alpha1.TriggerData{Partition: 3, Offset: 42}
			result := substituteVars("$(trigger.partition)/$(trigger.offset)", nil, td, nil)
			Expect(result).To(Equal("3/42"))
		})

		It("substitutes $(trigger.scheduledTime) in RFC3339 format when set", func() {
			// Use a fixed Unix epoch instant for deterministic output.
			epoch := metav1.Unix(0, 0)
			td := &automationv1alpha1.TriggerData{ScheduledTime: &epoch}
			result := substituteVars("$(trigger.scheduledTime)", nil, td, nil)
			Expect(result).To(Equal("1970-01-01T00:00:00Z"))
		})

		It("replaces $(trigger.scheduledTime) with empty string when unset", func() {
			td := &automationv1alpha1.TriggerData{}
			result := substituteVars("$(trigger.scheduledTime)", nil, td, nil)
			Expect(result).To(Equal(""))
		})
	})

	Context("combined substitutions", func() {
		It("applies step results and trigger substitutions in the same string", func() {
			stepResults := map[string]map[string]string{
				"fetch": {"id": "item-7"},
			}
			td := &automationv1alpha1.TriggerData{
				Body: `{"tenant":"acme"}`,
			}
			template := "tenant=$(trigger.body.tenant) item=$(steps.fetch.results.id)"
			result := substituteVars(template, stepResults, td, nil)
			Expect(result).To(Equal("tenant=acme item=item-7"))
		})
	})

	Context("nil / empty trigger data", func() {
		It("returns the original string unchanged when triggerData is nil", func() {
			result := substituteVars("$(trigger.body)", nil, nil, nil)
			Expect(result).To(Equal("$(trigger.body)"))
		})
	})
})
