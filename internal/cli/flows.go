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

package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/cli/output"
)

// ListFlows lists Flow resources with enriched status and prints to stdout.
func ListFlows(ctx context.Context, c client.Client, namespace string, format output.Format) error {
	list := &automationv1alpha1.FlowList{}
	listOpts := []client.ListOption{}
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	if err := c.List(ctx, list, listOpts...); err != nil {
		return fmt.Errorf("listing Flows: %w", err)
	}

	// List FlowRuns to compute last-used per flow.
	frList := &automationv1alpha1.FlowRunList{}
	frOpts := []client.ListOption{}
	if namespace != "" {
		frOpts = append(frOpts, client.InNamespace(namespace))
	}
	_ = c.List(ctx, frList, frOpts...) // best-effort

	// Build map: flow name -> most recent FlowRun creationTimestamp.
	lastUsed := map[string]metav1.Time{}
	for _, fr := range frList.Items {
		flowName := fr.Spec.FlowRef.Name
		if flowName == "" {
			continue
		}
		if existing, ok := lastUsed[flowName]; !ok || fr.CreationTimestamp.After(existing.Time) {
			lastUsed[flowName] = fr.CreationTimestamp
		}
	}

	switch format {
	case output.FormatJSON:
		return output.PrintJSON(os.Stdout, list.Items)
	case output.FormatYAML:
		return output.PrintYAML(os.Stdout, list.Items)
	default:
		printFlowTable(os.Stdout, list.Items, lastUsed)
		return nil
	}
}

func printFlowTable(w io.Writer, items []automationv1alpha1.Flow, lastUsed map[string]metav1.Time) {
	headers := []string{"NAME", "STEPS", "READY", "LAST USED"}
	rows := make([][]string, 0, len(items))
	for _, f := range items {
		steps := fmt.Sprintf("%d", len(f.Spec.Steps))
		ready := flowReady(f.Status.Conditions)
		lu := "never"
		if t, ok := lastUsed[f.Name]; ok && !t.IsZero() {
			lu = output.FmtAge(t) + " ago"
		}
		rows = append(rows, []string{f.Name, steps, ready, lu})
	}
	output.PrintTable(w, headers, rows)
}

// flowReady returns the string value of the Ready condition.
func flowReady(conditions []metav1.Condition) string {
	for _, c := range conditions {
		if c.Type == "Ready" {
			return string(c.Status)
		}
	}
	return "Unknown"
}
