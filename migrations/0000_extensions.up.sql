-- Bootstrap the extensions the schema depends on. pgcrypto provides
-- gen_random_uuid(); citext provides the case-insensitive tag name type. Both
-- are "trusted" extensions (PostgreSQL 13+), so a database owner can create
-- them without superuser. IF NOT EXISTS makes this a no-op where they already
-- exist (e.g. installs that pre-created them). If your role lacks privilege to
-- create extensions, a superuser must run these once before `loose-ends migrate`.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
