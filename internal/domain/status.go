package domain

import "fmt"

var allowedTransitions = map[TaskStatus]map[TaskStatus]bool{
	TaskStatusPending: {
		TaskStatusRunning:   true,
		TaskStatusCancelled: true,
	},
	TaskStatusRunning: {
		TaskStatusSuccess:   true,
		TaskStatusPending:   true,
		TaskStatusFailed:    true,
		TaskStatusCancelled: true,
	},
}

func CanTransition(from, to TaskStatus) bool {
	if from == to {
		return true
	}
	if targets, ok := allowedTransitions[from]; ok {
		return targets[to]
	}
	return false
}
func (t *Task) TransitionTo(to TaskStatus) error {
	if !CanTransition(t.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidStatusTransition, t.Status, to)
	}
	t.Status = to
	return nil
}
func IsTerminal(s TaskStatus) bool {
	switch s {
	case TaskStatusSuccess, TaskStatusFailed, TaskStatusCancelled:
		return true
	}
	return false
}
