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

// kubezap is a read-only CLI for inspecting KubeZap resources in a Kubernetes
// cluster. It is distributed as kubectl-kubezap so it can be invoked as either
// `kubezap <command>` or `kubectl kubezap <command>`.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kubezap/kubezap-operator/internal/cli"
	"github.com/kubezap/kubezap-operator/internal/cli/output"
)

// Global persistent flag values populated by cobra before each command runs.
var (
	kubeconfigFlag string
	contextFlag    string
	namespaceFlag  string
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// rootCmd builds the root cobra command with persistent flags and all
// subcommands attached.
func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "kubezap",
		Short: "Inspect KubeZap resources in a Kubernetes cluster",
		Long: `kubezap is a read-only CLI for KubeZap — a Kubernetes workflow automation operator.

It provides richer views than 'kubectl get' for KubeZap resources, with
FlowRun history querying as the primary use case.

All commands are read-only. Write operations are not supported.

Kubeconfig is resolved from --kubeconfig, $KUBECONFIG, ~/.kube/config, or
in-cluster credentials — in that order, matching kubectl behavior.`,
		SilenceUsage: true,
	}

	// Persistent flags available on every subcommand.
	root.PersistentFlags().StringVar(&kubeconfigFlag, "kubeconfig", "",
		"Path to the kubeconfig file. Defaults to $KUBECONFIG or ~/.kube/config.")
	root.PersistentFlags().StringVar(&contextFlag, "context", "",
		"Kubeconfig context to use. Defaults to the current context.")
	root.PersistentFlags().StringVarP(&namespaceFlag, "namespace", "n", "",
		"Kubernetes namespace. Defaults to the namespace set in the active context.")

	root.AddCommand(
		newHistoryCmd(),
		newTriggersCmd(),
		newFlowsCmd(),
		newIntegrationsCmd(),
		newVersionCmd(),
		newWatchCmd(),
	)

	return root
}

// newHistoryCmd returns the 'history' subcommand.
func newHistoryCmd() *cobra.Command {
	var (
		triggerFilter string
		flowFilter    string
		phaseFilter   string
		sinceFlag     string
		watchFlag     bool
		allNamespaces bool
		outputFormat  string
	)

	cmd := &cobra.Command{
		Use:   "history [flowrun-name]",
		Short: "List or inspect FlowRun history",
		Long: `List FlowRun execution history with optional filters, or show a detailed
per-step timeline for a single FlowRun.

List form:
  kubezap history [-n <ns>] [--trigger <name>] [--flow <name>]
                  [--phase <phase>] [--since <duration>] [--watch]

Detail form:
  kubezap history <flowrun-name> [-n <ns>]

Results are sorted newest first. Use --watch to live-tail completions.`,
		Example: `  # List all FlowRuns in the current namespace
  kubezap history

  # Filter by trigger and phase
  kubezap history --trigger order-webhook --phase Failed

  # Show FlowRuns from the last 2 hours across all namespaces
  kubezap history -A --since 2h

  # Show detail for a specific FlowRun
  kubezap history order-123-20260318-abc4f

  # Live-tail completions
  kubezap history --watch`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, namespace, err := cli.BuildClient(kubeconfigFlag, contextFlag, resolveNamespace(allNamespaces))
			if err != nil {
				return err
			}

			ctx := context.TODO()

			if len(args) == 1 {
				// Detail form: single FlowRun.
				format, err := output.ParseFormat(outputFormat)
				if err != nil {
					return err
				}
				return cli.GetFlowRun(ctx, c, namespace, args[0], format)
			}

			// Parse --since duration.
			var since time.Duration
			if sinceFlag != "" {
				since, err = time.ParseDuration(sinceFlag)
				if err != nil {
					return fmt.Errorf("invalid --since value %q: %w", sinceFlag, err)
				}
			}

			format, err := output.ParseFormat(outputFormat)
			if err != nil {
				return err
			}

			opts := cli.ListFlowRunOpts{
				TriggerFilter: triggerFilter,
				FlowFilter:    flowFilter,
				PhaseFilter:   phaseFilter,
				Since:         since,
				Format:        format,
			}

			if watchFlag {
				return cli.WatchFlowRuns(ctx, c, namespace, opts)
			}
			return cli.ListFlowRuns(ctx, c, namespace, opts)
		},
	}

	cmd.Flags().StringVar(&triggerFilter, "trigger", "", "Filter by trigger name.")
	cmd.Flags().StringVar(&flowFilter, "flow", "", "Filter by flow name.")
	cmd.Flags().StringVar(&phaseFilter, "phase", "", "Filter by phase (Pending|Running|Succeeded|Failed|Cancelled).")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "Only show FlowRuns created within this duration (e.g. 1h, 30m).")
	cmd.Flags().BoolVarP(&watchFlag, "watch", "w", false, "Live-tail FlowRun completions.")
	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "Query all namespaces.")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, json, yaml.")

	return cmd
}

