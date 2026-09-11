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

	"github.com/google/cel-go/cel"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// newTestReconciler returns a FlowRunReconciler with no external dependencies,
// suitable for unit-testing pure-logic methods such as evaluateWhen.
// The CEL environment is initialized eagerly here, mirroring SetupWithManager.
func newTestReconciler() *FlowRunReconciler {
	env, err := cel.NewEnv(
		cel.Variable("trigger", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("steps", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		panic("newTestReconciler: failed to initialize CEL env: " + err.Error())
	}
	return &FlowRunReconciler{celEnv: env}
}

var _ = Describe("evaluateWhen", func() {
	var r *FlowRunReconciler

	BeforeEach(func() {
		r = newTestReconciler()
	})

	when := func(exprs ...string) []automationv1alpha1.WhenExpression {
		out := make([]automationv1alpha1.WhenExpression, len(exprs))
		for i, e := range exprs {
			out[i] = automationv1alpha1.WhenExpression{Expression: e}
		}
		return out
	}

	Context("constant expressions", func() {
		It("returns true for expression 'true'", func() {
			result, err := r.evaluateWhen(when("true"), nil, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("returns false for expression 'false'", func() {
			result, err := r.evaluateWhen(when("false"), nil, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("returns true for an empty when list (vacuously true)", func() {
			result, err := r.evaluateWhen(nil, nil, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})
	})

	Context("trigger data expressions", func() {
		It("evaluates trigger.body field comparison via has() and string equality", func() {
			td := &automationv1alpha1.TriggerData{
				// Note: trigger.body in CEL is the raw body string, not a parsed map.
				// The evaluateWhen implementation exposes trigger.body as a plain string,
				// so string operations such as contains() are appropriate.
				Body: "premium",
			}
			result, err := r.evaluateWhen(when(`trigger.body == "premium"`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("evaluates trigger.topic equality", func() {
			td := &automationv1alpha1.TriggerData{Topic: "orders"}
			result, err := r.evaluateWhen(when(`trigger.topic == "orders"`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("evaluates trigger.topic inequality to false", func() {
			td := &automationv1alpha1.TriggerData{Topic: "returns"}
			result, err := r.evaluateWhen(when(`trigger.topic == "orders"`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("evaluates trigger.headers map access", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{"X-Env": "prod"},
			}
			result, err := r.evaluateWhen(when(`trigger.headers["X-Env"] == "prod"`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("returns false for a trigger header that does not match", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{"X-Env": "staging"},
			}
			result, err := r.evaluateWhen(when(`trigger.headers["X-Env"] == "prod"`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("handles nil trigger data by defaulting fields to empty strings", func() {
			// With nil triggerData the activations map defaults body/topic/etc. to "".
			result, err := r.evaluateWhen(when(`trigger.topic == ""`), nil, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})
	})

	Context("trigger.bodyFields (nested body field access)", func() {
		It("navigates a nested JSON field via dot-path", func() {
			td := &automationv1alpha1.TriggerData{
				Body:        `{"order":{"customer":{"tier":"enterprise"}}}`,
				ContentType: "application/json",
			}
			result, err := r.evaluateWhen(when(`trigger.bodyFields.order.customer.tier == "enterprise"`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("indexes into a JSON array", func() {
			td := &automationv1alpha1.TriggerData{
				Body:        `{"items":[{"id":"a"},{"id":"b"}]}`,
				ContentType: "application/json",
			}
			result, err := r.evaluateWhen(when(`trigger.bodyFields.items[1].id == "b"`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("compares a JSON number as a native CEL double, not a string", func() {
			td := &automationv1alpha1.TriggerData{
				Body:        `{"total":4850.00}`,
				ContentType: "application/json",
			}
			result, err := r.evaluateWhen(when(`trigger.bodyFields.total >= 1000.0`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("accesses a top-level field from a form-urlencoded body", func() {
			td := &automationv1alpha1.TriggerData{
				Body:        "channel=alerts&user=alice",
				ContentType: "application/x-www-form-urlencoded",
			}
			result, err := r.evaluateWhen(when(`trigger.bodyFields.channel == "alerts"`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("defaults to an empty map (not an error) for a content type it cannot parse (XML)", func() {
			td := &automationv1alpha1.TriggerData{
				Body:        `<order><id>1</id></order>`,
				ContentType: "application/xml",
			}
			result, err := r.evaluateWhen(when(`has(trigger.bodyFields.id)`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("defaults to an empty map when triggerData is nil", func() {
			result, err := r.evaluateWhen(when(`has(trigger.bodyFields.anything)`), nil, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("leaves trigger.body as the raw string, unaffected by bodyFields", func() {
			td := &automationv1alpha1.TriggerData{
				Body:        `{"eventType":"order.placed"}`,
				ContentType: "application/json",
			}
			result, err := r.evaluateWhen(when(`trigger.body.contains("order.placed") && trigger.bodyFields.eventType == "order.placed"`), nil, nil, td, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})
	})

	Context("step status expressions", func() {
		It("evaluates step status equality to Succeeded", func() {
			statuses := []automationv1alpha1.StepRunStatus{
				{Name: "step1", Phase: "Succeeded"},
			}
			result, err := r.evaluateWhen(when(`steps.step1.status == "Succeeded"`), nil, statuses, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("returns false when step phase does not match", func() {
			statuses := []automationv1alpha1.StepRunStatus{
				{Name: "step1", Phase: "Failed"},
			}
			result, err := r.evaluateWhen(when(`steps.step1.status == "Succeeded"`), nil, statuses, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("evaluates step results map access", func() {
			stepResults := map[string]map[string]string{
				"fetch": {"status_code": "200"},
			}
			result, err := r.evaluateWhen(when(`steps.fetch.results["status_code"] == "200"`), stepResults, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("normalises hyphens to underscores in step names for CEL access", func() {
			stepResults := map[string]map[string]string{
				"my-step": {"key": "val"},
			}
			// The step name "my-step" is accessible in CEL as "my_step".
			result, err := r.evaluateWhen(when(`steps.my_step.results["key"] == "val"`), stepResults, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})
	})

	Context("param expressions", func() {
		It("evaluates a resolved param value", func() {
			params := map[string]string{"env": "production"}
			result, err := r.evaluateWhen(when(`params.env == "production"`), nil, nil, nil, params)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("evaluates false for a non-matching resolved param value", func() {
			params := map[string]string{"env": "staging"}
			result, err := r.evaluateWhen(when(`params.env == "production"`), nil, nil, nil, params)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("treats an unset params map as an empty map, not an error", func() {
			result, err := r.evaluateWhen(when(`has(params.env)`), nil, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})
	})

	Context("multiple when expressions (AND semantics)", func() {
		It("returns true only when all expressions are true", func() {
			td := &automationv1alpha1.TriggerData{Topic: "orders"}
			statuses := []automationv1alpha1.StepRunStatus{
				{Name: "validate", Phase: "Succeeded"},
			}
			result, err := r.evaluateWhen(
				when(`trigger.topic == "orders"`, `steps.validate.status == "Succeeded"`),
				nil, statuses, td, nil,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("returns false when the first expression is false regardless of the second", func() {
			result, err := r.evaluateWhen(when("false", "true"), nil, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})
	})

	Context("error handling", func() {
		It("returns an error for an invalid CEL expression (syntax error)", func() {
			_, err := r.evaluateWhen(when("this is !!! not valid CEL"), nil, nil, nil, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("CEL"))
		})

		It("returns an error when the CEL expression returns a non-bool value", func() {
			// String literal is a valid CEL expression but returns a string, not bool.
			_, err := r.evaluateWhen(when(`"not a bool"`), nil, nil, nil, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("bool"))
		})

		It("does not panic on an invalid expression — returns a wrapped error", func() {
			Expect(func() {
				_, _ = r.evaluateWhen(when("!!!"), nil, nil, nil, nil)
			}).NotTo(Panic())
		})
	})

	Context("CEL program caching", func() {
		It("produces the same result when the same expression is evaluated twice (exercises cache hit path)", func() {
			expr := `trigger.topic == "payments"`
			td := &automationv1alpha1.TriggerData{Topic: "payments"}

			result1, err1 := r.evaluateWhen(when(expr), nil, nil, td, nil)
			result2, err2 := r.evaluateWhen(when(expr), nil, nil, td, nil)

			Expect(err1).NotTo(HaveOccurred())
			Expect(err2).NotTo(HaveOccurred())
			Expect(result1).To(BeTrue())
			Expect(result2).To(BeTrue())
		})
	})

	Context("CEL cost limits", func() {
		It("returns an error when CELCostLimit is set very low and a complex expression exceeds it", func() {
			// Set a cost limit of 1 — any non-trivial expression should exceed this.
			r.CELCostLimit = 1
			// A nested list comprehension is combinatorially expensive: the outer
			// filter iterates the list and the inner exists() re-iterates for each
			// element, giving O(n²) cost. Even with a tiny list, this blows the
			// budget of 1 almost immediately.
			complexExpr := `[1, 2, 3, 4, 5].filter(x, [1, 2, 3, 4, 5].exists(y, y == x)).size() > 0`
			_, err := r.evaluateWhen(when(complexExpr), nil, nil, nil, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cost limit"))
		})

		It("does not error for a simple expression when CELCostLimit is set to 10000 (default)", func() {
			r.CELCostLimit = 10000
			result, err := r.evaluateWhen(when("true"), nil, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("does not apply a cost limit when CELCostLimit is 0 (unlimited)", func() {
			r.CELCostLimit = 0
			// This expression would fail under a budget of 1 but must succeed with no limit.
			result, err := r.evaluateWhen(when(`trigger.topic == ""`), nil, nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})
	})
})
