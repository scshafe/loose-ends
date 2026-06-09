package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"loose_ends/internal/model"
	"loose_ends/internal/storage"
)

type taskHandlers struct {
	store TaskStore
}

type taskCreateRequest struct {
	Title    string  `json:"title"`
	Body     *string `json:"body"`
	ParentID *string `json:"parent_id"`
}

type taskTagRequest struct {
	Name string `json:"name"`
}

func (h taskHandlers) create(w http.ResponseWriter, r *http.Request) {
	var req taskCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	params := storage.CreateTaskParams{Title: req.Title, Body: req.Body}
	if req.ParentID != nil {
		parentID, err := h.store.ResolveTaskID(r.Context(), *req.ParentID)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		params.ParentID = &parentID
	}

	task, err := h.store.CreateTask(r.Context(), params)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (h taskHandlers) tree(w http.ResponseWriter, r *http.Request) {
	params, err := decodeTaskList(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	var rootID *uuid.UUID
	if root := strings.TrimSpace(r.URL.Query().Get("root")); root != "" {
		resolved, err := h.store.ResolveTaskID(r.Context(), root)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		rootID = &resolved
	}

	tasks, err := h.store.ListTaskTree(r.Context(), rootID, params)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (h taskHandlers) list(w http.ResponseWriter, r *http.Request) {
	params, err := decodeTaskList(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if err := h.applyParentQuery(r, &params); err != nil {
		writeStorageError(w, err)
		return
	}

	tasks, err := h.store.ListTasks(r.Context(), params)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (h taskHandlers) get(w http.ResponseWriter, r *http.Request) {
	task, err := h.store.ResolveTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h taskHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, err := h.store.ResolveTaskID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStorageError(w, err)
		return
	}

	decoded, err := decodeTaskUpdate(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if decoded.duplicateOfRef != nil {
		duplicateOfID, err := h.store.ResolveTaskID(r.Context(), *decoded.duplicateOfRef)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		if duplicateOfID == id {
			writeStorageError(w, storage.ErrTaskCannotDuplicateSelf)
			return
		}
		if _, err := h.store.GetTask(r.Context(), duplicateOfID); err != nil {
			writeStorageError(w, err)
			return
		}
		decoded.params.DuplicateOfID = &duplicateOfID
	}

	task, err := h.store.UpdateTask(r.Context(), id, decoded.params)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h taskHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, err := h.store.ResolveTaskID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStorageError(w, err)
		return
	}
	if err := h.store.DeleteTask(r.Context(), id); err != nil {
		writeStorageError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h taskHandlers) move(w http.ResponseWriter, r *http.Request) {
	id, err := h.store.ResolveTaskID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStorageError(w, err)
		return
	}

	decoded, err := decodeTaskMove(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if decoded.parentRefSet {
		decoded.params.ParentSet = true
		if decoded.parentRef != nil {
			parentID, err := h.store.ResolveTaskID(r.Context(), *decoded.parentRef)
			if err != nil {
				writeStorageError(w, err)
				return
			}
			decoded.params.ParentID = &parentID
		}
	}
	if decoded.beforeRef != nil {
		beforeID, err := h.store.ResolveTaskID(r.Context(), *decoded.beforeRef)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		decoded.params.BeforeID = &beforeID
	}
	if decoded.afterRef != nil {
		afterID, err := h.store.ResolveTaskID(r.Context(), *decoded.afterRef)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		decoded.params.AfterID = &afterID
	}

	task, err := h.store.MoveTask(r.Context(), id, decoded.params)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h taskHandlers) rebalance(w http.ResponseWriter, r *http.Request) {
	decoded, err := decodeRebalance(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	var parentID *uuid.UUID
	if decoded.parentRef != nil {
		resolved, err := h.store.ResolveTaskID(r.Context(), *decoded.parentRef)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		parentID = &resolved
	}
	count, err := h.store.RebalanceTasks(r.Context(), parentID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"updated": count})
}

func (h taskHandlers) attachTag(w http.ResponseWriter, r *http.Request) {
	id, err := h.store.ResolveTaskID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStorageError(w, err)
		return
	}

	var req taskTagRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	tag, err := h.store.AttachTaskTag(r.Context(), id, req.Name)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tag)
}

func (h taskHandlers) detachTag(w http.ResponseWriter, r *http.Request) {
	id, err := h.store.ResolveTaskID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStorageError(w, err)
		return
	}

	if err := h.store.DetachTaskTag(r.Context(), id, chi.URLParam(r, "tagName")); err != nil {
		writeStorageError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h taskHandlers) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.store.ListTags(r.Context())
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (h taskHandlers) deleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "tag id must be a UUID")
		return
	}
	if err := h.store.DeleteTag(r.Context(), id); err != nil {
		writeStorageError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeTaskList(r *http.Request) (storage.ListTaskParams, error) {
	var params storage.ListTaskParams
	query := r.URL.Query()
	if rawStatus := strings.TrimSpace(query.Get("status")); rawStatus != "" {
		status := model.TaskStatus(rawStatus)
		if !status.Valid() {
			return storage.ListTaskParams{}, fmt.Errorf("invalid status %q", rawStatus)
		}
		params.Status = &status
	}
	includeClosed := strings.TrimSpace(strings.ToLower(query.Get("include_closed")))
	params.IncludeClosed = includeClosed == "1" || includeClosed == "true" || includeClosed == "yes"
	params.Tag = strings.TrimSpace(query.Get("tag"))
	return params, nil
}

func (h taskHandlers) applyParentQuery(r *http.Request, params *storage.ListTaskParams) error {
	rawParent, ok := r.URL.Query()["parent_id"]
	if !ok {
		return nil
	}
	params.ParentSet = true
	value := ""
	if len(rawParent) > 0 {
		value = strings.TrimSpace(rawParent[0])
	}
	if value == "" || value == "root" || value == "null" {
		params.ParentID = nil
		return nil
	}
	parentID, err := h.store.ResolveTaskID(r.Context(), value)
	if err != nil {
		return err
	}
	params.ParentID = &parentID
	return nil
}

type decodedTaskUpdate struct {
	params         storage.UpdateTaskParams
	duplicateOfRef *string
}

type decodedTaskMove struct {
	params       storage.MoveTaskParams
	parentRefSet bool
	parentRef    *string
	beforeRef    *string
	afterRef     *string
}

func decodeTaskMove(r *http.Request) (decodedTaskMove, error) {
	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		return decodedTaskMove{}, err
	}

	var decoded decodedTaskMove
	for key, value := range raw {
		switch key {
		case "parent_id":
			decoded.parentRefSet = true
			if string(value) == "null" {
				continue
			}
			ref, err := decodeNonEmptyString(value, "parent_id")
			if err != nil {
				return decodedTaskMove{}, err
			}
			decoded.parentRef = &ref
		case "before_id":
			if string(value) == "null" {
				continue
			}
			ref, err := decodeNonEmptyString(value, "before_id")
			if err != nil {
				return decodedTaskMove{}, err
			}
			decoded.beforeRef = &ref
		case "after_id":
			if string(value) == "null" {
				continue
			}
			ref, err := decodeNonEmptyString(value, "after_id")
			if err != nil {
				return decodedTaskMove{}, err
			}
			decoded.afterRef = &ref
		case "position":
			if string(value) == "null" {
				continue
			}
			position, err := decodeNonEmptyString(value, "position")
			if err != nil {
				return decodedTaskMove{}, err
			}
			if position != "first" && position != "last" {
				return decodedTaskMove{}, fmt.Errorf("position must be first or last")
			}
			decoded.params.Position = position
		default:
			return decodedTaskMove{}, fmt.Errorf("unknown field %q", key)
		}
	}
	return decoded, nil
}

type decodedRebalance struct {
	parentRef *string
}

func decodeRebalance(r *http.Request) (decodedRebalance, error) {
	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		return decodedRebalance{}, err
	}
	var decoded decodedRebalance
	for key, value := range raw {
		switch key {
		case "parent_id":
			if string(value) == "null" {
				continue
			}
			ref, err := decodeNonEmptyString(value, "parent_id")
			if err != nil {
				return decodedRebalance{}, err
			}
			decoded.parentRef = &ref
		default:
			return decodedRebalance{}, fmt.Errorf("unknown field %q", key)
		}
	}
	return decoded, nil
}

