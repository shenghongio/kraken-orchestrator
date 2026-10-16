package domain

import "time"

type TaskID string
type TaskType string

const (
	TaskTypeDownload TaskType = "download"
	TaskTypeRclone   TaskType = "rclone"
	TaskTypeLog      TaskType = "log"
	TaskTypeAI       TaskType = "ai"
)

type TaskPriority int

const (
	PriorityLow    TaskPriority = 0
	PriorityNormal TaskPriority = 5
	PriorityHigh   TaskPriority = 10
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusSuccess   TaskStatus = "success"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

type AttemptStatus string

const (
	AttemptStatusRunning   AttemptStatus = "running"
	AttemptStatusSuccess   AttemptStatus = "success"
	AttemptStatusFailed    AttemptStatus = "failed"
	AttemptStatusCancelled AttemptStatus = "cancelled"
)

type Task struct {
	ID             TaskID
	Type           TaskType
	Priority       TaskPriority
	Status         TaskStatus
	Payload        []byte
	IdempotencyKey string
	
	MaxRetries int
	Attempt    int
	
	CurrentAgentID string
	LeaseEpoch     int64
	
	CancelRequested bool
	
	Result []byte
	Error  string
	
	NextRunAt time.Time
	
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int
}
type TaskAttempt struct {
	ID         uint64
	TaskID     TaskID
	AgentID    string
	Attempt    int
	LeaseEpoch int64
	Status     AttemptStatus
	Progress   []byte
	Result     []byte
	Error      string
	StartedAt  time.Time
	EndedAt    *time.Time
}
