{{/*
Expand the name of the chart.
*/}}
{{- define "kubezap.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kubezap.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart label.
*/}}
{{- define "kubezap.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "kubezap.labels" -}}
helm.sh/chart: {{ include "kubezap.chart" . }}
{{ include "kubezap.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "kubezap.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubezap.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "kubezap.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "kubezap.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Effective list of namespaces the operator's own reconciler RBAC (Role +
RoleBinding) must be granted in, as a JSON array for `range ... | fromJsonArray`.

  - watchOwnNamespace: true (default) -> [.Release.Namespace]              (OwnNamespace)
  - watchOwnNamespace: false, watchNamespaces: "ns"                        -> [.Release.Namespace, "ns"]         (SingleNamespace)
  - watchOwnNamespace: false, watchNamespaces: "ns1,ns2"                   -> [.Release.Namespace, "ns1","ns2"]  (MultiNamespace)

.Release.Namespace is always included, regardless of watch mode: the operator
runs there and must always be able to manage its own operational resources in
that namespace (its self-managed webhook serving certificate Secret in
particular — internal/webhookcerts operates in the operator's own namespace
unconditionally, not just when that namespace happens to be watched).
Deduplicated, so explicitly listing the release namespace in watchNamespaces
too is harmless. One templating loop covers all three watch modes — there is
no cluster-scoped fallback: the operator never needs a ClusterRole for its
own reconciler permissions (see docs/design/namespace-scoped-watch-modes-only.md).
*/}}
{{- define "kubezap.watchNamespaceList" -}}
{{- $result := list .Release.Namespace -}}
{{- if not .Values.watchOwnNamespace -}}
{{- range splitList "," .Values.watchNamespaces -}}
{{- $trimmed := trim . -}}
{{- if $trimmed -}}
{{- $result = append $result $trimmed -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $result | uniq | toJson -}}
{{- end }}

{{/*
Effective WATCH_NAMESPACES value for the controller.
When watchOwnNamespace is true, uses the release namespace.
When watchNamespaces is set (and watchOwnNamespace is false), uses that value.
*/}}
{{- define "kubezap.watchNamespaces" -}}
{{- if .Values.watchOwnNamespace -}}
{{- .Release.Namespace -}}
{{- else -}}
{{- .Values.watchNamespaces -}}
{{- end -}}
{{- end }}
