package domain

import "time"

type TaskLease struct {
	TaskID     TaskID
	AgentID    string
	Attempt    int
	LeaseEpoch int64
	ExpiresAt  time.Time
	RenewedAt  time.Time
}
