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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

// terminalPhases are FlowRun phases that indicate no further progress.
var terminalPhases = map[string]bool{
	"Succeeded": true,
	"Failed":    true,
	"Cancelled": true,
}

// stepBadge returns the single-character badge for a step phase.
func stepBadge(phase string) string {
	switch phase {
	case "Succeeded":
		return "✓"
	case "Failed":
		return "✗"
	case "Running":
		return "●"
	case "Skipped":
		return "-"
	default:
		// Pending, Waiting, or empty
		return "○"
	}
}

// RenderFlowRunTimeline prints the FlowRun execution timeline to out.
// elapsed is the wall-clock duration since the FlowRun started (used when
// Status.StartTime is nil or the FlowRun is still running).
func RenderFlowRunTimeline(fr *automationv1alpha1.FlowRun, elapsed time.Duration, out io.Writer) {
	triggerName := "-"
	if fr.Spec.TriggerRef != nil {
		triggerName = fr.Spec.TriggerRef.Name
	}

	phase := fr.Status.Phase
	if phase == "" {
		phase = "Pending"
	}

	// Compute elapsed: prefer actual duration if complete, otherwise use the
	// caller-supplied elapsed (wall clock since watch start).
	var elapsedStr string
	switch {
	case fr.Status.StartTime != nil && fr.Status.CompletionTime != nil:
		d := fr.Status.CompletionTime.Time.Sub(fr.Status.StartTime.Time)
		elapsedStr = fmt.Sprintf("%.1fs", d.Seconds())
	case fr.Status.StartTime != nil:
		d := time.Since(fr.Status.StartTime.Time)
		elapsedStr = fmt.Sprintf("%.1fs", d.Seconds())
	default:
		elapsedStr = fmt.Sprintf("%.1fs", elapsed.Seconds())
	}

	fmt.Fprintln(out, "---")
	fmt.Fprintf(out, "FlowRun: %s\n", fr.Name)
	fmt.Fprintf(out, "Trigger: %-20s  Flow: %s\n", triggerName, fr.Spec.FlowRef.Name)
	fmt.Fprintf(out, "Phase:   %-12s (%s elapsed)\n", phase, elapsedStr)

	if len(fr.Status.Steps) == 0 {
		return
	}

	fmt.Fprintln(out)
	for _, step := range fr.Status.Steps {
		badge := stepBadge(step.Phase)

		stepPhase := step.Phase
		if stepPhase == "" {
			stepPhase = "Pending"
		}

		var durStr string
		switch {
		case step.StartTime != nil && step.CompletionTime != nil:
			d := step.CompletionTime.Time.Sub(step.StartTime.Time)
			durStr = fmt.Sprintf("%.1fs", d.Seconds())
		case step.StartTime != nil:
			d := time.Since(step.StartTime.Time)
			durStr = fmt.Sprintf("~%.1fs", d.Seconds())
		default:
			durStr = "-"
		}

		fmt.Fprintf(out, "  %s  %-24s %-12s %s\n", badge, step.Name, stepPhase, durStr)
	}
}

// WatchFlowRun fetches the initial FlowRun state then opens a Kubernetes Watch
// on that specific resource, re-rendering the terminal execution timeline on
// each event. It exits when the FlowRun reaches a terminal phase (Succeeded,
// Failed, Cancelled) or ctx is cancelled.
func WatchFlowRun(ctx context.Context, c client.Client, namespace, name string, out io.Writer) error {
	// Initial fetch to validate existence and get the resourceVersion.
	fr := &automationv1alpha1.FlowRun{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, fr); err != nil {
		return fmt.Errorf("getting FlowRun %q: %w", name, err)
	}

	watchStart := time.Now()

	// Render initial state.
	RenderFlowRunTimeline(fr, time.Since(watchStart), out)

	// Early exit if already terminal.
	if terminalPhases[fr.Status.Phase] {
		return nil
	}

	// Open a Watch scoped to this specific resource via a field selector on
	// metadata.name. controller-runtime's client.Client does not expose a
	// Watch method directly; use the underlying scheme-aware watch via
	// client.WithWatch.
	wc, ok := c.(client.WithWatch)
	if !ok {
		return fmt.Errorf("Kubernetes client does not support Watch; upgrade to a watch-capable client")
	}

	watchList := &automationv1alpha1.FlowRunList{}
	watchOpts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingFields{"metadata.name": name},
		client.Continue(fr.ResourceVersion),
	}

	watcher, err := wc.Watch(ctx, watchList, watchOpts...)
	if err != nil {
		// Fall back to field-selector-less watch and filter client-side.
		watchOpts = []client.ListOption{
			client.InNamespace(namespace),
		}
		watcher, err = wc.Watch(ctx, watchList, watchOpts...)
		if err != nil {
			return fmt.Errorf("opening Watch on FlowRuns: %w", err)
		}
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Channel closed — watcher stopped or timed out.
				return nil
			}
			if event.Type == watch.Error {
				return fmt.Errorf("watch error event received")
			}

			updated, ok := event.Object.(*automationv1alpha1.FlowRun)
			if !ok {
				continue
			}
			// Filter to only the requested FlowRun when watching all in namespace.
			if updated.Name != name {
				continue
			}

			RenderFlowRunTimeline(updated, time.Since(watchStart), out)

			if terminalPhases[updated.Status.Phase] {
				return nil
			}
		}
	}
}

// buildWatchListOptions returns the ListOptions used to construct a metav1 Watch
// request for a single named FlowRun. Exported for testing.
func buildWatchListOptions(namespace, name, resourceVersion string) metav1.ListOptions {
	return metav1.ListOptions{
		FieldSelector:   "metadata.name=" + name,
		ResourceVersion: resourceVersion,
		Watch:           true,
	}
}

// isTerminalPhase reports whether phase is a terminal FlowRun phase.
func isTerminalPhase(phase string) bool {
	return terminalPhases[phase]
}

// fmtStepLine formats a single step line for the timeline. Exported for testing.
func fmtStepLine(step automationv1alpha1.StepRunStatus) string {
	badge := stepBadge(step.Phase)
	stepPhase := step.Phase
	if stepPhase == "" {
		stepPhase = "Pending"
	}

	var durStr string
	switch {
	case step.StartTime != nil && step.CompletionTime != nil:
		d := step.CompletionTime.Time.Sub(step.StartTime.Time)
		durStr = fmt.Sprintf("%.1fs", d.Seconds())
	case step.StartTime != nil:
		// Still running; use elapsed since start.
		d := time.Since(step.StartTime.Time)
		durStr = fmt.Sprintf("~%.1fs", d.Seconds())
	default:
		durStr = "-"
	}

	return strings.TrimRight(
		fmt.Sprintf("  %s  %-24s %-12s %s", badge, step.Name, stepPhase, durStr),
		" ",
	)
}
