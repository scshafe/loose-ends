package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loose_ends/internal/model"
	"loose_ends/internal/storage"
)

// fakeStore is an in-memory TaskStore stub for exercising the HTTP handlers
// without a live database.
type fakeStore struct {
	tasks     []model.Task
	tags      []model.TagWithCount
	getErr    error
	deleteErr error
	resolveID uuid.UUID
}

var _ TaskStore = (*fakeStore)(nil)

func (f *fakeStore) CreateTask(_ context.Context, p storage.CreateTaskParams) (*model.Task, error) {
	return &model.Task{ID: uuid.New(), Title: p.Title, Body: p.Body, ParentID: p.ParentID, Status: model.TaskStatusOpen}, nil
}
func (f *fakeStore) ListTasks(_ context.Context, _ storage.ListTaskParams) ([]model.Task, error) {
	return f.tasks, nil
}
func (f *fakeStore) ListTaskTree(_ context.Context, _ *uuid.UUID, _ storage.ListTaskParams) ([]model.Task, error) {
	return f.tasks, nil
}
func (f *fakeStore) GetTask(_ context.Context, id uuid.UUID) (*model.Task, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &model.Task{ID: id, Title: "x", Status: model.TaskStatusOpen}, nil
}
func (f *fakeStore) ResolveTask(_ context.Context, _ string) (*model.Task, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &model.Task{ID: f.id(), Title: "x", Status: model.TaskStatusOpen}, nil
}
func (f *fakeStore) ResolveTaskID(_ context.Context, _ string) (uuid.UUID, error) {
	if f.getErr != nil {
		return uuid.Nil, f.getErr
	}
	return f.id(), nil
}
func (f *fakeStore) UpdateTask(_ context.Context, id uuid.UUID, _ storage.UpdateTaskParams) (*model.Task, error) {
	return &model.Task{ID: id, Title: "x", Status: model.TaskStatusOpen}, nil
}
func (f *fakeStore) DeleteTask(_ context.Context, _ uuid.UUID) error { return f.deleteErr }
func (f *fakeStore) MoveTask(_ context.Context, id uuid.UUID, _ storage.MoveTaskParams) (*model.Task, error) {
	return &model.Task{ID: id, Title: "x", Status: model.TaskStatusOpen}, nil
}
func (f *fakeStore) RebalanceTasks(_ context.Context, _ *uuid.UUID) (int64, error) {
	return int64(len(f.tasks)), nil
}
func (f *fakeStore) AttachTaskTag(_ context.Context, _ uuid.UUID, name string) (*model.Tag, error) {
	return &model.Tag{ID: uuid.New(), Name: name}, nil
}
func (f *fakeStore) DetachTaskTag(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (f *fakeStore) ListTags(_ context.Context) ([]model.TagWithCount, error)     { return f.tags, nil }
func (f *fakeStore) DeleteTag(_ context.Context, _ uuid.UUID) error               { return nil }

func (f *fakeStore) id() uuid.UUID {
	if f.resolveID == uuid.Nil {
		f.resolveID = uuid.New()
	}
	return f.resolveID
}

func do(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHealthAndVersion(t *testing.T) {
	h := NewHandler("v9", &fakeStore{}, "")
	if w := do(h, "GET", "/healthz", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("healthz: code=%d body=%s", w.Code, w.Body.String())
	}
	if w := do(h, "GET", "/version", "", ""); !strings.Contains(w.Body.String(), `"v9"`) {
		t.Errorf("version: %s", w.Body.String())
	}
}

func TestCreateTask(t *testing.T) {
	h := NewHandler("test", &fakeStore{}, "")
	w := do(h, "POST", "/tasks", `{"title":"hello"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"hello"`) {
		t.Errorf("body: %s", w.Body.String())
	}
}

func TestCreateTaskBadJSON(t *testing.T) {
	h := NewHandler("test", &fakeStore{}, "")
	if w := do(h, "POST", "/tasks", `{not json`, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("code=%d", w.Code)
	}
}

func TestListTasks(t *testing.T) {
	h := NewHandler("test", &fakeStore{tasks: []model.Task{{ID: uuid.New(), Title: "alpha", Status: model.TaskStatusOpen}}}, "")
	w := do(h, "GET", "/tasks", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"alpha"`) {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGetTaskNotFound(t *testing.T) {
	h := NewHandler("test", &fakeStore{getErr: storage.ErrTaskNotFound}, "")
	if w := do(h, "GET", "/tasks/abc", "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("code=%d", w.Code)
	}
}

func TestDeleteTask(t *testing.T) {
	h := NewHandler("test", &fakeStore{}, "")
	if w := do(h, "DELETE", "/tasks/abc", "", ""); w.Code != http.StatusNoContent {
		t.Fatalf("code=%d", w.Code)
	}
}

func TestUpdateValidationErrors(t *testing.T) {
	h := NewHandler("test", &fakeStore{}, "")
	cases := []struct{ name, body string }{
		{"no fields", `{}`},
		{"invalid status", `{"status":"in_progress"}`},
		{"unknown field", `{"bogus":1}`},
		{"dup id without status", `{"duplicate_of_id":"abc"}`},
	}
	for _, tc := range cases {
		if w := do(h, "PATCH", "/tasks/abc", tc.body, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s: code=%d body=%s", tc.name, w.Code, w.Body.String())
		}
	}
}

func TestAuthMiddleware(t *testing.T) {
	h := NewHandler("test", &fakeStore{}, "topsecret")
	if w := do(h, "GET", "/healthz", "", ""); w.Code != 200 {
		t.Errorf("healthz must stay open: code=%d", w.Code)
	}
	if w := do(h, "GET", "/tasks", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: code=%d", w.Code)
	}
	if w := do(h, "GET", "/tasks", "", "wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: code=%d", w.Code)
	}
	if w := do(h, "GET", "/tasks", "", "topsecret"); w.Code != http.StatusOK {
		t.Errorf("right token: code=%d", w.Code)
	}
}

func TestDecodeTaskUpdate(t *testing.T) {
	mk := func(body string) *http.Request { return httptest.NewRequest("PATCH", "/", strings.NewReader(body)) }

	d, err := decodeTaskUpdate(mk(`{"title":"new"}`))
	if err != nil || d.params.Title == nil || *d.params.Title != "new" {
		t.Fatalf("title decode: err=%v params=%+v", err, d.params)
	}

	if _, err := decodeTaskUpdate(mk(`{"status":"duplicate","duplicate_of_id":"abc"}`)); err != nil {
		t.Errorf("valid duplicate update rejected: %v", err)
	}

	bad := []string{`{}`, `{"status":"nope"}`, `{"bogus":1}`, `{"duplicate_of_id":"abc"}`, `{"title":null}`}
	for _, b := range bad {
		if _, err := decodeTaskUpdate(mk(b)); err == nil {
			t.Errorf("expected error for %s", b)
		}
	}
}

func TestDecodeTaskMove(t *testing.T) {
	mk := func(body string) *http.Request { return httptest.NewRequest("POST", "/", strings.NewReader(body)) }

	d, err := decodeTaskMove(mk(`{"parent_id":null,"position":"first"}`))
	if err != nil {
		t.Fatalf("move decode: %v", err)
	}
	if !d.parentRefSet || d.parentRef != nil {
		t.Errorf("parent_id null should set parentRefSet with nil ref: %+v", d)
	}
	if d.params.Position != "first" {
		t.Errorf("position not decoded: %+v", d.params)
	}

	for _, b := range []string{`{"position":"middle"}`, `{"nope":1}`, `{"before_id":""}`} {
		if _, err := decodeTaskMove(mk(b)); err == nil {
			t.Errorf("expected error for %s", b)
		}
	}
}
