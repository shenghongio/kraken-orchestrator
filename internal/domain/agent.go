package domain

import "time"

// AgentStatus 表示Agent的运行状态
type AgentStatus string

const (
	AgentStatusOffline  AgentStatus = "offline"
	AgentStatusOnline   AgentStatus = "online"
	AgentStatusDraining AgentStatus = "draining"
)

// Agent 是执行任务的工作节点
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
