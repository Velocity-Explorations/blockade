package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr string          `yaml:"listen_addr"`
	Lnd        *LndConfig      `yaml:"lnd"`
	Bitcoind   *BitcoindConfig `yaml:"bitcoind"`
	Routes     []RouteConfig   `yaml:"routes"`
}

type LndConfig struct {
	Host         string `yaml:"host"`
	TLSCertPath  string `yaml:"tls_cert_path"`
	MacaroonPath string `yaml:"macaroon_path"`
}

type BitcoindConfig struct {
	Host    string `yaml:"host"`
	RPCUser string `yaml:"rpc_user"`
	RPCPass string `yaml:"rpc_password"`
}

type RouteConfig struct {
	PathPrefix string `yaml:"path_prefix"`
	Upstream   string `yaml:"upstream"`
	PriceSats  int64  `yaml:"price_sats"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.ListenAddr == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if cfg.Lnd == nil && cfg.Bitcoind == nil {
		return fmt.Errorf("either lnd or bitcoind backend must be configured")
	}
	if cfg.Lnd != nil && cfg.Bitcoind != nil {
		return fmt.Errorf("only one of lnd or bitcoind may be configured")
	}
	if cfg.Lnd != nil {
		if cfg.Lnd.Host == "" {
			return fmt.Errorf("lnd.host is required")
		}
		if cfg.Lnd.TLSCertPath == "" {
			return fmt.Errorf("lnd.tls_cert_path is required")
		}
		if cfg.Lnd.MacaroonPath == "" {
			return fmt.Errorf("lnd.macaroon_path is required")
		}
	}
	if cfg.Bitcoind != nil {
		if cfg.Bitcoind.Host == "" {
			return fmt.Errorf("bitcoind.host is required")
		}
		if cfg.Bitcoind.RPCUser == "" {
			return fmt.Errorf("bitcoind.rpc_user is required")
		}
		if cfg.Bitcoind.RPCPass == "" {
			return fmt.Errorf("bitcoind.rpc_password is required")
		}
	}
	if len(cfg.Routes) == 0 {
		return fmt.Errorf("at least one route is required")
	}
	for i, r := range cfg.Routes {
		if r.PathPrefix == "" {
			return fmt.Errorf("routes[%d].path_prefix is required", i)
		}
		if r.Upstream == "" {
			return fmt.Errorf("routes[%d].upstream is required", i)
		}
		if r.PriceSats <= 0 {
			return fmt.Errorf("routes[%d].price_sats must be > 0", i)
		}
	}
	return nil
}
