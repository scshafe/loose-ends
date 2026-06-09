package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"loose_ends/internal/model"
)

var (
	ErrTaskNotFound            = errors.New("task not found")
	ErrAmbiguousTask           = errors.New("task id prefix is ambiguous")
	ErrInvalidTaskStatus       = errors.New("invalid task status")
	ErrDuplicateTargetRequired = errors.New("duplicate_of_id is required for duplicate tasks")
	ErrTaskCannotDuplicateSelf = errors.New("task cannot be duplicate of itself")
	ErrTaskMoveInvalid         = errors.New("invalid task move")
	ErrTaskMoveCycle           = errors.New("task cannot be moved under itself or a descendant")
)

type CreateTaskParams struct {
	Title    string
	Body     *string
	ParentID *uuid.UUID
}

type ListTaskParams struct {
	Status        *model.TaskStatus
	IncludeClosed bool
	ParentSet     bool
	ParentID      *uuid.UUID
	Tag           string
}

type UpdateTaskParams struct {
	Title         *string
	BodySet       bool
	Body          *string
	Status        *model.TaskStatus
	DuplicateOfID *uuid.UUID
}

type MoveTaskParams struct {
	ParentSet bool
	ParentID  *uuid.UUID
	BeforeID  *uuid.UUID
	AfterID   *uuid.UUID
	Position  string
}

type rowScanner interface {
	Scan(dest ...any) error
}

const taskColumns = `id, parent_id, title, body, status, sort_key, duplicate_of_id, created_at, updated_at, completed_at`

func (r *Repository) CreateTask(ctx context.Context, params CreateTaskParams) (*model.Task, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("repository is not open")
	}

	title := strings.TrimSpace(params.Title)
	if title == "" {
		return nil, errors.New("task title is required")
	}

	if params.ParentID != nil {
		if _, err := r.GetTask(ctx, *params.ParentID); err != nil {
			return nil, err
		}
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO tasks (parent_id, title, body, sort_key)
		VALUES ($1, $2, $3, COALESCE((SELECT max(sort_key) + 1 FROM tasks WHERE parent_id IS NOT DISTINCT FROM $1::uuid), 1))
		RETURNING `+taskColumns,
		params.ParentID,
		title,
		params.Body,
	)
	return scanTask(row)
}

func (r *Repository) ListTasks(ctx context.Context, params ListTaskParams) ([]model.Task, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("repository is not open")
	}
	var status *string
	if params.Status != nil {
		if !params.Status.Valid() {
			return nil, ErrInvalidTaskStatus
		}
		value := string(*params.Status)
		status = &value
	}

	rows, err := r.pool.Query(ctx, `
		SELECT `+taskColumns+`
		FROM tasks
		WHERE (($1::text IS NOT NULL AND status = $1)
				OR ($1::text IS NULL AND ($2::boolean OR status = 'open')))
			AND ($3::boolean = false OR parent_id IS NOT DISTINCT FROM $4::uuid)
			AND ($5::text = '' OR EXISTS (
				SELECT 1
				FROM task_tags
				JOIN tags ON tags.id = task_tags.tag_id
				WHERE task_tags.task_id = tasks.id AND tags.name = $5
			))
		ORDER BY parent_id NULLS FIRST, sort_key, created_at, id
	`, status, params.IncludeClosed, params.ParentSet, params.ParentID, strings.TrimSpace(params.Tag))
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]model.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return tasks, nil
}

func (r *Repository) ListTaskTree(ctx context.Context, rootID *uuid.UUID, params ListTaskParams) ([]model.Task, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("repository is not open")
	}
	var status *string
	if params.Status != nil {
		if !params.Status.Valid() {
			return nil, ErrInvalidTaskStatus
		}
		value := string(*params.Status)
		status = &value
	}

	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT `+taskColumns+`, ARRAY[sort_key] AS sort_path
			FROM tasks
			WHERE (($3::uuid IS NULL AND parent_id IS NULL) OR id = $3::uuid)
			UNION ALL
			SELECT tasks.`+strings.ReplaceAll(taskColumns, ", ", ", tasks.")+`, tree.sort_path || tasks.sort_key
			FROM tasks
			JOIN tree ON tasks.parent_id = tree.id
		)
		SELECT `+taskColumns+`
		FROM tree
		WHERE (($1::text IS NOT NULL AND status = $1)
			OR ($1::text IS NULL AND ($2::boolean OR status = 'open')))
			AND ($4::text = '' OR EXISTS (
				SELECT 1
				FROM task_tags
				JOIN tags ON tags.id = task_tags.tag_id
				WHERE task_tags.task_id = tree.id AND tags.name = $4
			))
		ORDER BY sort_path, created_at, id
	`, status, params.IncludeClosed, rootID, strings.TrimSpace(params.Tag))
	if err != nil {
		return nil, fmt.Errorf("list task tree: %w", err)
	}
	defer rows.Close()

	tasks := make([]model.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list task tree: %w", err)
	}
	return tasks, nil
}

