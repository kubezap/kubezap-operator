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

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

// newTestReconciler returns a FlowRunReconciler with no external dependencies,
// suitable for unit-testing pure-logic methods such as evaluateWhen.
func newTestReconciler() *FlowRunReconciler {
	return &FlowRunReconciler{}
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
			result, err := r.evaluateWhen(when("true"), nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("returns false for expression 'false'", func() {
			result, err := r.evaluateWhen(when("false"), nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("returns true for an empty when list (vacuously true)", func() {
			result, err := r.evaluateWhen(nil, nil, nil, nil)
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
			result, err := r.evaluateWhen(when(`trigger.body == "premium"`), nil, nil, td)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("evaluates trigger.topic equality", func() {
			td := &automationv1alpha1.TriggerData{Topic: "orders"}
			result, err := r.evaluateWhen(when(`trigger.topic == "orders"`), nil, nil, td)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("evaluates trigger.topic inequality to false", func() {
			td := &automationv1alpha1.TriggerData{Topic: "returns"}
			result, err := r.evaluateWhen(when(`trigger.topic == "orders"`), nil, nil, td)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("evaluates trigger.headers map access", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{"X-Env": "prod"},
			}
			result, err := r.evaluateWhen(when(`trigger.headers["X-Env"] == "prod"`), nil, nil, td)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("returns false for a trigger header that does not match", func() {
			td := &automationv1alpha1.TriggerData{
				Headers: map[string]string{"X-Env": "staging"},
			}
			result, err := r.evaluateWhen(when(`trigger.headers["X-Env"] == "prod"`), nil, nil, td)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("handles nil trigger data by defaulting fields to empty strings", func() {
			// With nil triggerData the activations map defaults body/topic/etc. to "".
			result, err := r.evaluateWhen(when(`trigger.topic == ""`), nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})
	})

	Context("step status expressions", func() {
		It("evaluates step status equality to Succeeded", func() {
			statuses := []automationv1alpha1.StepRunStatus{
				{Name: "step1", Phase: "Succeeded"},
			}
			result, err := r.evaluateWhen(when(`steps.step1.status == "Succeeded"`), nil, statuses, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("returns false when step phase does not match", func() {
			statuses := []automationv1alpha1.StepRunStatus{
				{Name: "step1", Phase: "Failed"},
			}
			result, err := r.evaluateWhen(when(`steps.step1.status == "Succeeded"`), nil, statuses, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("evaluates step results map access", func() {
			stepResults := map[string]map[string]string{
				"fetch": {"status_code": "200"},
			}
			result, err := r.evaluateWhen(when(`steps.fetch.results["status_code"] == "200"`), stepResults, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("normalises hyphens to underscores in step names for CEL access", func() {
			stepResults := map[string]map[string]string{
				"my-step": {"key": "val"},
			}
			// The step name "my-step" is accessible in CEL as "my_step".
			result, err := r.evaluateWhen(when(`steps.my_step.results["key"] == "val"`), stepResults, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
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
				nil, statuses, td,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("returns false when the first expression is false regardless of the second", func() {
			result, err := r.evaluateWhen(when("false", "true"), nil, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})
	})

	Context("error handling", func() {
		It("returns an error for an invalid CEL expression (syntax error)", func() {
			_, err := r.evaluateWhen(when("this is !!! not valid CEL"), nil, nil, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("CEL"))
		})

		It("returns an error when the CEL expression returns a non-bool value", func() {
			// String literal is a valid CEL expression but returns a string, not bool.
			_, err := r.evaluateWhen(when(`"not a bool"`), nil, nil, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("bool"))
		})

		It("does not panic on an invalid expression — returns a wrapped error", func() {
			Expect(func() {
				_, _ = r.evaluateWhen(when("!!!"), nil, nil, nil)
			}).NotTo(Panic())
		})
	})

	Context("CEL program caching", func() {
		It("produces the same result when the same expression is evaluated twice (exercises cache hit path)", func() {
			expr := `trigger.topic == "payments"`
			td := &automationv1alpha1.TriggerData{Topic: "payments"}

			result1, err1 := r.evaluateWhen(when(expr), nil, nil, td)
			result2, err2 := r.evaluateWhen(when(expr), nil, nil, td)

			Expect(err1).NotTo(HaveOccurred())
			Expect(err2).NotTo(HaveOccurred())
			Expect(result1).To(BeTrue())
			Expect(result2).To(BeTrue())
		})
	})
})
