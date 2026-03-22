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
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/cli/output"
)

// ListTriggers lists Trigger resources with enriched status and prints to stdout.
func ListTriggers(ctx context.Context, c client.Client, namespace string, format output.Format) error {
	list := &automationv1alpha1.TriggerList{}
	listOpts := []client.ListOption{}
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	if err := c.List(ctx, list, listOpts...); err != nil {
		return fmt.Errorf("listing Triggers: %w", err)
	}

	// Also list all FlowRuns to calculate active counts per trigger.
	frList := &automationv1alpha1.FlowRunList{}
	frOpts := []client.ListOption{}
	if namespace != "" {
		frOpts = append(frOpts, client.InNamespace(namespace))
	}
	_ = c.List(ctx, frList, frOpts...) // best-effort; ignore error

	// Build map: trigger name -> active (Running) FlowRun count.
	activeCount := map[string]int{}
	for _, fr := range frList.Items {
		if fr.Spec.TriggerRef == nil {
			continue
		}
		if fr.Status.Phase == "Running" {
			activeCount[fr.Spec.TriggerRef.Name]++
		}
	}

	switch format {
	case output.FormatJSON:
		return output.PrintJSON(os.Stdout, list.Items)
	case output.FormatYAML:
		return output.PrintYAML(os.Stdout, list.Items)
	default:
		printTriggerTable(os.Stdout, list.Items, activeCount)
		return nil
	}
}

func printTriggerTable(w io.Writer, items []automationv1alpha1.Trigger, activeCount map[string]int) {
	headers := []string{"NAME", "TYPE", "STATUS", "LAST FIRED", "ACTIVE", "GC POLICY"}
	rows := make([][]string, 0, len(items))
	for _, t := range items {
		status := triggerStatus(t.Status.Conditions)
		lastFired := "never"
		if t.Status.LastTriggeredTime != nil {
			lastFired = output.FmtAge(*t.Status.LastTriggeredTime) + " ago"
		}
		active := fmt.Sprintf("%d", activeCount[t.Name])
		gcPolicy := fmtGCPolicy(t.Spec.FlowRunGC)

		rows = append(rows, []string{
			t.Name,
			output.Dash(t.Spec.Type),
			status,
			lastFired,
			active,
			gcPolicy,
		})
	}
	output.PrintTable(w, headers, rows)
}

// triggerStatus extracts a human-readable status from the trigger's conditions.
func triggerStatus(conditions []metav1.Condition) string {
	for _, c := range conditions {
		if c.Type == "Accepted" && c.Status == metav1.ConditionTrue {
			return "Accepted"
		}
	}
	for _, c := range conditions {
		if c.Status == metav1.ConditionFalse {
			return "Error: " + c.Reason
		}
	}
	return "Pending"
}

// fmtGCPolicy formats a FlowRunGCPolicy into a compact string.
func fmtGCPolicy(gc *automationv1alpha1.FlowRunGCPolicy) string {
	if gc == nil {
		return "default"
	}

	var parts []string

	// Count-based limits.
	if gc.MaxSucceeded != nil || gc.MaxFailed != nil {
		s := int32(0)
		f := int32(0)
		if gc.MaxSucceeded != nil {
			s = *gc.MaxSucceeded
		}
		if gc.MaxFailed != nil {
			f = *gc.MaxFailed
		}
		parts = append(parts, fmt.Sprintf("max:%d/%d", s, f))
	}

	// TTL-based limits.
	if gc.TTLAfterSucceeded != nil || gc.TTLAfterFailed != nil {
		var ttlParts []string
		if gc.TTLAfterSucceeded != nil {
			ttlParts = append(ttlParts, gc.TTLAfterSucceeded.Duration.String())
		} else {
			ttlParts = append(ttlParts, "-")
		}
		if gc.TTLAfterFailed != nil {
			ttlParts = append(ttlParts, gc.TTLAfterFailed.Duration.String())
		} else {
			ttlParts = append(ttlParts, "-")
		}
		parts = append(parts, "ttl:"+strings.Join(ttlParts, "/"))
	}

	if len(parts) == 0 {
		return "default"
	}
	return strings.Join(parts, " ")
}
