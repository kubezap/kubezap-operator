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
	"testing"
)

func TestResolveCacheNamespaces(t *testing.T) {
	tests := []struct {
		name              string
		watchNS           string
		operatorNamespace string
		want              []string
	}{
		{
			name:              "OwnNamespace mode: empty WATCH_NAMESPACES watches only the operator namespace",
			watchNS:           "",
			operatorNamespace: "kubezap-system",
			want:              []string{"kubezap-system"},
		},
		{
			name:              "MultiNamespace mode: operator namespace is always included even when omitted from the list",
			watchNS:           "team-a,team-b",
			operatorNamespace: "kubezap-system",
			want:              []string{"kubezap-system", "team-a", "team-b"},
		},
		{
			name:              "MultiNamespace mode: no duplicate entry when the operator namespace is also explicitly listed",
			watchNS:           "kubezap-system,team-a",
			operatorNamespace: "kubezap-system",
			want:              []string{"kubezap-system", "team-a"},
		},
		{
			name:              "MultiNamespace mode: whitespace around entries is trimmed",
			watchNS:           " team-a , team-b ",
			operatorNamespace: "kubezap-system",
			want:              []string{"kubezap-system", "team-a", "team-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveCacheNamespaces(tt.watchNS, tt.operatorNamespace)

			if len(got) != len(tt.want) {
				t.Fatalf("resolveCacheNamespaces(%q, %q) = %v, want namespaces %v",
					tt.watchNS, tt.operatorNamespace, got, tt.want)
			}
			for _, ns := range tt.want {
				if _, ok := got[ns]; !ok {
					t.Errorf("resolveCacheNamespaces(%q, %q) missing expected namespace %q, got %v",
						tt.watchNS, tt.operatorNamespace, ns, got)
				}
			}
		})
	}
}
