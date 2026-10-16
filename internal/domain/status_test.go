package domain

import "testing"

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from TaskStatus
		to   TaskStatus
		Want bool
	}{
		{TaskStatusPending, TaskStatusPending, true},
		{TaskStatusPending, TaskStatusCancelled, true},
		{TaskStatusPending, TaskStatusSuccess, false},
		{TaskStatusRunning, TaskStatusSuccess, true},
		{TaskStatusRunning, TaskStatusPending, true},
		{TaskStatusRunning, TaskStatusFailed, true},
		{TaskStatusRunning, TaskStatusCancelled, true},
		{TaskStatusSuccess, TaskStatusRunning, false},
		{TaskStatusFailed, TaskStatusRunning, false},
		{TaskStatusCancelled, TaskStatusRunning, false},
	}
	for _, tt := range tests {
		got := CanTransition(tt.from, tt.to)
		if got != tt.Want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.Want)
		}
	}
}

func TestIsTerminated(t *testing.T) {
	tests := []struct {
		status TaskStatus
		want   bool
	}{
		{TaskStatusPending, false},
		{TaskStatusRunning, false},
		{TaskStatusSuccess, true},
		{TaskStatusFailed, true},
		{TaskStatusCancelled, true},
	}
	for _, tt := range tests {
		got := IsTerminal(tt.status)
		if got != tt.want {
			t.Errorf("IsTerminal(%s) = %v, want %v", tt.status, got, tt.want)
		}
	}
}
func TestTaskTransitionTo(t *testing.T) {
	task := &Task{Status: TaskStatusPending}
	if err := task.TransitionTo(TaskStatusRunning); err != nil {
		t.Fatalf("task.TransitionTo() expected nil error, got: %v", err)
	}
	if task.Status != TaskStatusRunning {
		t.Errorf("task.TransitionTo() expected running got %s", task.Status)
	}
	if err := task.TransitionTo(TaskStatusPending); err != nil {
		t.Fatalf("task.TransitionTo() expected nil error ,got: %v", err)
	}
	if err := task.TransitionTo(TaskStatusSuccess); err == nil {
		t.Error("task.TransitionTo() expected error for pending -> success")
	}
}
