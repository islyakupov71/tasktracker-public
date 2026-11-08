// Package model holds the canonical domain types for the task tracker.
package model

import "time"

// Role is a light onboarding label chosen on /start; the per-task lifecycle is
// driven by Status + the assignee names, not by Role.
type Role string

const (
	RoleAnalyst   Role = "analyst"
	RoleDeveloper Role = "developer"
)

func (r Role) Valid() bool { return r == RoleAnalyst || r == RoleDeveloper }

func (r Role) RU() string {
	switch r {
	case RoleAnalyst:
		return "Аналитик"
	case RoleDeveloper:
		return "Реализатор"
	}
	return string(r)
}

// Name is an assignee. There are exactly two people.
const (
	NameMemberOne = "Участник 01"
	NameMemberTwo = "Участник 02"
)

// ValidName reports whether s is one of the two allowed assignee names.
func ValidName(s string) bool { return s == NameMemberOne || s == NameMemberTwo }

// Importance buckets a task into one of three priority columns.
type Importance string

const (
	ImportanceUrgent Importance = "urgent" // СРОЧНО
	ImportanceNormal Importance = "normal" // НОРМ
	ImportanceLow    Importance = "low"    // НЕ СРОЧНО
)

func (i Importance) Valid() bool {
	return i == ImportanceUrgent || i == ImportanceNormal || i == ImportanceLow
}

func (i Importance) RU() string {
	switch i {
	case ImportanceUrgent:
		return "СРОЧНО"
	case ImportanceNormal:
		return "НОРМ"
	case ImportanceLow:
		return "НЕ СРОЧНО"
	}
	return string(i)
}

// Emoji returns the traffic-light marker for the importance, used to make
// notifications scannable at a glance.
func (i Importance) Emoji() string {
	switch i {
	case ImportanceUrgent:
		return "🔴"
	case ImportanceNormal:
		return "🟡"
	case ImportanceLow:
		return "🟢"
	}
	return "⚪️"
}

// Label combines the emoji and RU text, e.g. "🔴 СРОЧНО".
func (i Importance) Label() string { return i.Emoji() + " " + i.RU() }

// Category is the high-level area a bug lives in.
type Category string

const (
	CategoryFrontend Category = "frontend"
	CategoryBackend  Category = "backend"
	CategoryServer   Category = "server"
)

func (c Category) Valid() bool {
	return c == CategoryFrontend || c == CategoryBackend || c == CategoryServer
}

func (c Category) RU() string {
	switch c {
	case CategoryFrontend:
		return "Фронтенд"
	case CategoryBackend:
		return "Бэкенд"
	case CategoryServer:
		return "Сервер"
	}
	return string(c)
}

// BackendKind narrows a backend bug. Empty unless Category == backend.
type BackendKind string

const (
	BackendParsing  BackendKind = "parsing"
	BackendMatching BackendKind = "matching"
	BackendTrading  BackendKind = "trading"
)

func (b BackendKind) Valid() bool {
	return b == BackendParsing || b == BackendMatching || b == BackendTrading
}

func (b BackendKind) RU() string {
	switch b {
	case BackendParsing:
		return "Парсинг"
	case BackendMatching:
		return "Мэтчинг"
	case BackendTrading:
		return "Торговля"
	}
	return string(b)
}

// Stage is the pipeline phase a task is being worked in.
type Stage string

const (
	StageAnalyst Stage = "analyst" // Аналитика
	StageImpl    Stage = "impl"    // Реализация
)

func (s Stage) Valid() bool { return s == StageAnalyst || s == StageImpl }

func (s Stage) RU() string {
	switch s {
	case StageAnalyst:
		return "Аналитика"
	case StageImpl:
		return "Реализация"
	}
	return string(s)
}

// Status is the single source of truth for a task's position in the lifecycle
// and which page it shows on.
//
//	unassigned ─assign(name)→ analyst_idle ─start→ analyst_wip ⇄ analyst_help
//	  analyst_wip/help ─Мужик(implName)→ impl_idle ─start→ impl_wip ⇄ impl_help
//	  analyst_wip/help ─Лох(problem)───→ problem (stage=analyst)
//	  impl_wip/help    ─Мужик(verdict)─→ done
//	  impl_wip/help    ─Лох(problem)───→ problem (stage=impl)
//	  problem ─reopen→ unassigned
type Status string

