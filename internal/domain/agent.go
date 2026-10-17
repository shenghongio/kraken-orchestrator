package domain

import "time"

type AgentStatus string

const (
	AgentStatusOffline  AgentStatus = "offline"
	AgentStatusOnline   AgentStatus = "online"
	AgentStatusDraining AgentStatus = "draining"
)

type Agent struct {
	ID             string
	Status         AgentStatus
	Version        string
	Capabilities   []string
	MaxConcurrency int
	BusySlots      int
	LastHeartbeat  time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
