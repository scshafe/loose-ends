package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	appconfig "loose_ends/internal/config"
	"loose_ends/internal/model"
)

type httpTaskClient struct {
	baseURL    string
	token      string
	display    appconfig.DisplayConfig
	httpClient *http.Client
}

type listTaskOptions struct {
	All    bool
	Status string
	Parent string
	Root   bool
	Tag    string
}

type outputOptions struct {
	IDLength   int
	TreeIndent string
	Color      bool
}

func newAddCommand(out io.Writer) *cobra.Command {
	var body string
	var parent string
	var tags []string

	cmd := &cobra.Command{
		Use:   "add TITLE",
		Short: "Create a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.TrimSpace(args[0])
			if title == "" {
				return fmt.Errorf("task title cannot be empty")
			}
			bodyText, err := bodyFromFlag(body)
			if err != nil {
				return err
			}
			client, err := newTaskClient()
			if err != nil {
				return err
			}

			req := map[string]any{"title": title}
			if bodyText != "" {
				req["body"] = bodyText
			}
			if strings.TrimSpace(parent) != "" {
				req["parent_id"] = strings.TrimSpace(parent)
			}

			task, err := client.createTask(cmd.Context(), req)
			if err != nil {
				return err
			}
			for _, tag := range tags {
				if strings.TrimSpace(tag) == "" {
					return fmt.Errorf("tag name cannot be empty")
				}
				if _, err := client.attachTag(cmd.Context(), task.ID.String(), tag); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(out, "%s %s\n", shortID(task.ID.String(), client.display.IDLength), task.Title)
			return err
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "task body; use '-' to read from stdin")
	cmd.Flags().StringVar(&parent, "parent", "", "parent task ID")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "attach tag to the new task")
	return cmd
}

func newListCommand(out io.Writer) *cobra.Command {
	var all bool
	var status string
	var parent string
	var root bool
	var tree bool
	var tag string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(status) != "" {
				parsed := model.TaskStatus(strings.TrimSpace(status))
				if !parsed.Valid() {
					return fmt.Errorf("invalid status %q (valid: open, done, archived, duplicate, partially_complete)", status)
				}
			}

			client, err := newTaskClient()
			if err != nil {
				return err
			}

			if strings.TrimSpace(parent) != "" && root {
				return fmt.Errorf("only one of --parent or --root may be set")
			}

			tasks, err := client.listTasks(cmd.Context(), listTaskOptions{All: all, Status: status, Parent: parent, Root: root, Tag: tag})
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(out, tasks)
			}
			if tree {
				return printTaskTree(out, tasks, outputOptionsFor(out, client.display))
			}
			for _, task := range tasks {
				if _, err := fmt.Fprintf(out, "%s %-18s %s\n", shortID(task.ID.String(), client.display.IDLength), task.Status, task.Title); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include closed tasks")
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&parent, "parent", "", "filter by parent task ID")
	cmd.Flags().BoolVar(&root, "root", false, "show only top-level tasks")
	cmd.Flags().BoolVar(&tree, "tree", false, "show tasks as a tree")
	cmd.Flags().StringVar(&tag, "tag", "", "filter by tag name")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON output")
	return cmd
}

func newShowCommand(out io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "show ID",
		Short: "Show a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newTaskClient()
			if err != nil {
				return err
			}

			task, err := client.getTask(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(out, task)
			}
			return printTask(out, task)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON output")
	return cmd
}

func newEditCommand(out io.Writer) *cobra.Command {
	var title string
	var body string

	cmd := &cobra.Command{
		Use:   "edit ID",
		Short: "Edit a task title or body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changedTitle := cmd.Flags().Changed("title")
			changedBody := cmd.Flags().Changed("body")
			if !changedTitle && !changedBody {
				return fmt.Errorf("at least one of --title or --body is required")
			}

			client, err := newTaskClient()
			if err != nil {
				return err
			}

			req := make(map[string]any)
			if changedTitle {
				trimmedTitle := strings.TrimSpace(title)
				if trimmedTitle == "" {
					return fmt.Errorf("task title cannot be empty")
				}
				req["title"] = trimmedTitle
			}
			if changedBody {
				bodyText, err := bodyFromFlag(body)
				if err != nil {
					return err
				}
				req["body"] = bodyText
			}

			task, err := client.updateTask(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "%s %s\n", shortID(task.ID.String(), client.display.IDLength), task.Title)
			return err
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new task title")
	cmd.Flags().StringVar(&body, "body", "", "new task body; use '-' to read from stdin")
	return cmd
}

func newRemoveCommand(out io.Writer) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "rm ID",
		Aliases: []string{"delete"},
		Short:   "Delete a task",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				confirmed, err := confirm(out, fmt.Sprintf("delete task %s? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if !confirmed {
					_, err := fmt.Fprintln(out, "not deleted")
					return err
				}
			}

			client, err := newTaskClient()
			if err != nil {
				return err
			}
			if err := client.deleteTask(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, err = fmt.Fprintln(out, "deleted")
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return cmd
}

func newStatusCommand(out io.Writer, use string, short string, status model.TaskStatus) *cobra.Command {
	return &cobra.Command{
		Use:   use + " ID",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newTaskClient()
			if err != nil {
				return err
			}
			task, err := client.updateTask(cmd.Context(), args[0], map[string]any{"status": status})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "%s %-18s %s\n", shortID(task.ID.String(), client.display.IDLength), task.Status, task.Title)
			return err
		},
	}
}