func (r *Repository) MoveTask(ctx context.Context, id uuid.UUID, params MoveTaskParams) (*model.Task, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("repository is not open")
	}
	if params.BeforeID != nil && params.AfterID != nil {
		return nil, ErrTaskMoveInvalid
	}
	if params.Position != "" && (params.BeforeID != nil || params.AfterID != nil) {
		return nil, ErrTaskMoveInvalid
	}
	if params.Position != "" && params.Position != "first" && params.Position != "last" {
		return nil, ErrTaskMoveInvalid
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("move task: %w", err)
	}
	defer tx.Rollback(ctx)

	task, err := getTaskTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	parentID := params.ParentID
	parentSet := params.ParentSet
	if params.BeforeID != nil || params.AfterID != nil {
		targetID := params.BeforeID
		if targetID == nil {
			targetID = params.AfterID
		}
		if *targetID == id {
			return nil, ErrTaskMoveInvalid
		}
		target, err := getTaskTx(ctx, tx, *targetID)
		if err != nil {
			return nil, err
		}
		if !parentSet {
			parentID = target.ParentID
			parentSet = true
		} else if !sameUUIDPtr(parentID, target.ParentID) {
			return nil, ErrTaskMoveInvalid
		}
	}
	if !parentSet {
		parentID = task.ParentID
	}

	if parentID != nil {
		if *parentID == id {
			return nil, ErrTaskMoveCycle
		}
		if _, err := getTaskTx(ctx, tx, *parentID); err != nil {
			return nil, err
		}
		cycle, err := wouldCreateCycle(ctx, tx, id, *parentID)
		if err != nil {
			return nil, err
		}
		if cycle {
			return nil, ErrTaskMoveCycle
		}
	}

	sortKey, err := moveSortKey(ctx, tx, id, parentID, params)
	if err != nil {
		return nil, err
	}

	row := tx.QueryRow(ctx, `
		UPDATE tasks
		SET parent_id = $2, sort_key = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+taskColumns,
		id,
		parentID,
		sortKey,
	)
	moved, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("move task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("move task: %w", err)
	}
	return moved, nil
}

func (r *Repository) RebalanceTasks(ctx context.Context, parentID *uuid.UUID) (int64, error) {
	if r == nil || r.pool == nil {
		return 0, errors.New("repository is not open")
	}
	tag, err := r.pool.Exec(ctx, `
		WITH ordered AS (
			SELECT id, row_number() OVER (ORDER BY sort_key, created_at, id) AS position
			FROM tasks
			WHERE parent_id IS NOT DISTINCT FROM $1::uuid
		)
		UPDATE tasks
		SET sort_key = ordered.position::double precision,
			updated_at = now()
		FROM ordered
		WHERE tasks.id = ordered.id
	`, parentID)
	if err != nil {
		return 0, fmt.Errorf("rebalance tasks: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *Repository) GetTask(ctx context.Context, id uuid.UUID) (*model.Task, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("repository is not open")
	}

	row := r.pool.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = $1`, id)
	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

