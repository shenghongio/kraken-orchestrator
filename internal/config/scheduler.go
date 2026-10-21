package config

import (
	"errors"
	"fmt"
	"time"
)

type SchedulerConfig struct {
	Log   LogConfig   `koanf:"log"`
	HTTP  HTTPConfig  `koanf:"http"`
	DB    DBConfig    `koanf:"db"`
	Redis RedisConfig `koanf:"redis"`

	LeaseTTL            time.Duration `koanf:"lease.ttl"`
	LeaseReaperInterval time.Duration `koanf:"lease.reaper_interval"`
	HeartbeatInterval   time.Duration `koanf:"heartbeat.interval"`
}

func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
		HTTP: HTTPConfig{
			Addr:         ":8080",
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
		},
		DB: DBConfig{
			MaxOpenConns:    25,
			MaxIdleConns:    5,
			ConnMaxLifetime: 10 * time.Minute,
		},
		LeaseTTL:            60 * time.Second,
		LeaseReaperInterval: 5 * time.Second,
		HeartbeatInterval:   15 * time.Second,
	}
}
func LoadSchedulerConfig(configFile string) (*SchedulerConfig, error) {
	lc, err := Load(configFile)
	if err != nil {
		return nil, err
	}
	cfg := DefaultSchedulerConfig()
	if err := lc.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal scheduler config: %w", err)
	}
	if cfg.DB.DSN == "" {
		return nil, errors.New("db.dsn is required")
	}
	return &cfg, nil
}