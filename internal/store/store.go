// Package store is the SQLite-backed persistence layer for the task tracker.
package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"tasktracker/internal/model"
)

//go:embed schema.sql
var schemaSQL string

// Store wraps a SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, enables WAL,
// applies the schema and runs lightweight migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // modernc serializes writes; avoids "database is locked"
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// migrate adds any columns missing from an older database.
func (s *Store) migrate() error {
	cols := []struct{ name, ddl string }{
		{"importance", "TEXT NOT NULL DEFAULT 'normal'"},
		{"analyst_name", "TEXT NOT NULL DEFAULT ''"},
		{"dev_name", "TEXT NOT NULL DEFAULT ''"},
		{"problem_stage", "TEXT NOT NULL DEFAULT ''"},
		{"problem_desc", "TEXT NOT NULL DEFAULT ''"},
		{"analyst_lied", "INTEGER NOT NULL DEFAULT 0"},
		{"solution_desc", "TEXT NOT NULL DEFAULT ''"},
		{"work_started", "TEXT"},
		{"last_reminder", "TEXT"},
	}
	for _, c := range cols {
		if err := s.ensureColumn("tasks", c.name, c.ddl); err != nil {
			return err
		}
	}
	return nil
}

// ensureColumn adds a column to a table if it is not already present.
func (s *Store) ensureColumn(table, col, ddl string) error {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == col {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, ddl))
	return err
}

func (s *Store) Close() error { return s.db.Close() }

const tsLayout = time.RFC3339Nano

func nowStr() string { return time.Now().UTC().Format(tsLayout) }

func parseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(tsLayout, v)
	return t
}

func parseTimePtr(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t := parseTime(v.String)
	return &t
}

// ---- Users ----

// UpsertUser sets or updates a user's role.
func (s *Store) UpsertUser(tgID int64, username string, role model.Role) error {
	now := nowStr()
	_, err := s.db.Exec(`
		INSERT INTO users (tg_id, username, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tg_id) DO UPDATE SET
			username = excluded.username,
			role = excluded.role,
			updated_at = excluded.updated_at`,
		tgID, username, string(role), now, now)
	return err
}

// GetUser returns the user, or (nil, nil) if not registered.
func (s *Store) GetUser(tgID int64) (*model.User, error) {
	row := s.db.QueryRow(`SELECT tg_id, username, role, created_at, updated_at FROM users WHERE tg_id = ?`, tgID)
	return scanUser(row)
}

