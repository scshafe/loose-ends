package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"loose_ends/internal/model"
	"loose_ends/internal/storage"
)

const DefaultVersion = "dev"

// TaskStore is the persistence surface the HTTP handlers depend on. It is
// satisfied by *storage.Repository in production and by fakes in tests, so the
// handlers can be exercised without a live database.
type TaskStore interface {
	CreateTask(ctx context.Context, params storage.CreateTaskParams) (*model.Task, error)
	ListTasks(ctx context.Context, params storage.ListTaskParams) ([]model.Task, error)
	ListTaskTree(ctx context.Context, rootID *uuid.UUID, params storage.ListTaskParams) ([]model.Task, error)
	GetTask(ctx context.Context, id uuid.UUID) (*model.Task, error)
	ResolveTask(ctx context.Context, ref string) (*model.Task, error)
	ResolveTaskID(ctx context.Context, ref string) (uuid.UUID, error)
	UpdateTask(ctx context.Context, id uuid.UUID, params storage.UpdateTaskParams) (*model.Task, error)
	DeleteTask(ctx context.Context, id uuid.UUID) error
	MoveTask(ctx context.Context, id uuid.UUID, params storage.MoveTaskParams) (*model.Task, error)
	RebalanceTasks(ctx context.Context, parentID *uuid.UUID) (int64, error)
	AttachTaskTag(ctx context.Context, taskID uuid.UUID, name string) (*model.Tag, error)
	DetachTaskTag(ctx context.Context, taskID uuid.UUID, name string) error
	ListTags(ctx context.Context) ([]model.TagWithCount, error)
	DeleteTag(ctx context.Context, id uuid.UUID) error
}

type Config struct {
	ListenAddr string
	Version    string
	Store      TaskStore
	AuthToken  string
	// TailnetListener, when non-nil, serves the same handler on the tailnet in
	// addition to the loopback ListenAddr.
	TailnetListener net.Listener
}

func NewHandler(version string, store TaskStore, authToken string) http.Handler {
	if version == "" {
		version = DefaultVersion
	}

	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": version})
	})
	if store != nil {
		tasks := taskHandlers{store: store}
		r.Group(func(r chi.Router) {
			if strings.TrimSpace(authToken) != "" {
				r.Use(requireBearer(authToken))
			}
			r.Post("/tasks", tasks.create)
			r.Get("/tasks", tasks.list)
			r.Get("/tasks/tree", tasks.tree)
			r.Get("/tags", tasks.listTags)
			r.Delete("/tags/{id}", tasks.deleteTag)
			r.Get("/tasks/{id}", tasks.get)
			r.Patch("/tasks/{id}", tasks.update)
			r.Delete("/tasks/{id}", tasks.delete)
			r.Post("/tasks/{id}/move", tasks.move)
			r.Post("/tasks/{id}/tags", tasks.attachTag)
			r.Delete("/tasks/{id}/tags/{tagName}", tasks.detachTag)
			r.Post("/admin/rebalance", tasks.rebalance)
		})
	}
	return r
}

// requireBearer rejects requests that do not carry the configured bearer token.
// /healthz and /version are intentionally left open for liveness checks.
func requireBearer(token string) func(http.Handler) http.Handler {
	expected := []byte("Bearer " + token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := []byte(r.Header.Get("Authorization"))
			if subtle.ConstantTimeCompare(got, expected) != 1 {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func Run(ctx context.Context, cfg Config) error {
	if cfg.ListenAddr == "" {
		return errors.New("listen address is required")
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           NewHandler(cfg.Version, cfg.Store, cfg.AuthToken),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- srv.ListenAndServe()
	}()
	if cfg.TailnetListener != nil {
		go func() {
			errCh <- srv.Serve(cfg.TailnetListener)
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}
		return nil
	case err := <-errCh:
		// One listener failed; close the server so the other listener (e.g. the
		// tailnet one) is not left serving.
		_ = srv.Close()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
