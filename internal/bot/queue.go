package bot

// The task lifecycle (take / panic / outcome / archive) is handled entirely in
// the Mini App via the web API. The bot's former "Принять задачу" / "Отметить
// выполненной" flows were removed; this file is intentionally minimal and kept
// as a home for any future bot-side queue helpers (e.g. the planned group
// "/запрос" command).