// ListUsers returns all registered users (used to broadcast notifications).
func (s *Store) ListUsers() ([]*model.User, error) {
	rows, err := s.db.Query(`SELECT tg_id, username, role, created_at, updated_at FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func scanUser(sc interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	var role, created, updated string
	if err := sc.Scan(&u.TgID, &u.Username, &role, &created, &updated); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	u.Role = model.Role(role)
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updated)
	return &u, nil
}

// ---- Tasks ----

// CreateTask inserts a new bug (status unassigned) and returns its id.
func (s *Store) CreateTask(t *model.Task) (int64, error) {
	now := nowStr()
	imp := t.Importance
	if !imp.Valid() {
		imp = model.ImportanceNormal
	}
	res, err := s.db.Exec(`
		INSERT INTO tasks (title, description, category, backend_kind, importance,
			status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'unassigned', ?, ?, ?)`,
		t.Title, t.Description, string(t.Category), string(t.BackendKind), string(imp),
		t.CreatedBy, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const taskCols = `id, title, description, category, backend_kind, importance, status,
	analyst_name, dev_name, analyst_notes, problem_stage, problem_desc,
	analyst_lied, solution_desc, created_by, created_at, updated_at, closed_at,
	work_started, last_reminder`

func scanTask(sc interface{ Scan(...any) error }) (*model.Task, error) {
	var t model.Task
	var category, backendKind, importance, status, problemStage string
	var analystLied int
	var created, updated string
	var closed, workStarted, lastReminder sql.NullString
	if err := sc.Scan(&t.ID, &t.Title, &t.Description, &category, &backendKind, &importance, &status,
		&t.AnalystName, &t.DevName, &t.AnalystNotes, &problemStage, &t.ProblemDesc,
		&analystLied, &t.SolutionDesc, &t.CreatedBy, &created, &updated, &closed,
		&workStarted, &lastReminder); err != nil {
		return nil, err
	}
	t.Category = model.Category(category)
	t.BackendKind = model.BackendKind(backendKind)
	t.Importance = model.Importance(importance)
	t.Status = model.Status(status)
	t.ProblemStage = model.Stage(problemStage)
	t.AnalystLied = analystLied != 0
	t.CreatedAt = parseTime(created)
	t.UpdatedAt = parseTime(updated)
	t.ClosedAt = parseTimePtr(closed)
	t.WorkStarted = parseTimePtr(workStarted)
	t.LastReminder = parseTimePtr(lastReminder)
	return &t, nil
}

// windowWhere returns the SQL predicate selecting a page's tasks, by status.
func windowWhere(w model.Window) string {
	switch w {
	case model.WindowNew:
		return "status = 'unassigned'"
	case model.WindowAnalyst:
		return "status IN ('analyst_idle','analyst_wip','analyst_help')"
	case model.WindowImpl:
		return "status IN ('impl_idle','impl_wip','impl_help')"
	case model.WindowDone:
		return "status = 'done'"
	case model.WindowProblems:
		return "status = 'problem'"
	default:
		return "1=0"
	}
}

// ListTasks returns tasks for a window, newest first.
func (s *Store) ListTasks(w model.Window) ([]*model.Task, error) {
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE %s ORDER BY created_at DESC`,
		taskCols, windowWhere(w))
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTask returns a task hydrated with comments and attachments.
func (s *Store) GetTask(id int64) (*model.Task, error) {
	row := s.db.QueryRow(fmt.Sprintf(`SELECT %s FROM tasks WHERE id = ?`, taskCols), id)
	t, err := scanTask(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if t.Comments, err = s.listComments(id); err != nil {
		return nil, err
	}
	if t.Attachments, err = s.listAttachments(id); err != nil {
		return nil, err
	}
	return t, nil
}

// exec runs an UPDATE statement with updated_at stamped.
func (s *Store) update(id int64, sets string, args ...any) error {
	q := fmt.Sprintf(`UPDATE tasks SET %s, updated_at = ? WHERE id = ?`, sets)
	args = append(args, nowStr(), id)
	_, err := s.db.Exec(q, args...)
	return err
}

// ---- lifecycle transitions ----

// AssignAnalyst distributes a new task: picks the analyst and moves it to the
// Аналитика page (status not-started).
func (s *Store) AssignAnalyst(id int64, name string) error {
	if err := s.update(id, "status = 'analyst_idle', analyst_name = ?", name); err != nil {
		return err
	}
	s.LogEvent(name, "assign_analyst", id)
	return nil
}

// AssignImpl distributes a new task straight to Реализация, skipping analysis
// (used when the cause is already clear). dev_name is the implementer.
func (s *Store) AssignImpl(id int64, name string) error {
	if err := s.update(id, "status = 'impl_idle', dev_name = ?", name); err != nil {
		return err
	}
	s.LogEvent(name, "assign_impl", id)
	return nil
}

// StartWork flips an idle task to "в работе" for the given stage and stamps the
// work-start time (resetting the reminder clock).
func (s *Store) StartWork(id int64, stage model.Stage) error {
	analyst, dev := s.names(id)
	switch stage {
	case model.StageAnalyst:
		if err := s.update(id, "status = 'analyst_wip', work_started = ?, last_reminder = NULL", nowStr()); err != nil {
			return err
		}
		s.LogEvent(analyst, "start_analyst", id)
		return nil
	case model.StageImpl:
		if err := s.update(id, "status = 'impl_wip', work_started = ?, last_reminder = NULL", nowStr()); err != nil {
			return err
		}
		s.LogEvent(dev, "start_impl", id)
		return nil
	}
	return fmt.Errorf("start: bad stage %q", stage)
}

// ReturnToPool sends a task back to Новые задачи (unassigned), clearing
// assignees, problem fields and the work clock.
func (s *Store) ReturnToPool(id int64) error {
	return s.update(id, `status = 'unassigned', analyst_name = '', dev_name = '',
		problem_stage = '', problem_desc = '', work_started = NULL, last_reminder = NULL,
		closed_at = NULL`)
}

// SetLastReminder records that a reminder was just sent for a task.
func (s *Store) SetLastReminder(id int64) error {
	return s.update(id, "last_reminder = ?", nowStr())
}

// CountActiveByImportance counts tasks currently being worked (wip/help on
// either stage) at the given importance — used to decide the low-priority nudge.
func (s *Store) CountActiveByImportance(imp model.Importance) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks
		WHERE importance = ? AND status IN
		('analyst_wip','analyst_help','impl_wip','impl_help')`, string(imp)).Scan(&n)
	return n, err
}

// ListInProgress returns all tasks currently in work (wip/help), used by the
// reminder scheduler.
func (s *Store) ListInProgress() ([]*model.Task, error) {
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE status IN
		('analyst_wip','analyst_help','impl_wip','impl_help') ORDER BY created_at ASC`, taskCols)
	return s.queryTasks(q)
}