func decodeNonEmptyString(value json.RawMessage, name string) (string, error) {
	var ref string
	if err := json.Unmarshal(value, &ref); err != nil {
		return "", fmt.Errorf("%s must be a string or null", name)
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("%s cannot be empty", name)
	}
	return ref, nil
}

func decodeTaskUpdate(r *http.Request) (decodedTaskUpdate, error) {
	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		return decodedTaskUpdate{}, err
	}

	var decoded decodedTaskUpdate
	for key, value := range raw {
		switch key {
		case "title":
			if string(value) == "null" {
				return decodedTaskUpdate{}, fmt.Errorf("title cannot be null")
			}
			var title string
			if err := json.Unmarshal(value, &title); err != nil {
				return decodedTaskUpdate{}, fmt.Errorf("title must be a string")
			}
			decoded.params.Title = &title
		case "body":
			decoded.params.BodySet = true
			if string(value) == "null" {
				decoded.params.Body = nil
				continue
			}
			var body string
			if err := json.Unmarshal(value, &body); err != nil {
				return decodedTaskUpdate{}, fmt.Errorf("body must be a string or null")
			}
			decoded.params.Body = &body
		case "status":
			if string(value) == "null" {
				return decodedTaskUpdate{}, fmt.Errorf("status cannot be null")
			}
			var rawStatus string
			if err := json.Unmarshal(value, &rawStatus); err != nil {
				return decodedTaskUpdate{}, fmt.Errorf("status must be a string")
			}
			status := model.TaskStatus(rawStatus)
			if !status.Valid() {
				return decodedTaskUpdate{}, fmt.Errorf("invalid status %q", rawStatus)
			}
			decoded.params.Status = &status
		case "duplicate_of_id":
			if string(value) == "null" {
				continue
			}
			var ref string
			if err := json.Unmarshal(value, &ref); err != nil {
				return decodedTaskUpdate{}, fmt.Errorf("duplicate_of_id must be a string or null")
			}
			ref = strings.TrimSpace(ref)
			if ref == "" {
				return decodedTaskUpdate{}, fmt.Errorf("duplicate_of_id cannot be empty")
			}
			decoded.duplicateOfRef = &ref
		default:
			return decodedTaskUpdate{}, fmt.Errorf("unknown field %q", key)
		}
	}
	if decoded.duplicateOfRef != nil && (decoded.params.Status == nil || *decoded.params.Status != model.TaskStatusDuplicate) {
		return decodedTaskUpdate{}, fmt.Errorf("duplicate_of_id requires status duplicate")
	}
	if decoded.params.Title == nil && !decoded.params.BodySet && decoded.params.Status == nil {
		return decodedTaskUpdate{}, fmt.Errorf("at least one field is required")
	}
	return decoded, nil
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("request body must be JSON")
		}
		return err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body must contain a single JSON value")
	}
	return nil
}

func writeStorageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrTaskNotFound):
		writeError(w, http.StatusNotFound, "TASK_NOT_FOUND", "no task matches that ID or prefix")
	case errors.Is(err, storage.ErrTagNotFound):
		writeError(w, http.StatusNotFound, "TAG_NOT_FOUND", "tag not found")
	case errors.Is(err, storage.ErrAmbiguousTask):
		writeError(w, http.StatusBadRequest, "TASK_ID_AMBIGUOUS", "task ID prefix is ambiguous; use more characters")
	case errors.Is(err, storage.ErrInvalidTaskStatus):
		writeError(w, http.StatusBadRequest, "INVALID_TASK_STATUS", err.Error())
	case errors.Is(err, storage.ErrDuplicateTargetRequired), errors.Is(err, storage.ErrTaskCannotDuplicateSelf), errors.Is(err, storage.ErrTaskMoveInvalid), errors.Is(err, storage.ErrTaskMoveCycle):
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case strings.Contains(err.Error(), "required"):
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
