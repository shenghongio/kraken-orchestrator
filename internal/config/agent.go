package config

import (
	"fmt"
	"time"
)

type AgentConfig struct {
	Log  LogConfig  `koanf:"log"`
	HTTP HTTPConfig `koanf:"http"`

	SchedulerURL   string   `koanf:"scheduler.url"`
	AgentID        string   `koanf:"agent.id"`
	Capabilities   []string `koanf:"agent.capabilities"`
	MaxConcurrency int      `koanf:"agent.max_concurrency"`
	WorkDir        string   `koanf:"agent.work_dir"`
	SQLitePath     string   `koanf:"agent.sqlite_path"`

	HeartbeatInterval time.Duration `koanf:"heartbeat.interval"`
	ClaimInterval     time.Duration `koanf:"claim.interval"`
}

func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		HTTP: HTTPConfig{
			Addr: ":8081",
		},
		MaxConcurrency:    4,
		HeartbeatInterval: 15 * time.Second,
		ClaimInterval:     5 * time.Second,
		WorkDir:           "/var/lib/kraken-agent/work",
		SQLitePath:        "/var/lib/kraken-agent/agent.db",
	}
}

func LoadAgentConfig(configFile string) (*AgentConfig, error) {
	k, err := Load(configFile)
	if err != nil {
		return nil, err
	}
	agentConfig := DefaultAgentConfig()
	if err := k.Unmarshal("", &agentConfig); err != nil {
		return nil, fmt.Errorf("unmarshal agent config: %w", err)
	}
	if agentConfig.SchedulerURL == "" {
		return nil, fmt.Errorf("scheduler url is required")
	}
	if agentConfig.AgentID == "" {
		return nil, fmt.Errorf("agent id is required")
	}
	return &agentConfig, nil
}