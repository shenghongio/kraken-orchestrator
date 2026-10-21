package config

import (
	"errors"
	"fmt"
)

type CliConfig struct {
	SchedulerURL string    `koanf:"scheduler.url"`
	Log          LogConfig `koanf:"log"`
}

func DefaultCliConfig() CliConfig {
	return CliConfig{
		SchedulerURL: "http://localhost:8080",
		Log: LogConfig{
			Level:  "warn",
			Format: "text",
		},
	}
}

func LoadCliConfig(configFile string) (*CliConfig, error) {
	k, err := Load(configFile)
	if err != nil {
		return nil, err
	}
	defaultCliConfig := DefaultCliConfig()
	if err := k.Unmarshal("", &defaultCliConfig); err != nil {
		return nil, fmt.Errorf("unmarshal cli config: %w", err)
	}
	if defaultCliConfig.SchedulerURL == "" {
		return nil, errors.New("scheduler url is required")
	}
	return &defaultCliConfig, nil
}