func (r *Repository) ResolveTaskID(ctx context.Context, ref string) (uuid.UUID, error) {
	if r == nil || r.pool == nil {
		return uuid.Nil, errors.New("repository is not open")
	}

	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return uuid.Nil, ErrTaskNotFound
	}
	if id, err := uuid.Parse(ref); err == nil {
		return id, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id
		FROM tasks
		WHERE left(lower(id::text), length($1)) = $1
		ORDER BY id
		LIMIT 2
	`, ref)
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve task id: %w", err)
	}
	defer rows.Close()

	ids := make([]uuid.UUID, 0, 2)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return uuid.Nil, fmt.Errorf("resolve task id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, fmt.Errorf("resolve task id: %w", err)
	}

	switch len(ids) {
	case 0:
		return uuid.Nil, ErrTaskNotFound
	case 1:
		return ids[0], nil
	default:
		return uuid.Nil, ErrAmbiguousTask
	}
}

func (r *Repository) ResolveTask(ctx context.Context, ref string) (*model.Task, error) {
	id, err := r.ResolveTaskID(ctx, ref)
	if err != nil {
		return nil, err
	}
	return r.GetTask(ctx, id)
}

func (r *Repository) UpdateTask(ctx context.Context, id uuid.UUID, params UpdateTaskParams) (*model.Task, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("repository is not open")
	}
	if params.Title == nil && !params.BodySet && params.Status == nil {
		return nil, errors.New("at least one task field is required")
	}

	title := (*string)(nil)
	if params.Title != nil {
		trimmed := strings.TrimSpace(*params.Title)
		if trimmed == "" {
			return nil, errors.New("task title is required")
		}
		title = &trimmed
	}
	status := (*string)(nil)
	if params.Status != nil {
		if !params.Status.Valid() {
			return nil, ErrInvalidTaskStatus
		}
		if *params.Status == model.TaskStatusDuplicate {
			if params.DuplicateOfID == nil {
				return nil, ErrDuplicateTargetRequired
			}
			if *params.DuplicateOfID == id {
				return nil, ErrTaskCannotDuplicateSelf
			}
		}
		value := string(*params.Status)
		status = &value
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE tasks
		SET
			title = COALESCE($2, title),
			body = CASE WHEN $3 THEN $4 ELSE body END,
			status = COALESCE($5::text, status),
			duplicate_of_id = CASE
				WHEN $5::text = 'duplicate' THEN $6
				WHEN $5::text IS NOT NULL THEN NULL
				ELSE duplicate_of_id
			END,
			completed_at = CASE
				WHEN $5::text IN ('done', 'partially_complete') AND status <> $5::text THEN now()
				WHEN $5::text IN ('open', 'archived', 'duplicate') THEN NULL
				ELSE completed_at
			END,
			updated_at = now()
		WHERE id = $1
		RETURNING `+taskColumns,
		id,
		title,
		params.BodySet,
		params.Body,
		status,
		params.DuplicateOfID,
	)
	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	return task, nil
}

