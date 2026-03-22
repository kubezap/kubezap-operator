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

package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/borfswitch/kubezap/internal/cli"
)

// newWatchCmd returns the 'watch' subcommand.
func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch <flowrun-name>",
		Short: "Stream live execution timeline for a FlowRun",
		Long: `Watch a FlowRun in real time, streaming phase and step status updates
as the workflow executes.

A new timeline snapshot is printed for each status change received from the
Kubernetes Watch API. The command exits automatically when the FlowRun reaches
a terminal phase (Succeeded, Failed, Cancelled).

Status badges:
  ✓  Succeeded
  ✗  Failed
  ●  Running
  ○  Pending / Waiting
  -  Skipped`,
		Example: `  kubezap watch order-webhook-20260318-abc4f
  kubezap watch order-webhook-20260318-abc4f -n production`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, namespace, err := cli.BuildClient(kubeconfigFlag, contextFlag, namespaceFlag)
			if err != nil {
				return err
			}
			return cli.WatchFlowRun(context.TODO(), c, namespace, args[0], os.Stdout)
		},
	}
}