func newDuplicateCommand(out io.Writer) *cobra.Command {
	var duplicateOf string

	cmd := &cobra.Command{
		Use:   "duplicate ID --of OTHER_ID",
		Short: "Mark a task as a duplicate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(duplicateOf) == "" {
				return fmt.Errorf("--of is required")
			}
			client, err := newTaskClient()
			if err != nil {
				return err
			}
			task, err := client.updateTask(cmd.Context(), args[0], map[string]any{
				"status":          model.TaskStatusDuplicate,
				"duplicate_of_id": duplicateOf,
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "%s %-18s %s\n", shortID(task.ID.String(), client.display.IDLength), task.Status, task.Title)
			return err
		},
	}
	cmd.Flags().StringVar(&duplicateOf, "of", "", "canonical task ID")
	_ = cmd.MarkFlagRequired("of")
	return cmd
}

func newMoveCommand(out io.Writer) *cobra.Command {
	var parent string
	var root bool
	var before string
	var after string
	var first bool
	var last bool

	cmd := &cobra.Command{
		Use:   "move ID",
		Short: "Move a task within the hierarchy or sibling order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(parent) != "" && root {
				return fmt.Errorf("only one of --parent or --root may be set")
			}
			positionCount := 0
			if strings.TrimSpace(before) != "" {
				positionCount++
			}
			if strings.TrimSpace(after) != "" {
				positionCount++
			}
			if first {
				positionCount++
			}
			if last {
				positionCount++
			}
			if positionCount > 1 {
				return fmt.Errorf("only one of --before, --after, --first, or --last may be set")
			}

			client, err := newTaskClient()
			if err != nil {
				return err
			}
			req := make(map[string]any)
			if root {
				req["parent_id"] = nil
			} else if strings.TrimSpace(parent) != "" {
				req["parent_id"] = strings.TrimSpace(parent)
			}
			if strings.TrimSpace(before) != "" {
				req["before_id"] = strings.TrimSpace(before)
			}
			if strings.TrimSpace(after) != "" {
				req["after_id"] = strings.TrimSpace(after)
			}
			if first {
				req["position"] = "first"
			}
			if last {
				req["position"] = "last"
			}

			task, err := client.moveTask(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "%s %.6g %s\n", shortID(task.ID.String(), client.display.IDLength), task.SortKey, task.Title)
			return err
		},
	}
	cmd.Flags().StringVar(&parent, "parent", "", "new parent task ID")
	cmd.Flags().BoolVar(&root, "root", false, "move to top level")
	cmd.Flags().StringVar(&before, "before", "", "place before task ID")
	cmd.Flags().StringVar(&after, "after", "", "place after task ID")
	cmd.Flags().BoolVar(&first, "first", false, "place first among siblings")
	cmd.Flags().BoolVar(&last, "last", false, "place last among siblings")
	return cmd
}

func newRebalanceCommand(out io.Writer) *cobra.Command {
	var parent string
	var root bool

	cmd := &cobra.Command{
		Use:   "rebalance",
		Short: "Re-space sibling sort keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(parent) != "" && root {
				return fmt.Errorf("only one of --parent or --root may be set")
			}
			client, err := newTaskClient()
			if err != nil {
				return err
			}
			req := make(map[string]any)
			if strings.TrimSpace(parent) != "" {
				req["parent_id"] = strings.TrimSpace(parent)
			} else {
				req["parent_id"] = nil
			}
			updated, err := client.rebalanceTasks(cmd.Context(), req)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "rebalanced %d tasks\n", updated)
			return err
		},
	}
	cmd.Flags().StringVar(&parent, "parent", "", "parent task ID")
	cmd.Flags().BoolVar(&root, "root", false, "rebalance top-level tasks")
	return cmd
}