// ListWaiting returns idle/unassigned tasks of an importance (the low-priority
// nudge target), oldest first.
func (s *Store) ListWaiting(imp model.Importance) ([]*model.Task, error) {
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE importance = ? AND status IN
		('unassigned','analyst_idle','impl_idle') ORDER BY created_at ASC`, taskCols)
	return s.queryTasks(q, string(imp))
}

func (s *Store) queryTasks(q string, args ...any) ([]*model.Task, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- announcements ----

// AddAnnouncement records a bot message posted for a task.
func (s *Store) AddAnnouncement(taskID, chatID, messageID int64) error {
	_, err := s.db.Exec(`INSERT INTO task_announcements (task_id, chat_id, message_id) VALUES (?, ?, ?)`,
		taskID, chatID, messageID)
	return err
}

// Announcement is a (chat, message) the bot can reply to.
type Announcement struct {
	ChatID    int64
	MessageID int64
}

// ListAnnouncements returns the announcement messages posted for a task.
func (s *Store) ListAnnouncements(taskID int64) ([]Announcement, error) {
	rows, err := s.db.Query(`SELECT chat_id, message_id FROM task_announcements WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ChatID, &a.MessageID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetHelp toggles "горит очко" for the given stage (the task stays on its page).
func (s *Store) SetHelp(id int64, stage model.Stage, on bool) error {
	var status model.Status
	switch stage {
	case model.StageAnalyst:
		status = model.StatusAnalystWIP
		if on {
			status = model.StatusAnalystHelp
		}
	case model.StageImpl:
		status = model.StatusImplWIP
		if on {
			status = model.StatusImplHelp
		}
	default:
		return fmt.Errorf("help: bad stage %q", stage)
	}
	if err := s.update(id, "status = ?", string(status)); err != nil {
		return err
	}
	if on {
		analyst, dev := s.names(id)
		who := analyst
		if stage == model.StageImpl {
			who = dev
		}
		s.LogEvent(who, "panic", id)
	}
	return nil
}

// SetAnalystNotes stores the analyst's solution notes / new inputs.
func (s *Store) SetAnalystNotes(id int64, notes string) error {
	return s.update(id, "analyst_notes = ?", notes)
}

// AnalystSuccess ("Мужик"): saves notes, assigns the implementer and moves the
// task to the Реализация page (not-started).
func (s *Store) AnalystSuccess(id int64, implName, notes string) error {
	analyst, _ := s.names(id)
	if err := s.update(id, "status = 'impl_idle', dev_name = ?, analyst_notes = ?", implName, notes); err != nil {
		return err
	}
	s.LogEvent(analyst, "analyst_success", id)
	return nil
}

// AnalystFail ("Лох"): records the problem and moves the task to the Проблемы
// page, tagged as an analysis-stage dead end.
func (s *Store) AnalystFail(id int64, problemDesc string) error {
	analyst, _ := s.names(id)
	if err := s.update(id, "status = 'problem', problem_stage = 'analyst', problem_desc = ?", problemDesc); err != nil {
		return err
	}
	s.LogEvent(analyst, "analyst_fail", id)
	return nil
}

// ImplSuccess ("Мужик"): records the verdict on the analyst and the real
// solution, then archives the task to Сделано.
func (s *Store) ImplSuccess(id int64, analystLied bool, solution string) error {
	_, dev := s.names(id)
	lied := 0
	if analystLied {
		lied = 1
	}
	if err := s.update(id, "status = 'done', analyst_lied = ?, solution_desc = ?, closed_at = ?",
		lied, solution, nowStr()); err != nil {
		return err
	}
	s.LogEvent(dev, "impl_success", id)
	return nil
}

// ImplFail ("Лох"): records the problem and moves the task to Проблемы, tagged
// as an implementation-stage dead end.
func (s *Store) ImplFail(id int64, problemDesc string) error {
	_, dev := s.names(id)
	if err := s.update(id, "status = 'problem', problem_stage = 'impl', problem_desc = ?", problemDesc); err != nil {
		return err
	}
	s.LogEvent(dev, "impl_fail", id)
	return nil
}

// DeleteTask removes a task and (via ON DELETE CASCADE) its comments/attachments.
func (s *Store) DeleteTask(id int64) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	return err
}

