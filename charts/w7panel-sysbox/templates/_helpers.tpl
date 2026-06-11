{{/*
Expand the name of the chart.
*/}}
{{- define "w7panel-sysbox.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "w7panel-sysbox.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "w7panel-sysbox.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "w7panel-sysbox.labels" -}}
helm.sh/chart: {{ include "w7panel-sysbox.chart" . }}
{{ include "w7panel-sysbox.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "w7panel-sysbox.selectorLabels" -}}
app.kubernetes.io/name: {{ include "w7panel-sysbox.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "w7panel-sysbox.serviceAccountName" -}}
{{- default (printf "%s-label-node" (include "w7panel-sysbox.fullname" .)) .Values.serviceAccount.name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Namespace for namespaced sysbox installer resources.
*/}}
{{- define "w7panel-sysbox.namespace" -}}
{{- default .Release.Namespace .Values.namespaceOverride }}
{{- end }}

{{/*
DaemonSet name.
*/}}
{{- define "w7panel-sysbox.daemonSetName" -}}
{{- default (printf "%s-deploy" (include "w7panel-sysbox.fullname" .)) .Values.daemonSet.name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
ClusterRole name.
*/}}
{{- define "w7panel-sysbox.clusterRoleName" -}}
{{- default (printf "%s-node-labeler" (include "w7panel-sysbox.fullname" .)) .Values.rbac.clusterRoleName | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
ClusterRoleBinding name.
*/}}
{{- define "w7panel-sysbox.clusterRoleBindingName" -}}
{{- default (printf "%s-label-node-rb" (include "w7panel-sysbox.fullname" .)) .Values.rbac.clusterRoleBindingName | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
RuntimeClass name.
*/}}
{{- define "w7panel-sysbox.runtimeClassName" -}}
{{- default "sysbox-runc" .Values.runtimeClass.name | trunc 63 | trimSuffix "-" }}
{{- end }}
