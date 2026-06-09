//go:build integration

// Integration tests that exercise the real SQL against a live Postgres. They are
// excluded from the default `go test ./...` build; run with:
//
//	LOOSE_ENDS_TEST_DSN='postgres://loose_ends@127.0.0.1:5432/le_clean?sslmode=disable' \
//	  go test -tags integration ./internal/storage/
//
// The target database is migrated and TRUNCATEd at the start of each test, so
// use a throwaway database, never production data.
package storage

import (
	"context"
	"os"
	"testing"

	"loose_ends/internal/model"
)

func testRepo(t *testing.T) *Repository {
	t.Helper()
	dsn := os.Getenv("LOOSE_ENDS_TEST_DSN")
	if dsn == "" {
		t.Skip("set LOOSE_ENDS_TEST_DSN to run storage integration tests")
	}
	if err := RunMigrations(dsn, ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(repo.Close)
	if _, err := repo.pool.Exec(context.Background(), `TRUNCATE task_tags, tags, tasks RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return repo
}

// Regression test for the gate review's HIGH finding: deleting a task that is
// the duplicate *target* of another task used to fail the lifecycle CHECK.
func TestIntegrationDeleteDuplicateTarget(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	canonical, err := repo.CreateTask(ctx, CreateTaskParams{Title: "canonical"})
	if err != nil {
		t.Fatal(err)
	}
	dup, err := repo.CreateTask(ctx, CreateTaskParams{Title: "dup"})
	if err != nil {
		t.Fatal(err)
	}
	status := model.TaskStatusDuplicate
	if _, err := repo.UpdateTask(ctx, dup.ID, UpdateTaskParams{Status: &status, DuplicateOfID: &canonical.ID}); err != nil {
		t.Fatalf("mark duplicate: %v", err)
	}

	if err := repo.DeleteTask(ctx, canonical.ID); err != nil {
		t.Fatalf("delete duplicate target should succeed: %v", err)
	}

	got, err := repo.GetTask(ctx, dup.ID)
	if err != nil {
		t.Fatalf("dependent should survive: %v", err)
	}
	if got.Status != model.TaskStatusOpen {
		t.Errorf("dependent status = %q, want open", got.Status)
	}
	if got.DuplicateOfID != nil {
		t.Errorf("dependent duplicate_of_id should be cleared, got %v", *got.DuplicateOfID)
	}
}

func TestIntegrationMoveOrderingAndCycle(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	parent, err := repo.CreateTask(ctx, CreateTaskParams{Title: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := repo.CreateTask(ctx, CreateTaskParams{Title: "child", ParentID: &parent.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Moving a parent under its own descendant must be rejected.
	if _, err := repo.MoveTask(ctx, parent.ID, MoveTaskParams{ParentSet: true, ParentID: &child.ID}); err == nil {
		t.Errorf("expected cycle error moving parent under its child")
	}

	// Fractional sort keys: a sibling moved before another lands between it and
	// the previous sibling.
	s1, err := repo.CreateTask(ctx, CreateTaskParams{Title: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := repo.CreateTask(ctx, CreateTaskParams{Title: "s2"})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := repo.MoveTask(ctx, s2.ID, MoveTaskParams{BeforeID: &s1.ID})
	if err != nil {
		t.Fatalf("move before: %v", err)
	}
	if moved.SortKey >= s1.SortKey {
		t.Errorf("moved sort_key %v should be < s1 sort_key %v", moved.SortKey, s1.SortKey)
	}
}

func TestIntegrationLifecycleCompletedAt(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	task, err := repo.CreateTask(ctx, CreateTaskParams{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	done := model.TaskStatusDone
	got, err := repo.UpdateTask(ctx, task.ID, UpdateTaskParams{Status: &done})
	if err != nil {
		t.Fatal(err)
	}
	if got.CompletedAt == nil {
		t.Errorf("done task should have completed_at set")
	}
	open := model.TaskStatusOpen
	got, err = repo.UpdateTask(ctx, task.ID, UpdateTaskParams{Status: &open})
	if err != nil {
		t.Fatal(err)
	}
	if got.CompletedAt != nil {
		t.Errorf("reopened task should clear completed_at, got %v", *got.CompletedAt)
	}
}