// ---- activity log / stats ----

// LogEvent records a per-person activity event (best-effort).
func (s *Store) LogEvent(person, action string, taskID int64) {
	if person == "" {
		return
	}
	s.db.Exec(`INSERT INTO events (ts, person, action, task_id) VALUES (?, ?, ?, ?)`,
		nowStr(), person, action, taskID)
}

// names returns the current analyst/dev assignee names for a task.
func (s *Store) names(id int64) (analyst, dev string) {
	s.db.QueryRow(`SELECT analyst_name, dev_name FROM tasks WHERE id = ?`, id).Scan(&analyst, &dev)
	return
}

// PersonStats aggregates a person's activity over the last `since`.
type PersonStats struct {
	Took     int // взял в работу (start)
	Analyzed int // проанализировал (analyst_success)
	Realized int // реализовал (impl_success)
	Failed   int // не выполнил (analyst_fail + impl_fail)
	Panicked int // сгорело очко (panic)
}

// StatsFor returns a person's activity counts since the given time.
func (s *Store) StatsFor(person string, since time.Time) (PersonStats, error) {
	var ps PersonStats
	rows, err := s.db.Query(`SELECT action, COUNT(*) FROM events
		WHERE person = ? AND ts >= ? GROUP BY action`, person, since.UTC().Format(tsLayout))
	if err != nil {
		return ps, err
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		var n int
		if err := rows.Scan(&action, &n); err != nil {
			return ps, err
		}
		switch action {
		case "start_analyst", "start_impl":
			ps.Took += n
		case "analyst_success":
			ps.Analyzed += n
		case "impl_success":
			ps.Realized += n
		case "analyst_fail", "impl_fail":
			ps.Failed += n
		case "panic":
			ps.Panicked += n
		}
	}
	return ps, rows.Err()
}

