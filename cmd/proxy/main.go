package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/TheFutonEng/btc-paywall/internal/config"
	"github.com/TheFutonEng/btc-paywall/internal/payment"
	"github.com/TheFutonEng/btc-paywall/internal/payment/lightning"
	"github.com/TheFutonEng/btc-paywall/internal/payment/onchain"
	"github.com/TheFutonEng/btc-paywall/internal/proxy"
	"github.com/TheFutonEng/btc-paywall/internal/store"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Open the anti-replay and pending-address stores. If db_path is set, state
	// survives proxy restarts. Otherwise in-memory stores are used.
	// Not deferred — the process lifetime equals the server lifetime; SQLite
	// flushes are per-INSERT so no data is lost on exit.
	var st store.Store
	var ps store.PendingStore
	var sqliteStore *store.SQLiteStore
	if cfg.DBPath != "" {
		s, err := store.OpenSQLite(cfg.DBPath)
		if err != nil {
			log.Fatalf("open store: %v", err)
		}
		sqliteStore = s
		st = s
		ps = s
		log.Printf("btc-paywall token store: %s", cfg.DBPath)
	} else {
		m := store.NewMemStore()
		st = m
		ps = m
	}

	var verifier payment.PaymentVerifier

	switch {
	case cfg.Lnd != nil:
		lndClient, err := lightning.NewClient(cfg.Lnd.Host, cfg.Lnd.TLSCertPath, cfg.Lnd.MacaroonPath)
		if err != nil {
			log.Fatalf("connect to lnd: %v", err)
		}
		lnVerifier, err := lightning.NewVerifier(lndClient, st)
		if err != nil {
			_ = lndClient.Close()
			log.Fatalf("create lightning verifier: %v", err)
		}
		verifier = lnVerifier

	case cfg.Bitcoind != nil:
		btcClient := onchain.NewClient(cfg.Bitcoind.Host, cfg.Bitcoind.RPCUser, cfg.Bitcoind.RPCPass)
		verifier = onchain.NewVerifier(btcClient, cfg.Bitcoind.MinConfirmations, st, ps)
	}

	handler, err := proxy.New(cfg.Routes, verifier, cfg.RateLimit)
	if err != nil {
		log.Fatalf("create proxy: %v", err)
	}

	if cfg.Curve != nil {
		principals := store.NewMemPrincipalStore()
		handler.EnableCurve(cfg.Curve, principals)
		log.Printf("cost curve enabled: floor=%d slope=%d stake=%d cap=%d",
			cfg.Curve.FloorTollSats, cfg.Curve.EscalationSlope,
			cfg.Curve.EnrollmentStakeSats, cfg.Curve.AnonymousCap)
	}

	log.Printf("btc-paywall listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, handler); err != nil {
		if sqliteStore != nil {
			_ = sqliteStore.Close()
		}
		log.Fatalf("serve: %v", err)
	}
}
