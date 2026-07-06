package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/nestybox/sysbox-admission/admission"
	containerdUtils "github.com/nestybox/sysbox-libs/containerdUtils"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
)

const defaultCertificateRotationInterval = 6 * time.Hour
const defaultCertificateRenewalWindow = 90 * 24 * time.Hour
const defaultCertificateSecretRefreshInterval = time.Minute

type bootstrapTLSRuntime struct {
	client   kubernetes.Interface
	manager  *admission.LifecycleManager
	reloader *admission.CertificateReloader
}

func main() {
	addr := flag.String("addr", ":9443", "https listen address")
	tlsCert := flag.String("tls-cert", "", "tls certificate path")
	tlsKey := flag.String("tls-key", "", "tls key path")
	bootstrapWebhook := flag.Bool("bootstrap-webhook", false, "bootstrap webhook certificates and Kubernetes webhook configuration")
	namespace := flag.String("namespace", defaultNamespace(), "namespace for webhook-owned runtime resources")
	serviceName := flag.String("service-name", "sysbox-admission", "Kubernetes Service name for webhook callbacks")
	servicePort := flag.Int("service-port", 443, "Kubernetes Service port for webhook callbacks")
	webhookName := flag.String("webhook-name", admission.WebhookName, "MutatingWebhookConfiguration name")
	caSecretName := flag.String("ca-secret-name", "sysbox-admission-webhook-ca", "CA Secret name")
	tlsSecretName := flag.String("tls-secret-name", "sysbox-admission-webhook-tls", "TLS Secret name")
	leaseName := flag.String("lease-name", "sysbox-admission-webhook-init", "initialization Lease name")
	leaderIdentityOverride := flag.String("leader-identity", "", "leader election identity override; defaults to hostname")
	rotationInterval := flag.Duration("certificate-rotation-interval", defaultCertificateRotationInterval, "certificate rotation check interval")
	renewalWindow := flag.Duration("certificate-renewal-window", defaultCertificateRenewalWindow, "certificate renewal window before expiration")
	secretRefreshInterval := flag.Duration("certificate-secret-refresh-interval", defaultCertificateSecretRefreshInterval, "serving TLS Secret refresh interval for all replicas")
	flag.Parse()

	sandboxImage, err := containerdUtils.GetSandboxImage()
	if err != nil {
		log.Fatal(err)
	}
	mutator := admission.NewMutator(admission.Config{SandboxImage: sandboxImage})
	server := admission.NewServer(mutator)
	log.Printf("starting sysbox admission on %s sandboxImage=%s", *addr, sandboxImage)
	if *bootstrapWebhook {
		ctx := context.Background()
		lifecycleConfig := admission.LifecycleConfig{
			Namespace:     *namespace,
			ServiceName:   *serviceName,
			ServicePort:   int32(*servicePort),
			CASecretName:  *caSecretName,
			TLSSecretName: *tlsSecretName,
			LeaseName:     *leaseName,
			WebhookName:   *webhookName,
			RenewalWindow: *renewalWindow,
		}
		bootstrapRuntime, err := bootstrapTLS(lifecycleConfig)
		if err != nil {
			log.Fatal(err)
		}
		identity, err := leaderIdentity(*leaderIdentityOverride)
		if err != nil {
			log.Fatal(err)
		}
		startCertificateRotationLeaderElection(ctx, certificateRotationLeaderRuntime{
			client:   bootstrapRuntime.client,
			config:   lifecycleConfig,
			identity: identity,
			interval: *rotationInterval,
			manager:  bootstrapRuntime.manager,
			reloader: bootstrapRuntime.reloader,
		})
		refreshConfig := admission.TLSCertificateRefreshConfig{
			Client:    bootstrapRuntime.client,
			Namespace: lifecycleConfig.Namespace,
			Secret:    lifecycleConfig.TLSSecretName,
			Interval:  *secretRefreshInterval,
			Reloader:  bootstrapRuntime.reloader,
			ErrorHandler: func(err error) {
				log.Printf("refresh tls certificate from secret failed: %v", err)
			},
		}
		if err := admission.WaitForTLSCertificateFromSecret(ctx, refreshConfig); err != nil {
			log.Fatal(err)
		}
		go func() {
			if err := admission.RunTLSCertificateRefreshLoop(ctx, refreshConfig); err != nil {
				log.Printf("certificate secret refresh loop failed: %v", err)
			}
		}()
		httpServer := &http.Server{
			Addr:      *addr,
			Handler:   server,
			TLSConfig: dynamicTLSConfig(bootstrapRuntime.reloader),
		}
		log.Fatal(httpServer.ListenAndServeTLS("", ""))
	}
	if *tlsCert != "" && *tlsKey != "" {
		log.Fatal(http.ListenAndServeTLS(*addr, *tlsCert, *tlsKey, server))
	}
	log.Fatal(http.ListenAndServe(*addr, server))
}

func defaultNamespace() string {
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "sysbox-system"
}

func leaderIdentity(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return os.Hostname()
}

func dynamicTLSConfig(reloader *admission.CertificateReloader) *tls.Config {
	return &tls.Config{GetCertificate: reloader.GetCertificate}
}

func bootstrapTLS(config admission.LifecycleConfig) (bootstrapTLSRuntime, error) {
	client, err := inClusterClient()
	if err != nil {
		return bootstrapTLSRuntime{}, err
	}
	return bootstrapTLSWithClient(client, config), nil
}

func bootstrapTLSWithClient(client kubernetes.Interface, config admission.LifecycleConfig) bootstrapTLSRuntime {
	manager := admission.NewLifecycleManager(client, config)
	return bootstrapTLSRuntime{
		client:   client,
		manager:  manager,
		reloader: admission.NewEmptyCertificateReloader(),
	}
}

func inClusterClient() (kubernetes.Interface, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	return client, nil
}

type certificateRotationLeaderRuntime struct {
	client   kubernetes.Interface
	config   admission.LifecycleConfig
	identity string
	interval time.Duration
	manager  *admission.LifecycleManager
	reloader *admission.CertificateReloader
}

func startCertificateRotationLeaderElection(ctx context.Context, runtime certificateRotationLeaderRuntime) {
	lock := admission.NewLeaseLock(runtime.client, runtime.config, runtime.identity)
	go func() {
		err := admission.RunLeaderElection(ctx, lock, admission.DefaultLeaderElectionConfig(), leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				loop := admission.NewCertificateRotationLoop(runtime.manager, runtime.reloader, runtime.interval)
				loop.SetErrorHandler(func(err error) {
					log.Printf("certificate rotation reconcile failed: %v", err)
				})
				if err := loop.Run(ctx); err != nil {
					log.Printf("certificate rotation loop failed: %v", err)
				}
			},
		})
		if err != nil {
			log.Printf("certificate rotation leader election failed: %v", err)
		}
	}()
}