// NextNew returns the highest-priority unassigned task (urgent → normal → low,
// oldest first), or (nil, nil) if Новые задачи is empty.
func (s *Store) NextNew() (*model.Task, error) {
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE status = 'unassigned'
		ORDER BY CASE importance WHEN 'urgent' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END,
		created_at ASC LIMIT 1`, taskCols)
	t, err := scanTask(s.db.QueryRow(q))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return t, nil
}

// SearchTasks finds tasks whose title or description contains q, newest first.
func (s *Store) SearchTasks(q string, limit int) ([]*model.Task, error) {
	like := "%" + q + "%"
	query := fmt.Sprintf(`SELECT %s FROM tasks WHERE title LIKE ? OR description LIKE ?
		ORDER BY created_at DESC LIMIT ?`, taskCols)
	return s.queryTasks(query, like, like, limit)
}

// TasksByStatuses returns tasks in any of the given statuses, newest first.
func (s *Store) TasksByStatuses(statuses ...string) ([]*model.Task, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(statuses)), ",")
	args := make([]any, len(statuses))
	for i, st := range statuses {
		args[i] = st
	}
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE status IN (%s) ORDER BY created_at DESC`, taskCols, ph)
	return s.queryTasks(q, args...)
}

// MyActiveTasks returns the tasks currently on a person's plate (their stage,
// idle/wip/help), oldest first.
func (s *Store) MyActiveTasks(name string) ([]*model.Task, error) {
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE
		(analyst_name = ? AND status IN ('analyst_idle','analyst_wip','analyst_help'))
		OR (dev_name = ? AND status IN ('impl_idle','impl_wip','impl_help'))
		ORDER BY work_started ASC, created_at ASC`, taskCols)
	return s.queryTasks(q, name, name)
}

// CountRow is a (label, count) aggregate row.
type CountRow struct {
	Label string
	Count int
}

func (s *Store) countRows(query string, args ...any) ([]CountRow, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CountRow
	for rows.Next() {
		var r CountRow
		if err := rows.Scan(&r.Label, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// VozduhCounts returns, per analyst, how many done tasks had the analyst marked
// as having lied ("воздухан"), most first.
func (s *Store) VozduhCounts() ([]CountRow, error) {
	return s.countRows(`SELECT analyst_name, COUNT(*) FROM tasks
		WHERE analyst_lied = 1 AND analyst_name != '' GROUP BY analyst_name ORDER BY COUNT(*) DESC`)
}

// BackendBugCounts returns task counts per backend subtype (parsing/matching/
// trading), most first.
func (s *Store) BackendBugCounts() ([]CountRow, error) {
	return s.countRows(`SELECT backend_kind, COUNT(*) FROM tasks
		WHERE category = 'backend' AND backend_kind != '' GROUP BY backend_kind ORDER BY COUNT(*) DESC`)
}

// CategoryCounts returns task counts per category, most first.
func (s *Store) CategoryCounts() ([]CountRow, error) {
	return s.countRows(`SELECT category, COUNT(*) FROM tasks GROUP BY category ORDER BY COUNT(*) DESC`)
}

// CountCreatedSince counts tasks created at/after since.
func (s *Store) CountCreatedSince(since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE created_at >= ?`,
		since.UTC().Format(tsLayout)).Scan(&n)
	return n, err
}

// CountDoneBetween counts tasks closed (status done) with closed_at in [from,to).
func (s *Store) CountDoneBetween(from, to time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status='done' AND closed_at >= ? AND closed_at < ?`,
		from.UTC().Format(tsLayout), to.UTC().Format(tsLayout)).Scan(&n)
	return n, err
}

// CountEventsSince counts events of the given actions at/after since.
func (s *Store) CountEventsSince(since time.Time, actions ...string) (int, error) {
	if len(actions) == 0 {
		return 0, nil
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(actions)), ",")
	args := []any{since.UTC().Format(tsLayout)}
	for _, a := range actions {
		args = append(args, a)
	}
	var n int
	err := s.db.QueryRow(fmt.Sprintf(
		`SELECT COUNT(*) FROM events WHERE ts >= ? AND action IN (%s)`, ph), args...).Scan(&n)
	return n, err
}

// LastEventTime returns the time of a person's most recent event.
func (s *Store) LastEventTime(person string) (time.Time, bool, error) {
	var ts string
	err := s.db.QueryRow(`SELECT ts FROM events WHERE person = ? ORDER BY ts DESC LIMIT 1`, person).Scan(&ts)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return parseTime(ts), true, nil
}

// ---- news feed (Интересные факты) ----

// AddNews inserts a feed entry and returns its id.
func (s *Store) AddNews(n *model.News) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO news (author_tg, author_name, text, created_at)
		VALUES (?, ?, ?, ?)`, n.AuthorTg, n.AuthorName, n.Text, nowStr())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListNews returns feed entries, newest first (limit<=0 → 100).
