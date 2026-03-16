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
Determine if namespace-scoped RBAC should be used.
Returns "true" when watchNamespaces is a single non-empty namespace (OwnNamespace/SingleNamespace mode).
Returns "" (falsy) for AllNamespaces ("") and MultiNamespace ("ns1,ns2").
*/}}
{{- define "kubezap.namespacedRBAC" -}}
{{- $ns := .Values.watchNamespaces -}}
{{- if and (ne $ns "") (not (contains "," $ns)) -}}
true
{{- end -}}
{{- end }}
