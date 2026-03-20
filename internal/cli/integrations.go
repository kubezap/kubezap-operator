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
	"net/http"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
	"github.com/borfswitch/kubezap/internal/cli/output"
)

// ListIntegrations lists Integration resources with gateway and plugin health status.
func ListIntegrations(ctx context.Context, c client.Client, namespace string, format output.Format) error {
	list := &automationv1alpha1.IntegrationList{}
	listOpts := []client.ListOption{}
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	if err := c.List(ctx, list, listOpts...); err != nil {
		return fmt.Errorf("listing Integrations: %w", err)
	}

	switch format {
	case output.FormatJSON:
		return output.PrintJSON(os.Stdout, list.Items)
	case output.FormatYAML:
		return output.PrintYAML(os.Stdout, list.Items)
	default:
		printIntegrationTable(ctx, os.Stdout, c, list.Items, namespace)
		return nil
	}
}

func printIntegrationTable(ctx context.Context, w io.Writer, c client.Client, items []automationv1alpha1.Integration, namespace string) {
	headers := []string{"NAME", "TYPE", "GATEWAY STATUS", "PLUGIN HEALTH"}
	rows := make([][]string, 0, len(items))

	for _, intg := range items {
		gatewayStatus := gatewayDeploymentStatus(ctx, c, &intg, namespace)
		pluginHealth := pluginHealthStatus(ctx, c, &intg, namespace)
		rows = append(rows, []string{
			intg.Name,
			output.Dash(intg.Spec.Type),
			gatewayStatus,
			pluginHealth,
		})
	}

	output.PrintTable(w, headers, rows)
}

// gatewayDeploymentStatus looks up the gateway Deployment for the integration
// and returns a human-readable status string.
func gatewayDeploymentStatus(ctx context.Context, c client.Client, intg *automationv1alpha1.Integration, namespace string) string {
	// Use GatewayDeploymentName from status if set.
	deployName := intg.Status.GatewayDeploymentName
	if deployName == "" {
		// Fall back to convention: kubezap-<type>-gateway-<name> or kubezap-plugin-<name>.
		switch intg.Spec.Type {
		case "plugin":
			deployName = "kubezap-plugin-" + intg.Name
		default:
			deployName = fmt.Sprintf("kubezap-%s-gateway-%s", intg.Spec.Type, intg.Name)
		}
	}

	ns := namespace
	if ns == "" {
		ns = intg.Namespace
	}

	deploy := &appsv1.Deployment{}
	key := client.ObjectKey{Namespace: ns, Name: deployName}
	if err := c.Get(ctx, key, deploy); err != nil {
		return "NotFound"
	}

	ready := deploy.Status.ReadyReplicas
	total := deploy.Status.Replicas
	if ready == total && total > 0 {
		return fmt.Sprintf("Ready (%d/%d)", ready, total)
	}
	return fmt.Sprintf("Degraded (%d/%d)", ready, total)
}

// pluginHealthStatus calls the plugin's /healthz endpoint for type=plugin integrations.
// Returns "n/a" for non-plugin types, "Healthy", "Unhealthy", or "unknown".
func pluginHealthStatus(ctx context.Context, c client.Client, intg *automationv1alpha1.Integration, namespace string) string {
	if intg.Spec.Type != "plugin" {
		return "n/a"
	}
	if intg.Spec.Plugin == nil {
		return "unknown"
	}

	// Build the service URL using the convention kubezap-plugin-<name>.<namespace>.svc.cluster.local.
	ns := namespace
	if ns == "" {
		ns = intg.Namespace
	}
	port := intg.Spec.Plugin.PublisherPort
	if port == 0 {
		port = 8090
	}
	url := fmt.Sprintf("http://kubezap-plugin-%s.%s.svc.cluster.local:%d/healthz", intg.Name, ns, port)

	httpClient := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "unknown"
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "unknown"
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return "Healthy"
	}
	return "Unhealthy"
}