func (r *Repository) DeleteTask(ctx context.Context, id uuid.UUID) error {
	if r == nil || r.pool == nil {
		return errors.New("repository is not open")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	defer tx.Rollback(ctx)

	// Tasks that point at this one as their duplicate target have
	// duplicate_of_id with ON DELETE SET NULL. Deleting the target would null
	// those references while they still carry status='duplicate', violating the
	// lifecycle CHECK (status='duplicate' <-> duplicate_of_id IS NOT NULL) and
	// aborting the delete. Reopen the dependents first so the delete succeeds.
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status = 'open', duplicate_of_id = NULL
		WHERE duplicate_of_id = $1
	`, id); err != nil {
		return fmt.Errorf("delete task: reopen dependent duplicates: %w", err)
	}

	tag, err := tx.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTaskNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

func scanTask(row rowScanner) (*model.Task, error) {
	var task model.Task
	var parentID pgtype.UUID
	var body pgtype.Text
	var duplicateOfID pgtype.UUID
	var completedAt pgtype.Timestamptz

	if err := row.Scan(
		&task.ID,
		&parentID,
		&task.Title,
		&body,
		&task.Status,
		&task.SortKey,
		&duplicateOfID,
		&task.CreatedAt,
		&task.UpdatedAt,
		&completedAt,
	); err != nil {
		return nil, err
	}

	task.ParentID = uuidFromPG(parentID)
	if body.Valid {
		task.Body = &body.String
	}
	task.DuplicateOfID = uuidFromPG(duplicateOfID)
	if completedAt.Valid {
		completed := completedAt.Time.UTC().Round(0)
		task.CompletedAt = &completed
	}
	task.CreatedAt = task.CreatedAt.UTC().Round(0)
	task.UpdatedAt = task.UpdatedAt.UTC().Round(0)
	return &task, nil
}

func getTaskTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*model.Task, error) {
	row := tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = $1`, id)
	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

func wouldCreateCycle(ctx context.Context, tx pgx.Tx, id uuid.UUID, parentID uuid.UUID) (bool, error) {
	var cycle bool
	err := tx.QueryRow(ctx, `
		WITH RECURSIVE ancestors AS (
			SELECT id, parent_id FROM tasks WHERE id = $2
			UNION ALL
			SELECT tasks.id, tasks.parent_id
			FROM tasks
			JOIN ancestors ON tasks.id = ancestors.parent_id
		)
		SELECT EXISTS (SELECT 1 FROM ancestors WHERE id = $1)
	`, id, parentID).Scan(&cycle)
	if err != nil {
		return false, fmt.Errorf("check task cycle: %w", err)
	}
	return cycle, nil
}

func moveSortKey(ctx context.Context, tx pgx.Tx, id uuid.UUID, parentID *uuid.UUID, params MoveTaskParams) (float64, error) {
	if params.BeforeID != nil {
		upper, err := getSortKey(ctx, tx, *params.BeforeID)
		if err != nil {
			return 0, err
		}
		lower, ok, err := siblingSortKey(ctx, tx, id, parentID, "before", upper)
		if err != nil {
			return 0, err
		}
		if !ok {
			return upper - 1, nil
		}
		return (lower + upper) / 2, nil
	}
	if params.AfterID != nil {
		lower, err := getSortKey(ctx, tx, *params.AfterID)
		if err != nil {
			return 0, err
		}
		upper, ok, err := siblingSortKey(ctx, tx, id, parentID, "after", lower)
		if err != nil {
			return 0, err
		}
		if !ok {
			return lower + 1, nil
		}
		return (lower + upper) / 2, nil
	}

	if params.Position == "first" {
		first, ok, err := boundarySortKey(ctx, tx, id, parentID, true)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 1, nil
		}
		return first - 1, nil
	}

	last, ok, err := boundarySortKey(ctx, tx, id, parentID, false)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 1, nil
	}
	return last + 1, nil
}

func getSortKey(ctx context.Context, tx pgx.Tx, id uuid.UUID) (float64, error) {
	var sortKey float64
	if err := tx.QueryRow(ctx, `SELECT sort_key FROM tasks WHERE id = $1`, id).Scan(&sortKey); errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrTaskNotFound
	} else if err != nil {
		return 0, fmt.Errorf("get task sort key: %w", err)
	}
	return sortKey, nil
}

func siblingSortKey(ctx context.Context, tx pgx.Tx, id uuid.UUID, parentID *uuid.UUID, direction string, sortKey float64) (float64, bool, error) {
	operator := "<"
	order := "DESC"
	if direction == "after" {
		operator = ">"
		order = "ASC"
	}
	row := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT sort_key
		FROM tasks
		WHERE id <> $1 AND parent_id IS NOT DISTINCT FROM $2::uuid AND sort_key %s $3
		ORDER BY sort_key %s, created_at %s, id %s
		LIMIT 1
	`, operator, order, order, order), id, parentID, sortKey)
	var value float64
	if err := row.Scan(&value); errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	} else if err != nil {
		return 0, false, fmt.Errorf("get sibling sort key: %w", err)
	}
	return value, true, nil
}

func boundarySortKey(ctx context.Context, tx pgx.Tx, id uuid.UUID, parentID *uuid.UUID, first bool) (float64, bool, error) {
	order := "DESC"
	if first {
		order = "ASC"
	}
	row := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT sort_key
		FROM tasks
		WHERE id <> $1 AND parent_id IS NOT DISTINCT FROM $2::uuid
		ORDER BY sort_key %s, created_at %s, id %s
		LIMIT 1
	`, order, order, order), id, parentID)
	var value float64
	if err := row.Scan(&value); errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	} else if err != nil {
		return 0, false, fmt.Errorf("get boundary sort key: %w", err)
	}
	return value, true, nil
}

func sameUUIDPtr(a *uuid.UUID, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func uuidFromPG(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}
