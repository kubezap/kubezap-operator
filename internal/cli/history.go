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
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/cli/output"
)

// headerDuration is the "DURATION" column header shared by the FlowRun and
// step history table views in this file.
const headerDuration = "DURATION"

// headerPhase is the "PHASE" column header shared by the FlowRun and step
// history table views in this file.
const headerPhase = "PHASE"

// ListFlowRunOpts holds filter options for ListFlowRuns.
type ListFlowRunOpts struct {
	TriggerFilter string
	FlowFilter    string
	PhaseFilter   string
	Since         time.Duration
	Format        output.Format
}

// ListFlowRuns lists FlowRuns with optional filters and prints them to stdout.
func ListFlowRuns(ctx context.Context, c client.Client, namespace string, opts ListFlowRunOpts) error {
	list := &automationv1alpha1.FlowRunList{}
	listOpts := []client.ListOption{}
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	// Use label selector for trigger filter when set.
	if opts.TriggerFilter != "" {
		listOpts = append(listOpts, client.MatchingLabels{
			"kubezap.io/trigger": opts.TriggerFilter,
		})
	}

	if err := c.List(ctx, list, listOpts...); err != nil {
		return fmt.Errorf("listing FlowRuns: %w", err)
	}

	items := list.Items

	// Client-side filters.
	filtered := make([]automationv1alpha1.FlowRun, 0, len(items))
	cutoff := time.Time{}
	if opts.Since > 0 {
		cutoff = time.Now().Add(-opts.Since)
	}

	for _, fr := range items {
		if opts.FlowFilter != "" && fr.Spec.FlowRef.Name != opts.FlowFilter {
			continue
		}
		if opts.PhaseFilter != "" && !strings.EqualFold(string(fr.Status.Phase), opts.PhaseFilter) {
			continue
		}
		if !cutoff.IsZero() && fr.CreationTimestamp.Time.Before(cutoff) {
			continue
		}
		filtered = append(filtered, fr)
	}

	// Sort newest first.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreationTimestamp.After(filtered[j].CreationTimestamp.Time)
	})

	switch opts.Format {
	case output.FormatJSON:
		return output.PrintJSON(os.Stdout, filtered)
	case output.FormatYAML:
		return output.PrintYAML(os.Stdout, filtered)
	default:
		printFlowRunTable(os.Stdout, filtered)
		return nil
	}
}

func printFlowRunTable(w io.Writer, items []automationv1alpha1.FlowRun) {
	headers := []string{headerName, "TRIGGER", "FLOW", headerPhase, headerDuration, "AGE"}
	rows := make([][]string, 0, len(items))
	for i := range items {
		fr := &items[i]
		triggerName := "-"
		if fr.Spec.TriggerRef != nil {
			triggerName = fr.Spec.TriggerRef.Name
		}
		phase := output.Dash(string(fr.Status.Phase))
		dur := flowRunDuration(fr)
		age := output.FmtAge(fr.CreationTimestamp)
		rows = append(rows, []string{
			fr.Name,
			triggerName,
			output.Dash(fr.Spec.FlowRef.Name),
			phase,
			dur,
			age,
		})
	}
	output.PrintTable(w, headers, rows)
}

// flowRunDuration returns a formatted duration string for a FlowRun.
// If complete, it shows CompletionTime-StartTime. If still running, elapsed since StartTime.
func flowRunDuration(fr *automationv1alpha1.FlowRun) string {
	if fr.Status.StartTime == nil {
		return "-"
	}
	if fr.Status.CompletionTime != nil {
		return output.FmtStepDuration(fr.Status.StartTime, fr.Status.CompletionTime)
	}
	// Still running: show elapsed.
	d := time.Since(fr.Status.StartTime.Time)
	return output.FmtDuration(d)
}

// GetFlowRun retrieves a single FlowRun and prints detail including step timeline.
func GetFlowRun(ctx context.Context, c client.Client, namespace, name string, format output.Format) error {
	fr := &automationv1alpha1.FlowRun{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, fr); err != nil {
		return fmt.Errorf("getting FlowRun %q: %w", name, err)
	}

	switch format {
	case output.FormatJSON:
		return output.PrintJSON(os.Stdout, fr)
	case output.FormatYAML:
		return output.PrintYAML(os.Stdout, fr)
	default:
		printFlowRunDetail(os.Stdout, fr)
		return nil
	}
}

