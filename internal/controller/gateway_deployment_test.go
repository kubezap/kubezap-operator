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

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("desiredWebhookGatewayRole", func() {
	It("grants get access to secrets, required to resolve webhook auth secretRefs", func() {
		role := desiredWebhookGatewayRole("default")

		hasSecretsGet := false
		for _, rule := range role.Rules {
			if containsString(rule.APIGroups, "") && containsString(rule.Resources, "secrets") && containsString(rule.Verbs, "get") {
				hasSecretsGet = true
			}
		}
		Expect(hasSecretsGet).To(BeTrue(), "webhook gateway Role must grant get on secrets, or hmac/bearer/basic/apiKey/header-equals auth can never resolve their secretRef against a live cluster")
	})
})
