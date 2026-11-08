-- Schema for the task tracker. Applied idempotently on every startup.
-- Column additions for existing databases are handled by ensureColumn() in
-- store.go (ALTER TABLE ... ADD COLUMN), so adding a column here must be mirrored
-- there.

CREATE TABLE IF NOT EXISTS users (
    tg_id      INTEGER PRIMARY KEY,
    username   TEXT NOT NULL DEFAULT '',
    role       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    category      TEXT NOT NULL,
    backend_kind  TEXT NOT NULL DEFAULT '',
    importance    TEXT NOT NULL DEFAULT 'normal',
    status        TEXT NOT NULL DEFAULT 'unassigned',
    analyst_name  TEXT NOT NULL DEFAULT '',
    dev_name      TEXT NOT NULL DEFAULT '',
    analyst_notes TEXT NOT NULL DEFAULT '',
    problem_stage TEXT NOT NULL DEFAULT '',
    problem_desc  TEXT NOT NULL DEFAULT '',
    analyst_lied  INTEGER NOT NULL DEFAULT 0,
    solution_desc TEXT NOT NULL DEFAULT '',
    created_by    INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    closed_at     TEXT,
    work_started  TEXT,
    last_reminder TEXT
);

CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks (status);

-- Announcement messages the bot posted for a task (so it can reply to them, e.g.
-- "АНАЛИЗИРУЕТ УЧАСТНИК 01" when the task is picked up).
CREATE TABLE IF NOT EXISTS task_announcements (
    task_id    INTEGER NOT NULL,
    chat_id    INTEGER NOT NULL,
    message_id INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ann_task ON task_announcements (task_id);

CREATE TABLE IF NOT EXISTS comments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author_tg   INTEGER NOT NULL DEFAULT 0,
    author_role TEXT NOT NULL DEFAULT '',
    text        TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_comments_task ON comments (task_id);

CREATE TABLE IF NOT EXISTS attachments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    tg_file_id TEXT NOT NULL,
    origin     TEXT NOT NULL DEFAULT 'report',
    caption    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_attachments_task ON attachments (task_id);

-- Per-person activity log (for /member1, /member2 24h stats).
CREATE TABLE IF NOT EXISTS events (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    ts      TEXT NOT NULL,
    person  TEXT NOT NULL,
    action  TEXT NOT NULL,
    task_id INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_events_person_ts ON events (person, ts);

-- Simple key/value config (e.g. the bound group chat id for notifications).
CREATE TABLE IF NOT EXISTS app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- "Интересные факты" feed: ideas, findings and notes worth reading that are not
-- tasks. A lightweight news/timeline shared by the team.
CREATE TABLE IF NOT EXISTS news (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    author_tg   INTEGER NOT NULL DEFAULT 0,
    author_name TEXT NOT NULL DEFAULT '',
    text        TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_news_created ON news (created_at);