func newTagCommand(out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "tag ID TAG [TAG ...]",
		Short: "Attach a tag to a task",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newTaskClient()
			if err != nil {
				return err
			}
			for _, name := range args[1:] {
				tag, err := client.attachTag(cmd.Context(), args[0], name)
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintf(out, "%s\n", tag.Name); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newUntagCommand(out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "untag ID TAG [TAG ...]",
		Short: "Detach a tag from a task",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newTaskClient()
			if err != nil {
				return err
			}
			for _, name := range args[1:] {
				if err := client.detachTag(cmd.Context(), args[0], name); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintln(out, "untagged")
			return err
		},
	}
}

func newTagsCommand(out io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "tags",
		Short: "List tags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newTaskClient()
			if err != nil {
				return err
			}
			tags, err := client.listTags(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(out, tags)
			}
			for _, tag := range tags {
				if _, err := fmt.Fprintf(out, "%s %d\n", tag.Name, tag.TaskCount); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON output")
	return cmd
}

func newTaskClient() (*httpTaskClient, error) {
	cfg, _, err := appconfig.Load()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Server.URL) == "" {
		return nil, fmt.Errorf("server.url is required")
	}
	return &httpTaskClient{
		baseURL: strings.TrimRight(cfg.Server.URL, "/"),
		token:   cfg.Server.Token,
		display: cfg.Display,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}, nil
}

func (c *httpTaskClient) createTask(ctx context.Context, req map[string]any) (*model.Task, error) {
	var task model.Task
	if err := c.doJSON(ctx, http.MethodPost, "/tasks", req, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (c *httpTaskClient) listTasks(ctx context.Context, opts listTaskOptions) ([]model.Task, error) {
	var tasks []model.Task
	values := url.Values{}
	if opts.All {
		values.Set("include_closed", "true")
	}
	if strings.TrimSpace(opts.Status) != "" {
		values.Set("status", strings.TrimSpace(opts.Status))
	}
	if opts.Root {
		values.Set("parent_id", "root")
	} else if strings.TrimSpace(opts.Parent) != "" {
		values.Set("parent_id", strings.TrimSpace(opts.Parent))
	}
	if strings.TrimSpace(opts.Tag) != "" {
		values.Set("tag", strings.TrimSpace(opts.Tag))
	}
	path := "/tasks"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (c *httpTaskClient) getTask(ctx context.Context, id string) (*model.Task, error) {
	var task model.Task
	if err := c.doJSON(ctx, http.MethodGet, "/tasks/"+url.PathEscape(id), nil, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (c *httpTaskClient) updateTask(ctx context.Context, id string, req map[string]any) (*model.Task, error) {
	var task model.Task
	if err := c.doJSON(ctx, http.MethodPatch, "/tasks/"+url.PathEscape(id), req, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (c *httpTaskClient) deleteTask(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/tasks/"+url.PathEscape(id), nil, nil)
}

func (c *httpTaskClient) moveTask(ctx context.Context, id string, req map[string]any) (*model.Task, error) {
	var task model.Task
	if err := c.doJSON(ctx, http.MethodPost, "/tasks/"+url.PathEscape(id)+"/move", req, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (c *httpTaskClient) rebalanceTasks(ctx context.Context, req map[string]any) (int64, error) {
	var resp struct {
		Updated int64 `json:"updated"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/admin/rebalance", req, &resp); err != nil {
		return 0, err
	}
	return resp.Updated, nil
}

func (c *httpTaskClient) attachTag(ctx context.Context, id string, name string) (*model.Tag, error) {
	var tag model.Tag
	if err := c.doJSON(ctx, http.MethodPost, "/tasks/"+url.PathEscape(id)+"/tags", map[string]any{"name": name}, &tag); err != nil {
		return nil, err
	}
	return &tag, nil
}

func (c *httpTaskClient) detachTag(ctx context.Context, id string, name string) error {
	return c.doJSON(ctx, http.MethodDelete, "/tasks/"+url.PathEscape(id)+"/tags/"+url.PathEscape(name), nil, nil)
}

func (c *httpTaskClient) listTags(ctx context.Context) ([]model.TagWithCount, error) {
	var tags []model.TagWithCount
	if err := c.doJSON(ctx, http.MethodGet, "/tags", nil, &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

func (c *httpTaskClient) doJSON(ctx context.Context, method string, path string, reqBody any, dst any) error {
	var body io.Reader
	if reqBody != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
			return err
		}
		body = &buf
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s failed; is loose-ends serve running at %s? %w", method, path, c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return decodeAPIError(resp)
	}
	if dst == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func decodeAPIError(resp *http.Response) error {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("request failed: %s", resp.Status)
	}
	if payload.Error.Message == "" {
		return fmt.Errorf("request failed: %s", resp.Status)
	}
	if payload.Error.Code == "" {
		return fmt.Errorf("%s", payload.Error.Message)
	}
	return fmt.Errorf("%s: %s", payload.Error.Code, payload.Error.Message)
}

func printTask(out io.Writer, task *model.Task) error {
	if _, err := fmt.Fprintf(out, "id: %s\n", task.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "status: %s\n", task.Status); err != nil {
		return err
	}
	if task.ParentID != nil {
		if _, err := fmt.Fprintf(out, "parent_id: %s\n", *task.ParentID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "sort_key: %.6g\n", task.SortKey); err != nil {
		return err
	}
	if task.DuplicateOfID != nil {
		if _, err := fmt.Fprintf(out, "duplicate_of_id: %s\n", *task.DuplicateOfID); err != nil {
			return err
		}
	}
	if task.CompletedAt != nil {
		if _, err := fmt.Fprintf(out, "completed_at: %s\n", task.CompletedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "title: %s\n", task.Title); err != nil {
		return err
	}
	if task.Body != nil && *task.Body != "" {
		if _, err := fmt.Fprintf(out, "\n%s\n", *task.Body); err != nil {
			return err
		}
	}
	return nil
}

func printTaskTree(out io.Writer, tasks []model.Task, opts outputOptions) error {
	children := make(map[string][]model.Task)
	ids := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		ids[task.ID.String()] = struct{}{}
	}
	for _, task := range tasks {
		parentKey := ""
		if task.ParentID != nil {
			parentKey = task.ParentID.String()
		}
		children[parentKey] = append(children[parentKey], task)
	}
	for key := range children {
		sort.SliceStable(children[key], func(i, j int) bool {
			left := children[key][i]
			right := children[key][j]
			if left.SortKey == right.SortKey {
				return left.CreatedAt.Before(right.CreatedAt)
			}
			return left.SortKey < right.SortKey
		})
	}

	roots := children[""]
	for _, task := range tasks {
		if task.ParentID != nil {
			if _, ok := ids[task.ParentID.String()]; !ok {
				roots = append(roots, task)
			}
		}
	}
	sort.SliceStable(roots, func(i, j int) bool {
		if roots[i].SortKey == roots[j].SortKey {
			return roots[i].CreatedAt.Before(roots[j].CreatedAt)
		}
		return roots[i].SortKey < roots[j].SortKey
	})
	for _, task := range roots {
		if err := printTaskTreeNode(out, children, task, "", opts); err != nil {
			return err
		}
	}
	return nil
}

func printTaskTreeNode(out io.Writer, children map[string][]model.Task, task model.Task, indent string, opts outputOptions) error {
	if _, err := fmt.Fprintf(out, "%s%s %s %s\n", indent, shortID(task.ID.String(), opts.IDLength), formatStatus(task.Status, opts.Color), task.Title); err != nil {
		return err
	}
	for _, child := range children[task.ID.String()] {
		if err := printTaskTreeNode(out, children, child, indent+opts.TreeIndent, opts); err != nil {
			return err
		}
	}
	return nil
}

func confirm(out io.Writer, prompt string) (bool, error) {
	if _, err := fmt.Fprint(out, prompt); err != nil {
		return false, err
	}
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes", nil
}

func bodyFromFlag(value string) (string, error) {
	if value != "-" {
		return value, nil
	}
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read body from stdin: %w", err)
	}
	return string(body), nil
}

func outputOptionsFor(out io.Writer, display appconfig.DisplayConfig) outputOptions {
	idLength := display.IDLength
	if idLength <= 0 {
		idLength = 8
	}
	indent := display.TreeIndent
	if indent == "" {
		indent = "  "
	}
	return outputOptions{
		IDLength:   idLength,
		TreeIndent: indent,
		Color:      colorEnabled(out, display.Color),
	}
}

func colorEnabled(out io.Writer, mode string) bool {
	if noColor || mode == "never" {
		return false
	}
	if mode == "always" {
		return true
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func formatStatus(status model.TaskStatus, color bool) string {
	switch status {
	case model.TaskStatusOpen:
		return colorize("☐ open", "\033[36m", color)
	case model.TaskStatusDone:
		return colorize("☑ done", "\033[32m", color)
	case model.TaskStatusPartiallyComplete:
		return colorize("▣ partial", "\033[33m", color)
	case model.TaskStatusArchived:
		return colorize("⊗ archived", "\033[2m", color)
	case model.TaskStatusDuplicate:
		return colorize("≈ duplicate", "\033[35m", color)
	default:
		return string(status)
	}
}

func colorize(text string, code string, enabled bool) string {
	if !enabled {
		return text
	}
	return code + text + "\033[0m"
}

func writeJSON(out io.Writer, value any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func shortID(id string, length int) string {
	if length <= 0 {
		length = 8
	}
	if len(id) <= length {
		return id
	}
	return id[:length]
}
