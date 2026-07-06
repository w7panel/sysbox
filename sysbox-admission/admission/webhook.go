package admission

import (
	"fmt"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const WebhookName = "sysbox-webhook-mutator"

const RootfsMatchConditionExpression = `has(object.metadata.annotations) && "sysbox/rootfs-rw-layer" in object.metadata.annotations && object.metadata.annotations["sysbox/rootfs-rw-layer"] != ""`

type WebhookConfig struct {
	Name        string
	ServiceName string
	Namespace   string
	ServicePort int32
	CABundle    []byte
}

func BuildMutatingWebhookConfiguration(config WebhookConfig) (*admissionregistrationv1.MutatingWebhookConfiguration, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("webhook name is required")
	}
	if config.ServiceName == "" {
		return nil, fmt.Errorf("service name is required")
	}
	if config.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	path := "/mutate"
	port := config.ServicePort
	failurePolicy := admissionregistrationv1.Fail
	reinvocationPolicy := admissionregistrationv1.IfNeededReinvocationPolicy
	sideEffects := admissionregistrationv1.SideEffectClassNone
	timeoutSeconds := int32(10)
	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1ObjectMeta(config.Name),
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name:                    "rootfs-rw-layer.sysbox.nestybox.com",
				AdmissionReviewVersions: []string{"v1"},
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: config.Namespace,
						Name:      config.ServiceName,
						Path:      &path,
						Port:      &port,
					},
					CABundle: config.CABundle,
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"apps"},
							APIVersions: []string{"v1"},
							Resources:   []string{"deployments", "statefulsets", "daemonsets"},
						},
					},
					{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"batch"},
							APIVersions: []string{"v1"},
							Resources:   []string{"jobs", "cronjobs"},
						},
					},
					{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
						},
					},
				},
				FailurePolicy:      &failurePolicy,
				ReinvocationPolicy: &reinvocationPolicy,
				SideEffects:        &sideEffects,
				TimeoutSeconds:     &timeoutSeconds,
				MatchConditions: []admissionregistrationv1.MatchCondition{
					{Name: "has-rootfs-rw-layer", Expression: RootfsMatchConditionExpression},
				},
			},
		},
	}, nil
}

func metav1ObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name}
}