const (
	StatusUnassigned  Status = "unassigned"   // Новые задачи: не распределена
	StatusAnalystIdle Status = "analyst_idle" // Аналитика: назначена, не начата
	StatusAnalystWIP  Status = "analyst_wip"  // Аналитика: в работе
	StatusAnalystHelp Status = "analyst_help" // Аналитика: горит очко (нужна помощь)
	StatusImplIdle    Status = "impl_idle"    // Реализация: назначена, не начата
	StatusImplWIP     Status = "impl_wip"     // Реализация: в работе
	StatusImplHelp    Status = "impl_help"    // Реализация: горит очко
	StatusDone        Status = "done"         // Сделано
	StatusProblem     Status = "problem"      // Проблемы
)

func (s Status) RU() string {
	switch s {
	case StatusUnassigned:
		return "Не распределена"
	case StatusAnalystIdle, StatusImplIdle:
		return "Не начата"
	case StatusAnalystWIP, StatusImplWIP:
		return "В работе"
	case StatusAnalystHelp, StatusImplHelp:
		return "Горит очко 🔥"
	case StatusDone:
		return "Сделано ✅"
	case StatusProblem:
		return "Проблема ❌"
	}
	return string(s)
}

// IsHelp reports whether the task is in a "горит очко" state.
func (s Status) IsHelp() bool { return s == StatusAnalystHelp || s == StatusImplHelp }

// Window selects which page's subset of tasks to show.
type Window string

const (
	WindowNew      Window = "new"      // Новые задачи
	WindowAnalyst  Window = "analyst"  // Аналитика
	WindowImpl     Window = "impl"     // Реализация
	WindowDone     Window = "done"     // Сделано
	WindowProblems Window = "problems" // Проблемы
)

func (w Window) Valid() bool {
	switch w {
	case WindowNew, WindowAnalyst, WindowImpl, WindowDone, WindowProblems:
		return true
	}
	return false
}

// User is a Telegram user with an assigned pipeline role.
type User struct {
	TgID      int64     `json:"tg_id"`
	Username  string    `json:"username"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Task is a single bug travelling through Новые → Аналитика → Реализация →
// Сделано (or Проблемы on a dead end).
type Task struct {
	ID          int64       `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Category    Category    `json:"category"`
	BackendKind BackendKind `json:"backend_kind,omitempty"`
	Importance  Importance  `json:"importance"`
	Status      Status      `json:"status"`
	// AnalystName / DevName are the chosen assignees (Участник 01|Участник 02|""). AnalystName
	// is set when the task is distributed; DevName is chosen by the analyst when
	// handing off to Реализация.
	AnalystName  string `json:"analyst_name"`
	DevName      string `json:"dev_name"`
	AnalystNotes string `json:"analyst_notes"` // новые вводные / пути решения
	// ProblemStage / ProblemDesc are set when a task lands on the Проблемы page.
	ProblemStage Stage  `json:"problem_stage,omitempty"`
	ProblemDesc  string `json:"problem_desc,omitempty"`
	// AnalystLied / SolutionDesc are recorded by the implementer on success.
	AnalystLied  bool   `json:"analyst_lied"`
	SolutionDesc string `json:"solution_desc,omitempty"`

	CreatedBy int64      `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	// WorkStarted is stamped when the task is taken into work on its current
	// stage; LastReminder tracks the last nag so the scheduler doesn't spam.
	WorkStarted  *time.Time `json:"work_started,omitempty"`
	LastReminder *time.Time `json:"last_reminder,omitempty"`

	// Hydrated on detail reads.
	Comments    []Comment    `json:"comments,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// News is one entry in the "Интересные факты" feed: an idea, finding or note
// worth reading that is not a task.
type News struct {
	ID         int64     `json:"id"`
	AuthorTg   int64     `json:"author_tg"`
	AuthorName string    `json:"author_name"`
	Text       string    `json:"text"`
	CreatedAt  time.Time `json:"created_at"`
}

// Comment is free-text discussion attached to a task.
type Comment struct {
	ID         int64     `json:"id"`
	TaskID     int64     `json:"task_id"`
	AuthorTg   int64     `json:"author_tg"`
	AuthorRole Role      `json:"author_role"`
	Text       string    `json:"text"`
	CreatedAt  time.Time `json:"created_at"`
}

// AttachmentOrigin records who attached a screenshot.
type AttachmentOrigin string

const (
	OriginReport  AttachmentOrigin = "report"  // attached when the bug was created
	OriginAnalyst AttachmentOrigin = "analyst" // attached by the analyst with the solution
)

// Attachment is a Telegram-hosted screenshot referenced by file_id.
type Attachment struct {
	ID        int64            `json:"id"`
	TaskID    int64            `json:"task_id"`
	TgFileID  string           `json:"tg_file_id"`
	Origin    AttachmentOrigin `json:"origin"`
	Caption   string           `json:"caption,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}