func (s *Store) ListNews(limit int) ([]*model.News, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, author_tg, author_name, text, created_at
		FROM news ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.News
	for rows.Next() {
		var n model.News
		var created string
		if err := rows.Scan(&n.ID, &n.AuthorTg, &n.AuthorName, &n.Text, &created); err != nil {
			return nil, err
		}
		n.CreatedAt = parseTime(created)
		out = append(out, &n)
	}
	return out, rows.Err()
}

// GetNews returns a single feed entry, or (nil, nil) if absent.
func (s *Store) GetNews(id int64) (*model.News, error) {
	var n model.News
	var created string
	err := s.db.QueryRow(`SELECT id, author_tg, author_name, text, created_at
		FROM news WHERE id = ?`, id).Scan(&n.ID, &n.AuthorTg, &n.AuthorName, &n.Text, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.CreatedAt = parseTime(created)
	return &n, nil
}

// DeleteNews removes a feed entry.
func (s *Store) DeleteNews(id int64) error {
	_, err := s.db.Exec(`DELETE FROM news WHERE id = ?`, id)
	return err
}

// ---- settings ----

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO app_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// UpdateTask patches editable descriptive fields.
func (s *Store) UpdateTask(id int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]bool{
		"title": true, "description": true, "category": true,
		"backend_kind": true, "importance": true, "analyst_notes": true,
	}
	var sets []string
	var args []any
	for k, v := range fields {
		if !allowed[k] {
			continue
		}
		sets = append(sets, k+" = ?")
		args = append(args, v)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, nowStr(), id)
	_, err := s.db.Exec(
		fmt.Sprintf(`UPDATE tasks SET %s WHERE id = ?`, strings.Join(sets, ", ")), args...)
	return err
}

// ---- Comments ----

func (s *Store) AddComment(c *model.Comment) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO comments (task_id, author_tg, author_role, text, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		c.TaskID, c.AuthorTg, string(c.AuthorRole), c.Text, nowStr())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) listComments(taskID int64) ([]model.Comment, error) {
	rows, err := s.db.Query(`
		SELECT id, task_id, author_tg, author_role, text, created_at
		FROM comments WHERE task_id = ? ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Comment
	for rows.Next() {
		var c model.Comment
		var role, created string
		if err := rows.Scan(&c.ID, &c.TaskID, &c.AuthorTg, &role, &c.Text, &created); err != nil {
			return nil, err
		}
		c.AuthorRole = model.Role(role)
		c.CreatedAt = parseTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- Attachments ----

func (s *Store) AddAttachment(a *model.Attachment) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO attachments (task_id, tg_file_id, origin, caption, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		a.TaskID, a.TgFileID, string(a.Origin), a.Caption, nowStr())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) listAttachments(taskID int64) ([]model.Attachment, error) {
	rows, err := s.db.Query(`
		SELECT id, task_id, tg_file_id, origin, caption, created_at
		FROM attachments WHERE task_id = ? ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Attachment
	for rows.Next() {
		var a model.Attachment
		var origin, created string
		if err := rows.Scan(&a.ID, &a.TaskID, &a.TgFileID, &origin, &a.Caption, &created); err != nil {
			return nil, err
		}
		a.Origin = model.AttachmentOrigin(origin)
		a.CreatedAt = parseTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}