// newTriggersCmd returns the 'triggers' subcommand.
func newTriggersCmd() *cobra.Command {
	var (
		allNamespaces bool
		outputFormat  string
	)

	cmd := &cobra.Command{
		Use:   "triggers",
		Short: "List Trigger resources with enriched status",
		Long: `List Trigger resources in the cluster. Displays enriched status including
type, condition, last fired time, active FlowRun count, and effective GC policy.

This surfaces cross-referenced state that requires multiple kubectl commands
to assemble manually.`,
		Example: `  kubezap triggers
  kubezap triggers -n production
  kubezap triggers -A -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, namespace, err := cli.BuildClient(kubeconfigFlag, contextFlag, resolveNamespace(allNamespaces))
			if err != nil {
				return err
			}
			format, err := output.ParseFormat(outputFormat)
			if err != nil {
				return err
			}
			return cli.ListTriggers(context.TODO(), c, namespace, format)
		},
	}

	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "Query all namespaces.")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, json, yaml.")

	return cmd
}

// newFlowsCmd returns the 'flows' subcommand.
func newFlowsCmd() *cobra.Command {
	var (
		allNamespaces bool
		outputFormat  string
	)

	cmd := &cobra.Command{
		Use:   "flows",
		Short: "List Flow resources with enriched status",
		Long: `List Flow resources in the cluster. Displays step count, Ready condition,
and last used time (inferred from the most recent FlowRun referencing each Flow).`,
		Example: `  kubezap flows
  kubezap flows -n staging
  kubezap flows -A -o yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, namespace, err := cli.BuildClient(kubeconfigFlag, contextFlag, resolveNamespace(allNamespaces))
			if err != nil {
				return err
			}
			format, err := output.ParseFormat(outputFormat)
			if err != nil {
				return err
			}
			return cli.ListFlows(context.TODO(), c, namespace, format)
		},
	}

	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "Query all namespaces.")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, json, yaml.")

	return cmd
}

// newIntegrationsCmd returns the 'integrations' subcommand.
func newIntegrationsCmd() *cobra.Command {
	var (
		allNamespaces bool
		outputFormat  string
	)

	cmd := &cobra.Command{
		Use:   "integrations",
		Short: "List Integration resources with gateway and plugin health",
		Long: `List Integration resources in the cluster. Displays type, gateway Deployment
readiness, and plugin health check status (for type: plugin integrations).`,
		Example: `  kubezap integrations
  kubezap integrations -n production
  kubezap integrations -A -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, namespace, err := cli.BuildClient(kubeconfigFlag, contextFlag, resolveNamespace(allNamespaces))
			if err != nil {
				return err
			}
			format, err := output.ParseFormat(outputFormat)
			if err != nil {
				return err
			}
			return cli.ListIntegrations(context.TODO(), c, namespace, format)
		},
	}

	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "Query all namespaces.")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, json, yaml.")

	return cmd
}

// newVersionCmd returns the 'version' subcommand.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print CLI and operator version information",
		Long: `Print the kubezap CLI version and the operator version read from the
kubezap-controller-manager Deployment in the cluster.`,
		Example: `  kubezap version`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, namespace, err := cli.BuildClient(kubeconfigFlag, contextFlag, namespaceFlag)
			if err != nil {
				// version can still print CLI info without a cluster connection.
				fmt.Fprintf(os.Stdout, "kubezap CLI:      %s (git: %s)\n", cli.Version, cli.GitCommit)
				fmt.Fprintf(os.Stdout, "operator image:   unknown (no cluster connection: %v)\n", err)
				return nil
			}
			return cli.PrintVersion(context.TODO(), c, namespace, cli.Version, cli.GitCommit, os.Stdout)
		},
	}
}

// resolveNamespace returns an empty string when allNamespaces is true (which
// triggers all-namespace list behaviour in BuildClient callers), otherwise
// returns the value of the persistent namespace flag.
func resolveNamespace(allNamespaces bool) string {
	if allNamespaces {
		return ""
	}
	return namespaceFlag
}
