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

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// See docs/design/flow-parameters.md for the resolution order these tests
// verify: explicit FlowRunSpec.Params > auto-derived from a same-named top-level
// trigger.body field > declared default > required-and-missing fails > empty string.
var _ = Describe("resolveFlowParams", func() {
	It("returns nil with no error when the Flow declares no params", func() {
		resolved, err := resolveFlowParams(nil, nil, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(BeNil())
	})

	It("auto-derives a param from a same-named top-level JSON body field", func() {
		decls := []automationv1alpha1.ParamDeclaration{{Name: "orderId"}}
		td := &automationv1alpha1.TriggerData{Body: `{"orderId":"ord-123","other":"x"}`}

		resolved, err := resolveFlowParams(decls, nil, nil, td)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(map[string]string{"orderId": "ord-123"}))
	})

	It("auto-derives a param from a same-named top-level form-urlencoded body field", func() {
		decls := []automationv1alpha1.ParamDeclaration{{Name: "text"}}
		td := &automationv1alpha1.TriggerData{
			ContentType: "application/x-www-form-urlencoded",
			Body:        "text=deploy+staging&other=x",
		}

		resolved, err := resolveFlowParams(decls, nil, nil, td)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(map[string]string{"text": "deploy staging"}))
	})

	It("prefers an explicit FlowRunSpec.Params entry over the auto-derived body field", func() {
		decls := []automationv1alpha1.ParamDeclaration{{Name: "orderId"}}
		values := []automationv1alpha1.ParamValue{{Name: "orderId", Value: "explicit-override"}}
		td := &automationv1alpha1.TriggerData{Body: `{"orderId":"from-body"}`}

		resolved, err := resolveFlowParams(decls, values, nil, td)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(map[string]string{"orderId": "explicit-override"}))
	})

	It("resolves $(...) interpolation inside an explicit FlowRunSpec.Params value", func() {
		decls := []automationv1alpha1.ParamDeclaration{{Name: "orderId"}}
		values := []automationv1alpha1.ParamValue{{Name: "orderId", Value: "$(trigger.body.id)"}}
		td := &automationv1alpha1.TriggerData{Body: `{"id":"ord-from-interpolation"}`}

		resolved, err := resolveFlowParams(decls, values, nil, td)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(map[string]string{"orderId": "ord-from-interpolation"}))
	})

	It("uses the declared default when nothing is supplied or derivable", func() {
		decls := []automationv1alpha1.ParamDeclaration{{Name: "env", Default: "staging"}}

		resolved, err := resolveFlowParams(decls, nil, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(map[string]string{"env": "staging"}))
	})

	It("prefers auto-derivation from the body over the declared default", func() {
		decls := []automationv1alpha1.ParamDeclaration{{Name: "env", Default: "staging"}}
		td := &automationv1alpha1.TriggerData{Body: `{"env":"production"}`}

		resolved, err := resolveFlowParams(decls, nil, nil, td)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(map[string]string{"env": "production"}))
	})

	It("fails with a clear message when a required param has no value and no default", func() {
		decls := []automationv1alpha1.ParamDeclaration{{Name: "orderId", Required: true}}

		resolved, err := resolveFlowParams(decls, nil, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("orderId"))
		Expect(resolved).To(BeNil())
	})

	It("resolves an optional, unresolvable param to an empty string rather than failing", func() {
		decls := []automationv1alpha1.ParamDeclaration{{Name: "optional"}}

		resolved, err := resolveFlowParams(decls, nil, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(map[string]string{"optional": ""}))
	})

	It("resolves multiple declared params independently", func() {
		decls := []automationv1alpha1.ParamDeclaration{
			{Name: "orderId"},
			{Name: "env", Default: "staging"},
			{Name: "explicit"},
		}
		values := []automationv1alpha1.ParamValue{{Name: "explicit", Value: "set-directly"}}
		td := &automationv1alpha1.TriggerData{Body: `{"orderId":"ord-1","env":"production"}`}

		resolved, err := resolveFlowParams(decls, values, nil, td)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(map[string]string{
			"orderId":  "ord-1",
			"env":      "production",
			"explicit": "set-directly",
		}))
	})
})
