package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loose_ends/internal/model"
)

var ErrTagNotFound = errors.New("tag not found")

const tagColumns = `id, name, created_at`

func (r *Repository) AttachTaskTag(ctx context.Context, taskID uuid.UUID, name string) (*model.Tag, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("repository is not open")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("tag name is required")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("attach task tag: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := getTaskTx(ctx, tx, taskID); err != nil {
		return nil, err
	}

	row := tx.QueryRow(ctx, `
		WITH tag AS (
			INSERT INTO tags (name)
			VALUES ($1)
			ON CONFLICT (name) DO UPDATE SET name = tags.name
			RETURNING `+tagColumns+`
		), attached AS (
			INSERT INTO task_tags (task_id, tag_id)
			SELECT $2, id FROM tag
			ON CONFLICT DO NOTHING
		)
		SELECT `+tagColumns+` FROM tag
	`, name, taskID)
	tag, err := scanTag(row)
	if err != nil {
		return nil, fmt.Errorf("attach task tag: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("attach task tag: %w", err)
	}
	return tag, nil
}

func (r *Repository) DetachTaskTag(ctx context.Context, taskID uuid.UUID, name string) error {
	if r == nil || r.pool == nil {
		return errors.New("repository is not open")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("tag name is required")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("detach task tag: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := getTaskTx(ctx, tx, taskID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM task_tags
		USING tags
		WHERE task_tags.tag_id = tags.id
			AND task_tags.task_id = $1
			AND tags.name = $2
	`, taskID, name); err != nil {
		return fmt.Errorf("detach task tag: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("detach task tag: %w", err)
	}
	return nil
}

func (r *Repository) ListTags(ctx context.Context) ([]model.TagWithCount, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("repository is not open")
	}

	rows, err := r.pool.Query(ctx, `
		SELECT tags.id, tags.name, tags.created_at, count(task_tags.task_id) AS task_count
		FROM tags
		LEFT JOIN task_tags ON task_tags.tag_id = tags.id
		GROUP BY tags.id, tags.name, tags.created_at
		ORDER BY lower(tags.name), tags.name
	`)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()

	tags := make([]model.TagWithCount, 0)
	for rows.Next() {
		var tag model.TagWithCount
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.CreatedAt, &tag.TaskCount); err != nil {
			return nil, fmt.Errorf("list tags: %w", err)
		}
		tag.CreatedAt = tag.CreatedAt.UTC().Round(0)
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	return tags, nil
}

func (r *Repository) DeleteTag(ctx context.Context, id uuid.UUID) error {
	if r == nil || r.pool == nil {
		return errors.New("repository is not open")
	}

	tag, err := r.pool.Exec(ctx, `DELETE FROM tags WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTagNotFound
	}
	return nil
}

func scanTag(row rowScanner) (*model.Tag, error) {
	var tag model.Tag
	if err := row.Scan(&tag.ID, &tag.Name, &tag.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTagNotFound
	} else if err != nil {
		return nil, err
	}
	tag.CreatedAt = tag.CreatedAt.UTC().Round(0)
	return &tag, nil
}
