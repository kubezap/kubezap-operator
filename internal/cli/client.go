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

// Package cli provides shared utilities for the kubezap CLI binary.
package cli

import (
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// defaultNamespace is the fallback namespace used when none is configured or resolvable.
const defaultNamespace = "default"

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(automationv1alpha1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
}

// BuildClient loads a kubeconfig using the provided kubeconfig path and context
// override (either may be empty to fall back to defaults), constructs a
// controller-runtime client with the KubeZap scheme registered, and returns the
// client together with the active namespace.
//
// Namespace resolution order:
//  1. The namespace flag value (if non-empty).
//  2. The namespace set in the active kubeconfig context.
//  3. "default" as a final fallback.
func BuildClient(kubeconfigPath, contextName, namespaceFlagValue string) (client.Client, string, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}

	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("building REST config: %w", err)
	}

	// Determine active namespace.
	namespace := namespaceFlagValue
	if namespace == "" {
		ns, _, err := kubeConfig.Namespace()
		if err != nil {
			// Non-fatal: fall back to default.
			ns = defaultNamespace
		}
		if ns == "" {
			ns = defaultNamespace
		}
		namespace = ns
	}

	c, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, "", fmt.Errorf("creating Kubernetes client: %w", err)
	}

	return c, namespace, nil
}

// Version and GitCommit are set at link time via -ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
)

// HomeDir returns the user's home directory.
func HomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // Windows fallback
}
