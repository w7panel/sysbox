package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/nestybox/sysbox-admission/admission"
	containerdUtils "github.com/nestybox/sysbox-libs/containerdUtils"
	admissionregistrationv1client "k8s.io/client-go/kubernetes/typed/admissionregistration/v1"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
)

const defaultCertificateRotationInterval = 6 * time.Hour
const defaultCertificateRenewalWindow = 90 * 24 * time.Hour
const defaultCertificateSecretRefreshInterval = time.Minute

var (
	errIncompleteTLSFiles   = errors.New("tls-cert and tls-key must be configured together")
	errInsecureHTTPDisabled = errors.New("plaintext HTTP requires --allow-insecure-http")
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
)

type bootstrapTLSRuntime struct {
	clients  admission.LifecycleClients
	manager  *admission.LifecycleManager
	reloader *admission.CertificateReloader
}

type serveConfig struct {
	bootstrapWebhook  bool
	tlsCert           string
	tlsKey            string
	allowInsecureHTTP bool
}

func main() {
	addr := flag.String("addr", ":9443", "https listen address")
	tlsCert := flag.String("tls-cert", "", "tls certificate path")
	tlsKey := flag.String("tls-key", "", "tls key path")
	bootstrapWebhook := flag.Bool("bootstrap-webhook", false, "bootstrap webhook certificates and Kubernetes webhook configuration")
	allowInsecureHTTP := flag.Bool("allow-insecure-http", false, "allow plaintext HTTP without TLS; development only")
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
	if err := validateServeConfig(serveConfig{
		bootstrapWebhook:  *bootstrapWebhook,
		tlsCert:           *tlsCert,
		tlsKey:            *tlsKey,
		allowInsecureHTTP: *allowInsecureHTTP,
	}); err != nil {
		log.Fatal(err)
	}
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
			leases:   bootstrapRuntime.clients.LeaseClient,
			config:   lifecycleConfig,
			identity: identity,
			interval: *rotationInterval,
			manager:  bootstrapRuntime.manager,
			reloader: bootstrapRuntime.reloader,
		})
		refreshConfig := admission.TLSCertificateRefreshConfig{
			Secrets:  bootstrapRuntime.clients.Secrets,
			Secret:   lifecycleConfig.TLSSecretName,
			Interval: *secretRefreshInterval,
			Reloader: bootstrapRuntime.reloader,
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
		httpServer := newAdmissionHTTPServer(*addr, server, dynamicTLSConfig(bootstrapRuntime.reloader))
		log.Fatal(httpServer.ListenAndServeTLS("", ""))
	}
	if *tlsCert != "" && *tlsKey != "" {
		httpServer := newAdmissionHTTPServer(*addr, server, &tls.Config{MinVersion: tls.VersionTLS12})
		log.Fatal(httpServer.ListenAndServeTLS(*tlsCert, *tlsKey))
	}
	httpServer := newAdmissionHTTPServer(*addr, server, nil)
	log.Fatal(httpServer.ListenAndServe())
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
	return &tls.Config{GetCertificate: reloader.GetCertificate, MinVersion: tls.VersionTLS12}
}

func newAdmissionHTTPServer(addr string, handler http.Handler, tlsConfig *tls.Config) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func validateServeConfig(config serveConfig) error {
	if (config.tlsCert == "") != (config.tlsKey == "") {
		return errIncompleteTLSFiles
	}
	if config.bootstrapWebhook || config.tlsCert != "" || config.allowInsecureHTTP {
		return nil
	}
	return errInsecureHTTPDisabled
}

func bootstrapTLS(config admission.LifecycleConfig) (bootstrapTLSRuntime, error) {
	clients, err := inClusterLifecycleClients(config.Namespace)
	if err != nil {
		return bootstrapTLSRuntime{}, err
	}
	return bootstrapTLSWithClients(clients, config), nil
}

func bootstrapTLSWithClients(clients admission.LifecycleClients, config admission.LifecycleConfig) bootstrapTLSRuntime {
	manager := admission.NewLifecycleManager(clients, config)
	return bootstrapTLSRuntime{
		clients:  clients,
		manager:  manager,
		reloader: admission.NewEmptyCertificateReloader(),
	}
}

func inClusterLifecycleClients(namespace string) (admission.LifecycleClients, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return admission.LifecycleClients{}, err
	}
	coreClient, err := corev1client.NewForConfig(restConfig)
	if err != nil {
		return admission.LifecycleClients{}, err
	}
	coordinationClient, err := coordinationv1client.NewForConfig(restConfig)
	if err != nil {
		return admission.LifecycleClients{}, err
	}
	admissionregistrationClient, err := admissionregistrationv1client.NewForConfig(restConfig)
	if err != nil {
		return admission.LifecycleClients{}, err
	}
	return admission.LifecycleClients{
		Secrets:                       coreClient.Secrets(namespace),
		Leases:                        coordinationClient.Leases(namespace),
		LeaseClient:                   coordinationClient,
		MutatingWebhookConfigurations: admissionregistrationClient.MutatingWebhookConfigurations(),
	}, nil
}

type certificateRotationLeaderRuntime struct {
	leases   coordinationv1client.LeasesGetter
	config   admission.LifecycleConfig
	identity string
	interval time.Duration
	manager  *admission.LifecycleManager
	reloader *admission.CertificateReloader
}

func startCertificateRotationLeaderElection(ctx context.Context, runtime certificateRotationLeaderRuntime) {
	lock := admission.NewLeaseLock(runtime.leases, runtime.config, runtime.identity)
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
