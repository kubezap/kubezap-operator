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

// Shared string constants referenced from more than one file in this package.
//
// Each literal below is repeated across two or more files in this package, so
// it gets exactly one constant here rather than being duplicated (or
// separately named) per file. A literal that only repeats within a single
// file instead gets a file-scoped const in that file (see e.g.
// gateway_deployment.go's top-level consts).
const (
	// labelApp is the conventional short "app" label key used to select gateway
	// and plugin Deployments and their Pods.
	labelApp = "app"

	// labelComponent is the kubezap.io/component label key identifying which
	// gateway role a Deployment/Pod plays (webhook-gateway, kafka-gateway,
	// amqp-gateway, nats-gateway).
	labelComponent = "kubezap.io/component"

	// labelTrigger is the kubezap.io/trigger label key recording the
	// originating Trigger name on FlowRuns.
	labelTrigger = "kubezap.io/trigger"

	// conditionTypeReady is the metav1.Condition Type used across Flow,
	// Integration, and Trigger status conditions to report overall readiness.
	conditionTypeReady = "Ready"

	// healthzPath is the HTTP health-check path exposed by gateway and
	// executor Deployments, used as their liveness/readiness probe path.
	healthzPath = "/healthz"

	// triggerTypeCron / triggerTypeResource are TriggerSpec.Type values,
	// mirroring triggerTypeWebhook / integrationTypeKafka in
	// integration_controller.go.
	triggerTypeCron     = "cron"
	triggerTypeResource = "resource"

	// apiGroupAutomation / apiGroupRBAC are the API groups referenced by
	// gateway and plugin RBAC PolicyRules/RoleRefs.
	apiGroupAutomation = "automation.kubezap.io"
	apiGroupRBAC       = "rbac.authorization.k8s.io"

	// kindRole / kindServiceAccount are the RBAC Kind values used in
	// RoleRef/Subject entries for gateway and plugin RoleBindings.
	kindRole           = "Role"
	kindServiceAccount = "ServiceAccount"

	// resourceTriggers / resourceFlowRuns / resourceSecrets are the RBAC
	// resource names granted to gateway/plugin Roles.
	resourceTriggers = "triggers"
	resourceFlowRuns = "flowruns"
	resourceSecrets  = "secrets"

	// verbGet / verbCreate / verbList / verbWatch are RBAC verbs used across
	// gateway/plugin PolicyRules.
	verbGet    = "get"
	verbCreate = "create"
	verbList   = "list"
	verbWatch  = "watch"
)
