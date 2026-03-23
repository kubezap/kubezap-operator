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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	toolscache "k8s.io/client-go/tools/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

var _ = Describe("ResourceWatcher", func() {

	// -------------------------------------------------------------------------
	// Register / Deregister lifecycle
	// -------------------------------------------------------------------------

	Describe("Register and Deregister", func() {
		var dynClient dynamic.Interface

		BeforeEach(func() {
			// Create a real dynamic client from the envtest config so that
			// Register can start an informer without panicking.
			var err error
			dynClient, err = dynamic.NewForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
		})

		It("tracks watchers by key and removes them on Deregister", func() {
			logger := logf.FromContext(ctx)
			rw := NewResourceWatcher(k8sClient, dynClient, nil, logger)

			// Simulate the manager calling Start so mgrCtx is populated.
			startCtx, startCancel := context.WithCancel(ctx)
			defer startCancel()
			go func() { _ = rw.Start(startCtx) }() //nolint:errcheck
			// Yield so Start can store mgrCtx before Register is called.
			Eventually(func() bool {
				rw.mu.Lock()
				defer rw.mu.Unlock()
				return rw.mgrCtx != nil
			}).Should(BeTrue())

			trigger := &automationv1alpha1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trigger",
					Namespace: "default",
				},
				Spec: automationv1alpha1.TriggerSpec{
					Type: "resource",
					Resource: &automationv1alpha1.ResourceTrigger{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					FlowRef: &automationv1alpha1.FlowReference{Name: "test-flow"},
				},
			}

			rw.Register(trigger)

			rw.mu.Lock()
			_, exists := rw.watchers["default/test-trigger"]
			rw.mu.Unlock()
			Expect(exists).To(BeTrue(), "watcher should be registered")

			rw.Deregister("default/test-trigger")

			rw.mu.Lock()
			_, exists = rw.watchers["default/test-trigger"]
			rw.mu.Unlock()
			Expect(exists).To(BeFalse(), "watcher should be removed after Deregister")
		})

		It("is a no-op when deregistering a non-existent key", func() {
			logger := logf.FromContext(ctx)
			rw := NewResourceWatcher(k8sClient, dynClient, nil, logger)

			Expect(func() {
				rw.Deregister("nonexistent/trigger")
			}).NotTo(Panic())
		})

		It("replaces an existing watcher on re-Register", func() {
			logger := logf.FromContext(ctx)
			rw := NewResourceWatcher(k8sClient, dynClient, nil, logger)

			// Simulate the manager calling Start so mgrCtx is populated.
			startCtx, startCancel := context.WithCancel(ctx)
			defer startCancel()
			go func() { _ = rw.Start(startCtx) }() //nolint:errcheck
			Eventually(func() bool {
				rw.mu.Lock()
				defer rw.mu.Unlock()
				return rw.mgrCtx != nil
			}).Should(BeTrue())

			trigger := &automationv1alpha1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "replace-trigger",
					Namespace: "default",
				},
				Spec: automationv1alpha1.TriggerSpec{
					Type: "resource",
					Resource: &automationv1alpha1.ResourceTrigger{
						APIVersion: "v1",
						Kind:       "ConfigMap",
					},
					FlowRef: &automationv1alpha1.FlowReference{Name: "test-flow"},
				},
			}

			rw.Register(trigger)
			rw.Register(trigger) // re-register should not panic

			rw.mu.Lock()
			_, exists := rw.watchers["default/replace-trigger"]
			rw.mu.Unlock()
			Expect(exists).To(BeTrue())

			rw.Deregister("default/replace-trigger")
		})

		It("skips registration when resource spec is nil", func() {
			logger := logf.FromContext(ctx)
			rw := NewResourceWatcher(k8sClient, dynClient, nil, logger)

			trigger := &automationv1alpha1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nil-spec-trigger",
					Namespace: "default",
				},
				Spec: automationv1alpha1.TriggerSpec{
					Type:     "resource",
					Resource: nil,
					FlowRef:  &automationv1alpha1.FlowReference{Name: "test-flow"},
				},
			}

			rw.Register(trigger)

			rw.mu.Lock()
			_, exists := rw.watchers["default/nil-spec-trigger"]
			rw.mu.Unlock()
			Expect(exists).To(BeFalse(), "should not register when resource spec is nil")
		})

		It("skips registration when dynamicClient is nil", func() {
			logger := logf.FromContext(ctx)
			rw := NewResourceWatcher(k8sClient, nil, nil, logger)

			trigger := &automationv1alpha1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nil-dyn-trigger",
					Namespace: "default",
				},
				Spec: automationv1alpha1.TriggerSpec{
					Type: "resource",
					Resource: &automationv1alpha1.ResourceTrigger{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					FlowRef: &automationv1alpha1.FlowReference{Name: "test-flow"},
				},
			}

			rw.Register(trigger)

			rw.mu.Lock()
			_, exists := rw.watchers["default/nil-dyn-trigger"]
			rw.mu.Unlock()
			Expect(exists).To(BeFalse(), "should not register when dynamicClient is nil")
		})
	})

	// -------------------------------------------------------------------------
	// parseAPIVersion
	// -------------------------------------------------------------------------

	Describe("parseAPIVersion", func() {
		type testCase struct {
			input         string
			expectedGroup string
			expectedVer   string
		}

		DescribeTable("parses apiVersion strings correctly",
			func(tc testCase) {
				group, version := parseAPIVersion(tc.input)
				Expect(group).To(Equal(tc.expectedGroup))
				Expect(version).To(Equal(tc.expectedVer))
			},
			Entry("core group", testCase{input: "v1", expectedGroup: "", expectedVer: "v1"}),
			Entry("apps group", testCase{input: "apps/v1", expectedGroup: "apps", expectedVer: "v1"}),
			Entry("custom group", testCase{input: "automation.kubezap.io/v1alpha1", expectedGroup: "automation.kubezap.io", expectedVer: "v1alpha1"}),
			Entry("batch group", testCase{input: "batch/v1", expectedGroup: "batch", expectedVer: "v1"}),
		)
	})

	// -------------------------------------------------------------------------
	// allowedEventSet
	// -------------------------------------------------------------------------

	Describe("allowedEventSet", func() {
		It("defaults to create when events is empty", func() {
			set := allowedEventSet(nil)
			Expect(set).To(HaveLen(1))
			Expect(set["create"]).To(BeTrue())
		})

		It("defaults to create when events is an empty slice", func() {
			set := allowedEventSet([]string{})
			Expect(set).To(HaveLen(1))
			Expect(set["create"]).To(BeTrue())
		})

		It("normalises event names to lowercase", func() {
			set := allowedEventSet([]string{"Create", "UPDATE", "Delete"})
			Expect(set).To(HaveLen(3))
			Expect(set["create"]).To(BeTrue())
			Expect(set["update"]).To(BeTrue())
			Expect(set["delete"]).To(BeTrue())
		})

		It("returns the exact set when already lowercase", func() {
			set := allowedEventSet([]string{"update", "delete"})
			Expect(set).To(HaveLen(2))
			Expect(set["create"]).To(BeFalse())
			Expect(set["update"]).To(BeTrue())
			Expect(set["delete"]).To(BeTrue())
		})
	})

	// -------------------------------------------------------------------------
	// sanitizeName
	// -------------------------------------------------------------------------

	Describe("sanitizeName", func() {
		type testCase struct {
			input    string
			expected string
		}

		DescribeTable("sanitises resource names for FlowRun naming",
			func(tc testCase) {
				Expect(sanitizeName(tc.input)).To(Equal(tc.expected))
			},
			Entry("simple name", testCase{input: "my-pod", expected: "my-pod"}),
			Entry("uppercase", testCase{input: "My-Pod", expected: "my-pod"}),
			Entry("dots replaced", testCase{input: "my.pod.v1", expected: "my-pod-v1"}),
			Entry("underscores replaced", testCase{input: "my_pod_name", expected: "my-pod-name"}),
			Entry("truncated to 32 chars", testCase{
				input:    "this-is-a-very-long-resource-name-that-exceeds-the-limit",
				expected: "this-is-a-very-long-resource-nam",
			}),
			Entry("leading/trailing dashes trimmed", testCase{input: ".special.", expected: "special"}),
			Entry("empty string", testCase{input: "", expected: ""}),
		)
	})

	// -------------------------------------------------------------------------
	// fieldsChanged
	// -------------------------------------------------------------------------

	Describe("fieldsChanged", func() {
		makeObj := func(phase string) *unstructured.Unstructured {
			return &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"phase": phase,
					},
					"spec": map[string]interface{}{
						"replicas": int64(3),
					},
				},
			}
		}

		It("returns true when a watched field changes", func() {
			oldObj := makeObj("Running")
			newObj := makeObj("Failed")
			Expect(fieldsChanged(oldObj, newObj, []string{".status.phase"})).To(BeTrue())
		})

		It("returns false when watched fields are unchanged", func() {
			oldObj := makeObj("Running")
			newObj := makeObj("Running")
			Expect(fieldsChanged(oldObj, newObj, []string{".status.phase"})).To(BeFalse())
		})

		It("returns true when one of multiple fields changes", func() {
			oldObj := makeObj("Running")
			newObj := makeObj("Running")
			// Change replicas
			newObj.Object["spec"] = map[string]interface{}{"replicas": int64(5)}
			Expect(fieldsChanged(oldObj, newObj, []string{".status.phase", ".spec.replicas"})).To(BeTrue())
		})

		It("returns true when old object is not unstructured", func() {
			newObj := makeObj("Running")
			Expect(fieldsChanged("not-unstructured", newObj, []string{".status.phase"})).To(BeTrue())
		})

		It("returns true for missing fields (nil vs value)", func() {
			oldObj := &unstructured.Unstructured{Object: map[string]interface{}{}}
			newObj := makeObj("Running")
			Expect(fieldsChanged(oldObj, newObj, []string{".status.phase"})).To(BeTrue())
		})
	})

	// -------------------------------------------------------------------------
	// toUnstructured
	// -------------------------------------------------------------------------

	Describe("toUnstructured", func() {
		It("returns the object when it is already unstructured", func() {
			obj := &unstructured.Unstructured{Object: map[string]interface{}{"kind": "Pod"}}
			u, ok := toUnstructured(obj)
			Expect(ok).To(BeTrue())
			Expect(u.GetKind()).To(Equal("Pod"))
		})

		It("unwraps a DeletedFinalStateUnknown tombstone", func() {
			inner := &unstructured.Unstructured{Object: map[string]interface{}{"kind": "ConfigMap"}}
			tombstone := toolscache.DeletedFinalStateUnknown{
				Key: "default/my-cm",
				Obj: inner,
			}
			u, ok := toUnstructured(tombstone)
			Expect(ok).To(BeTrue())
			Expect(u.GetKind()).To(Equal("ConfigMap"))
		})

		It("returns false for an unrecognised type", func() {
			_, ok := toUnstructured("a string")
			Expect(ok).To(BeFalse())
		})
	})
})
