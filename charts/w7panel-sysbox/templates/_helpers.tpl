{{/*
Expand the name of the chart.
*/}}
{{- define "w7panel-sysbox.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "w7panel-sysbox.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
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
{{- printf "%s-installer" (include "w7panel-sysbox.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Namespace for namespaced sysbox installer resources.
*/}}
{{- define "w7panel-sysbox.namespace" -}}
{{- .Release.Namespace }}
{{- end }}

{{/*
Installer DaemonSet name.
*/}}
{{- define "w7panel-sysbox.daemonSetName" -}}
{{- printf "%s-installer" (include "w7panel-sysbox.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Nested agent DaemonSet name.
*/}}
{{- define "w7panel-sysbox.nestedAgentName" -}}
{{- printf "%s-nested-agent" (include "w7panel-sysbox.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
ClusterRole name.
*/}}
{{- define "w7panel-sysbox.clusterRoleName" -}}
{{- printf "%s-installer" (include "w7panel-sysbox.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
ClusterRoleBinding name.
*/}}
{{- define "w7panel-sysbox.clusterRoleBindingName" -}}
{{- printf "%s-installer" (include "w7panel-sysbox.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
RuntimeClass name.
*/}}
{{- define "w7panel-sysbox.runtimeClassName" -}}
{{- "sysbox-runc" }}
{{- end }}

{{/*
Render a mutable tag reference only when the caller did not pin an immutable
digest. The caller passes repository, tag, and digest explicitly so admission
can inherit installer defaults without duplicating this behavior.
*/}}
{{- define "w7panel-sysbox.imageRef" -}}
{{- if .digest -}}
{{- printf "%s@%s" .repository .digest -}}
{{- else -}}
{{- printf "%s:%s" .repository .tag -}}
{{- end -}}
{{- end }}

{{/*
Admission backend Deployment name.
*/}}
{{- define "w7panel-sysbox.admissionName" -}}
{{- printf "%s-admission" (include "w7panel-sysbox.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission backend Service name.
*/}}
{{- define "w7panel-sysbox.admissionServiceName" -}}
{{- printf "%s-admission" (include "w7panel-sysbox.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission mutating webhook name.
*/}}
{{- define "w7panel-sysbox.admissionWebhookName" -}}
{{- "sysbox-webhook-mutator" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission CA Secret name.
*/}}
{{- define "w7panel-sysbox.admissionCASecretName" -}}
{{- "sysbox-admission-webhook-ca" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission TLS Secret name.
*/}}
{{- define "w7panel-sysbox.admissionTLSSecretName" -}}
{{- "sysbox-admission-webhook-tls" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission lifecycle Lease name.
*/}}
{{- define "w7panel-sysbox.admissionLeaseName" -}}
{{- "sysbox-admission-webhook-init" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission selector labels.
*/}}
{{- define "w7panel-sysbox.admissionSelectorLabels" -}}
app.kubernetes.io/name: {{ include "w7panel-sysbox.name" . }}-admission
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
