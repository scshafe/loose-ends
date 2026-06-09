CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id UUID REFERENCES tasks(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    body TEXT,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'done', 'archived', 'duplicate', 'partially_complete')),
    sort_key DOUBLE PRECISION NOT NULL DEFAULT 0,
    duplicate_of_id UUID REFERENCES tasks(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CHECK (id <> parent_id),
    CHECK (status = 'duplicate' OR duplicate_of_id IS NULL)
);

CREATE TABLE tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name CITEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE task_tags (
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, tag_id)
);

CREATE INDEX tasks_parent_id_sort_key_idx ON tasks (parent_id, sort_key);
CREATE INDEX tasks_status_idx ON tasks (status);
CREATE INDEX tasks_duplicate_of_id_idx ON tasks (duplicate_of_id);
CREATE INDEX task_tags_tag_id_idx ON task_tags (tag_id);
