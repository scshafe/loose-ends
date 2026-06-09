package model

import (
	"time"

	"github.com/google/uuid"
)

type TaskStatus string

const (
	TaskStatusOpen              TaskStatus = "open"
	TaskStatusDone              TaskStatus = "done"
	TaskStatusArchived          TaskStatus = "archived"
	TaskStatusDuplicate         TaskStatus = "duplicate"
	TaskStatusPartiallyComplete TaskStatus = "partially_complete"
)

func (s TaskStatus) Valid() bool {
	switch s {
	case TaskStatusOpen, TaskStatusDone, TaskStatusArchived, TaskStatusDuplicate, TaskStatusPartiallyComplete:
		return true
	default:
		return false
	}
}

type Task struct {
	ID            uuid.UUID  `json:"id"`
	ParentID      *uuid.UUID `json:"parent_id,omitempty"`
	Title         string     `json:"title"`
	Body          *string    `json:"body,omitempty"`
	Status        TaskStatus `json:"status"`
	SortKey       float64    `json:"sort_key"`
	DuplicateOfID *uuid.UUID `json:"duplicate_of_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type Tag struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type TagWithCount struct {
	Tag
	TaskCount int64 `json:"task_count"`
}
