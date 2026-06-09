ALTER TABLE tasks
    ADD CONSTRAINT tasks_duplicate_requires_target
    CHECK (status <> 'duplicate' OR duplicate_of_id IS NOT NULL);
