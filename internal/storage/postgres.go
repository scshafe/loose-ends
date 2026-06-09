package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"loose_ends/migrations"
)

type Repository struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Repository, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("database dsn is required")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database dsn: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "loose-ends"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}
	return &Repository{pool: pool}, nil
}

func (r *Repository) Close() {
	if r != nil && r.pool != nil {
		r.pool.Close()
	}
}

func (r *Repository) Ping(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return errors.New("repository is not open")
	}
	return r.pool.Ping(ctx)
}

func (r *Repository) Pool() *pgxpool.Pool {
	if r == nil {
		return nil
	}
	return r.pool
}

// RunMigrations applies all pending migrations. With an empty migrationsDir it
// uses the migrations embedded in the binary (the default, so the single binary
// is self-bootstrapping); a non-empty migrationsDir reads from that directory on
// disk instead (useful during development).
func RunMigrations(dsn string, migrationsDir string) error {
	if strings.TrimSpace(dsn) == "" {
		return errors.New("database dsn is required")
	}

	var (
		m   *migrate.Migrate
		err error
	)
	if strings.TrimSpace(migrationsDir) == "" {
		src, srcErr := iofs.New(migrations.FS, ".")
		if srcErr != nil {
			return fmt.Errorf("load embedded migrations: %w", srcErr)
		}
		m, err = migrate.NewWithSourceInstance("iofs", src, migrateDSN(dsn))
	} else {
		absDir, absErr := filepath.Abs(migrationsDir)
		if absErr != nil {
			return fmt.Errorf("resolve migrations directory: %w", absErr)
		}
		m, err = migrate.New("file://"+filepath.ToSlash(absDir), migrateDSN(dsn))
	}
	if err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

func migrateDSN(dsn string) string {
	switch {
	case strings.HasPrefix(dsn, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(dsn, "postgres://")
	case strings.HasPrefix(dsn, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(dsn, "postgresql://")
	default:
		return dsn
	}
}