func printFlowRunDetail(w io.Writer, fr *automationv1alpha1.FlowRun) {
	triggerName := "-"
	if fr.Spec.TriggerRef != nil {
		triggerName = fr.Spec.TriggerRef.Name
	}

	startStr := "-"
	if fr.Status.StartTime != nil {
		startStr = fr.Status.StartTime.UTC().Format("2006-01-02 15:04:05 UTC")
	}

	dur := flowRunDuration(fr)

	fmt.Fprintf(w, "FlowRun:   %s\n", fr.Name)
	fmt.Fprintf(w, "Flow:      %s\n", output.Dash(fr.Spec.FlowRef.Name))
	fmt.Fprintf(w, "Trigger:   %s\n", triggerName)
	fmt.Fprintf(w, "Phase:     %s\n", output.Dash(string(fr.Status.Phase)))
	fmt.Fprintf(w, "Started:   %s\n", startStr)
	fmt.Fprintf(w, "Duration:  %s\n", dur)

	if len(fr.Status.Steps) == 0 {
		return
	}

	fmt.Fprintln(w)
	headers := []string{"STEP", headerPhase, "STARTED", headerDuration, "ATTEMPTS", "RESULTS"}
	rows := make([][]string, 0, len(fr.Status.Steps))

	for _, step := range fr.Status.Steps {
		startedOffset := "-"
		if step.StartTime != nil && fr.Status.StartTime != nil {
			offset := step.StartTime.Sub(fr.Status.StartTime.Time)
			startedOffset = fmt.Sprintf("+%.2fs", offset.Seconds())
		}

		stepDur := output.FmtStepDuration(step.StartTime, step.CompletionTime)
		attempts := fmt.Sprintf("%d", step.Attempts)
		results := fmtResults(step.Results)

		rows = append(rows, []string{
			step.Name,
			output.Dash(string(step.Phase)),
			startedOffset,
			stepDur,
			attempts,
			results,
		})
	}

	output.PrintTable(w, headers, rows)

	// Print failure messages below the table.
	for _, step := range fr.Status.Steps {
		if step.Message != "" && step.Phase == automationv1alpha1.StepPhaseFailed {
			fmt.Fprintf(w, "  message: %s\n", step.Message)
		}
	}
}

func fmtResults(results []automationv1alpha1.ResultValue) string {
	if len(results) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, r.Name+"="+r.Value)
	}
	return strings.Join(parts, " ")
}

// WatchFlowRuns polls for FlowRun completions every 2 seconds and prints new
// terminal-phase rows as they arrive. Runs until ctx is cancelled.
//
// TODO: Replace polling with a proper controller-runtime Watch call once the
// watch API usage pattern is established for CLI tools.
func WatchFlowRuns(ctx context.Context, c client.Client, namespace string, opts ListFlowRunOpts) error {
	seen := map[string]automationv1alpha1.FlowRunPhase{} // name -> phase at last check

	headers := []string{headerName, "TRIGGER", "FLOW", headerPhase, headerDuration, "AGE"}
	output.PrintTable(os.Stdout, headers, nil)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			list := &automationv1alpha1.FlowRunList{}
			listOpts := []client.ListOption{}
			if namespace != "" {
				listOpts = append(listOpts, client.InNamespace(namespace))
			}
			if opts.TriggerFilter != "" {
				listOpts = append(listOpts, client.MatchingLabels{
					"kubezap.io/trigger": opts.TriggerFilter,
				})
			}
			if err := c.List(ctx, list, listOpts...); err != nil {
				continue
			}
			for i := range list.Items {
				fr := &list.Items[i]
				phase := fr.Status.Phase
				if phase != automationv1alpha1.FlowRunPhaseSucceeded && phase != automationv1alpha1.FlowRunPhaseFailed && phase != automationv1alpha1.FlowRunPhaseCancelled {
					continue
				}
				if opts.FlowFilter != "" && fr.Spec.FlowRef.Name != opts.FlowFilter {
					continue
				}
				if prev, ok := seen[fr.Name]; ok && prev == phase {
					continue
				}
				seen[fr.Name] = phase

				triggerName := "-"
				if fr.Spec.TriggerRef != nil {
					triggerName = fr.Spec.TriggerRef.Name
				}
				dur := flowRunDuration(fr)
				age := output.FmtAge(fr.CreationTimestamp)
				fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\n",
					fr.Name, triggerName, output.Dash(fr.Spec.FlowRef.Name), phase, dur, age)
			}
		}
	}
}
