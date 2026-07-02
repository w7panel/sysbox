package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/nestybox/sysbox-admission/admission"
	containerdUtils "github.com/nestybox/sysbox-libs/containerdUtils"
)

func main() {
	addr := flag.String("addr", ":9443", "https listen address")
	tlsCert := flag.String("tls-cert", "", "tls certificate path")
	tlsKey := flag.String("tls-key", "", "tls key path")
	flag.Parse()

	sandboxImage, err := containerdUtils.GetSandboxImage()
	if err != nil {
		log.Fatal(err)
	}
	mutator := admission.NewMutator(admission.Config{SandboxImage: sandboxImage})
	server := admission.NewServer(mutator)
	log.Printf("starting sysbox admission on %s sandboxImage=%s", *addr, sandboxImage)
	if *tlsCert != "" && *tlsKey != "" {
		log.Fatal(http.ListenAndServeTLS(*addr, *tlsCert, *tlsKey, server))
	}
	log.Fatal(http.ListenAndServe(*addr, server))
}
