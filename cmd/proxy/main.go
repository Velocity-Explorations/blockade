package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/TheFutonEng/btc-paywall/internal/config"
	"github.com/TheFutonEng/btc-paywall/internal/payment/lightning"
	"github.com/TheFutonEng/btc-paywall/internal/proxy"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	lndClient, err := lightning.NewClient(cfg.Lnd.Host, cfg.Lnd.TLSCertPath, cfg.Lnd.MacaroonPath)
	if err != nil {
		log.Fatalf("connect to lnd: %v", err)
	}
	defer lndClient.Close()

	verifier, err := lightning.NewVerifier(lndClient)
	if err != nil {
		log.Fatalf("create verifier: %v", err)
	}

	handler, err := proxy.New(cfg.Routes, verifier)
	if err != nil {
		log.Fatalf("create proxy: %v", err)
	}

	log.Printf("btc-paywall listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, handler); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
