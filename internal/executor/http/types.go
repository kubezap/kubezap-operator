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

package executorhttp

// ExecuteRequest is the JSON body accepted by POST /execute.
// The controller resolves all secret references and auth headers before sending.
type ExecuteRequest struct {
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
	TLSSkipVerify  bool              `json:"tlsSkipVerify,omitempty"`
}

// ExecuteResponse is the JSON body always returned with HTTP 200 from POST /execute.
// Transport errors are embedded in the Error field; the executor itself never returns
// a non-200 for upstream failures.
type ExecuteResponse struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body"`
	Truncated  bool              `json:"truncated"`
	Error      string            `json:"error,omitempty"`
}
