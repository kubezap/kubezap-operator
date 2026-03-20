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

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const controllerDeploymentName = "kubezap-controller-manager"
const defaultOperatorNamespace = "kubezap-system"

// PrintVersion prints CLI version info and looks up the operator version from the
// kubezap-controller-manager Deployment.
func PrintVersion(ctx context.Context, c client.Client, namespace, cliVersion, gitCommit string, w io.Writer) error {
	fmt.Fprintf(w, "kubezap CLI:      %s (git: %s)\n", cliVersion, gitCommit)

	operatorImage, foundNS := lookupOperatorImage(ctx, c, namespace)
	if operatorImage == "" {
		fmt.Fprintf(w, "operator image:   unknown (deployment not found)\n")
	} else {
		fmt.Fprintf(w, "operator image:   %s\n", operatorImage)
		fmt.Fprintf(w, "namespace:        %s\n", foundNS)
	}

	return nil
}

// lookupOperatorImage finds the first container image in the kubezap-controller-manager
// Deployment. It checks kubezap-system first, then falls back to the provided namespace.
func lookupOperatorImage(ctx context.Context, c client.Client, fallbackNamespace string) (string, string) {
	namespacesToTry := []string{defaultOperatorNamespace}
	if fallbackNamespace != "" && fallbackNamespace != defaultOperatorNamespace {
		namespacesToTry = append(namespacesToTry, fallbackNamespace)
	}

	for _, ns := range namespacesToTry {
		deploy := &appsv1.Deployment{}
		key := client.ObjectKey{Namespace: ns, Name: controllerDeploymentName}
		if err := c.Get(ctx, key, deploy); err != nil {
			continue
		}
		for _, container := range deploy.Spec.Template.Spec.Containers {
			if container.Name == "manager" || strings.Contains(container.Image, "controller") {
				return container.Image, ns
			}
		}
		// Fallback: first container.
		if len(deploy.Spec.Template.Spec.Containers) > 0 {
			return deploy.Spec.Template.Spec.Containers[0].Image, ns
		}
	}
	return "", ""
}
