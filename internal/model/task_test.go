package model

import "testing"

func TestTaskStatusValid(t *testing.T) {
	valid := []TaskStatus{
		TaskStatusOpen,
		TaskStatusDone,
		TaskStatusArchived,
		TaskStatusDuplicate,
		TaskStatusPartiallyComplete,
	}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("expected status %q to be valid", s)
		}
	}

	invalid := []TaskStatus{"", "in_progress", "Open", "DONE", "pending", "complete", "partial"}
	for _, s := range invalid {
		if s.Valid() {
			t.Errorf("expected status %q to be invalid", s)
		}
	}
}
