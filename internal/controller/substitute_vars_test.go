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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

var _ = Describe("substituteVars", func() {
	Context("step result substitution", func() {
		It("substitutes a step result placeholder", func() {
			stepResults := map[string]map[string]string{
				"step1": {"url": "https://example.com/webhook"},
			}
			result := substituteVars("POST to $(steps.step1.results.url)", stepResults, nil)
			Expect(result).To(Equal("POST to https://example.com/webhook"))
		})

		It("substitutes multiple step result placeholders in one string", func() {
			stepResults := map[string]map[string]string{
				"step1": {"id": "order-123", "status": "confirmed"},
			}
			result := substituteVars("id=$(steps.step1.results.id) status=$(steps.step1.results.status)", stepResults, nil)
			Expect(result).To(Equal("id=order-123 status=confirmed"))
		})

		It("normalises hyphens to underscores in step names", func() {
			stepResults := map[string]map[string]string{
				// The key stored by the engine uses the original name; substituteVars
				// normalises hyphens to underscores both in the map key and in the placeholder.
				"my-step": {"output": "42"},
			}
			// Placeholder uses underscore form (as the CEL env also requires)
			result := substituteVars("value=$(steps.my_step.results.output)", stepResults, nil)
			Expect(result).To(Equal("value=42"))
		})

		It("leaves an unresolvable step result placeholder verbatim", func() {
			result := substituteVars("$(steps.missing.results.key)", nil, nil)
			Expect(result).To(Equal("$(steps.missing.results.key)"))
		})

		It("returns the original string unchanged when stepResults is nil and triggerData is nil", func() {
			result := substituteVars("no placeholders here", nil, nil)
			Expect(result).To(Equal("no placeholders here"))
		})
	})

	Context("trigger body substitution", func() {
		It("substitutes a top-level JSON body field", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"orderId":"ord-999","amount":200}`,
			}
			result := substituteVars("order=$(trigger.body.orderId)", nil, td)
			Expect(result).To(Equal("order=ord-999"))
		})

		It("substitutes the raw body via $(trigger.body)", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"raw":"payload"}`,
			}
			result := substituteVars("body=$(trigger.body)", nil, td)
			Expect(result).To(Equal(`body={"raw":"payload"}`))
		})

		It("resolves $(trigger.body.<field>) BEFORE $(trigger.body) so raw replacement is not triggered first", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"orderId":"ord-777"}`,
			}
			// Both placeholders in the same string; field substitution must not corrupt raw body.
			result := substituteVars("id=$(trigger.body.orderId) raw=$(trigger.body)", nil, td)
			Expect(result).To(Equal(`id=ord-777 raw={"orderId":"ord-777"}`))
		})

		It("resolves a nested body field via dot-path traversal", func() {
			// Dot-path access is now supported: $(trigger.body.order.id) traverses
			// body["order"]["id"] recursively.
			td := &automationv1alpha1.TriggerData{
				Body: `{"order":{"id":"nested-id"}}`,
			}
			result := substituteVars("nested=$(trigger.body.order.id)", nil, td)
			Expect(result).To(Equal("nested=nested-id"))
		})

		It("leaves body field placeholder verbatim when body is empty", func() {
			td := &automationv1alpha1.TriggerData{Body: ""}
			result := substituteVars("$(trigger.body.orderId)", nil, td)
			// Body is empty so no JSON parsing occurs; placeholder is left untouched
			// and the raw body substitution maps "$(trigger.body)" → "" — but the
			// field-specific placeholder "$(trigger.body.orderId)" does NOT match
			// the raw "$(trigger.body)" replacement, so it stays verbatim.
			Expect(result).To(Equal("$(trigger.body.orderId)"))
		})

		It("leaves body field placeholder verbatim when body is not valid JSON", func() {
			td := &automationv1alpha1.TriggerData{Body: "not-json"}
			result := substituteVars("$(trigger.body.field)", nil, td)
			Expect(result).To(Equal("$(trigger.body.field)"))
		})

		It("substitutes numeric body fields as strings", func() {
			td := &automationv1alpha1.TriggerData{
				Body: `{"amount":42}`,
			}
			result := substituteVars("amount=$(trigger.body.amount)", nil, td)
			// json.Unmarshal decodes numbers as float64; fmt.Sprintf("%v", 42.0) → "42"
			Expect(result).To(Equal("amount=42"))
		})
	})

	Context("trigger header substitution", func() {
		It("substitutes a header value case-insensitively", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{"X-Request-Id": "req-abc"},
			}
			// Lookup is case-insensitive on both sides.
			result := substituteVars("id=$(trigger.headers.x-request-id)", nil, td)
			Expect(result).To(Equal("id=req-abc"))
		})

		It("substitutes using the original header casing in the placeholder", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{"Authorization": "Bearer tok123"},
			}
			result := substituteVars("auth=$(trigger.headers.Authorization)", nil, td)
			Expect(result).To(Equal("auth=Bearer tok123"))
		})

		It("replaces a missing header placeholder with an empty string", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{},
			}
			result := substituteVars("$(trigger.headers.X-Missing)", nil, td)
			Expect(result).To(Equal(""))
		})
	})

	Context("other trigger fields", func() {
		It("substitutes $(trigger.topic)", func() {
			td := &automationv1alpha1.TriggerData{Topic: "orders"}
			result := substituteVars("topic=$(trigger.topic)", nil, td)
			Expect(result).To(Equal("topic=orders"))
		})

		It("substitutes $(trigger.partition) and $(trigger.offset)", func() {
			td := &automationv1alpha1.TriggerData{Partition: 3, Offset: 42}
			result := substituteVars("$(trigger.partition)/$(trigger.offset)", nil, td)
			Expect(result).To(Equal("3/42"))
		})

		It("substitutes $(trigger.scheduledTime) in RFC3339 format when set", func() {
			// Use a fixed Unix epoch instant for deterministic output.
			epoch := metav1.Unix(0, 0)
			td := &automationv1alpha1.TriggerData{ScheduledTime: &epoch}
			result := substituteVars("$(trigger.scheduledTime)", nil, td)
			Expect(result).To(Equal("1970-01-01T00:00:00Z"))
		})

		It("replaces $(trigger.scheduledTime) with empty string when unset", func() {
			td := &automationv1alpha1.TriggerData{}
			result := substituteVars("$(trigger.scheduledTime)", nil, td)
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
			result := substituteVars(template, stepResults, td)
			Expect(result).To(Equal("tenant=acme item=item-7"))
		})
	})

	Context("nil / empty trigger data", func() {
		It("returns the original string unchanged when triggerData is nil", func() {
			result := substituteVars("$(trigger.body)", nil, nil)
			Expect(result).To(Equal("$(trigger.body)"))
		})
	})
})
