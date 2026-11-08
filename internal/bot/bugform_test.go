package bot

import (
	"testing"

	"tasktracker/internal/model"
)

func TestIsBugForm(t *testing.T) {
	cases := map[string]bool{
		"#баг\nЗаголовок: x":     true,
		"  \n#bug\nЗаголовок: x": true,
		"#БАГ":                   true,
		"привет, это не заявка":  false,
		"/баг":                   false, // command, not a filled form
	}
	for in, want := range cases {
		if got := isBugForm(in); got != want {
			t.Errorf("isBugForm(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseBugForm_Full(t *testing.T) {
	body := "#баг\n" +
		"Важность: срочно\n" +
		"Категория: бэк\n" +
		"Тип: парсинг\n" +
		"Заголовок: Котировки не обновляются\n" +
		"Описание: Висят старые цены\nбольше минуты"
	task, err := parseBugForm(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.Importance != model.ImportanceUrgent {
		t.Errorf("importance = %q, want urgent", task.Importance)
	}
	if task.Category != model.CategoryBackend {
		t.Errorf("category = %q, want backend", task.Category)
	}
	if task.BackendKind != model.BackendParsing {
		t.Errorf("backend_kind = %q, want parsing", task.BackendKind)
	}
	if task.Title != "Котировки не обновляются" {
		t.Errorf("title = %q", task.Title)
	}
	if task.Description != "Висят старые цены\nбольше минуты" {
		t.Errorf("description = %q", task.Description)
	}
}

func TestParseBugForm_UneditedPlaceholders(t *testing.T) {
	// User left the option lines untouched and only typed a title in place.
	body := "#баг\n" +
		"Важность: срочно / норм / не срочно\n" +
		"Категория: фронт / бэк / сервер\n" +
		"Тип: парсинг / мэтчинг / торговля\n" +
		"Заголовок: Кнопка не нажимается\n" +
		"Описание: "
	task, err := parseBugForm(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.Importance != model.ImportanceNormal {
		t.Errorf("importance = %q, want normal (default)", task.Importance)
	}
	if task.Category != model.CategoryFrontend {
		t.Errorf("category = %q, want frontend (default)", task.Category)
	}
	if task.BackendKind != "" {
		t.Errorf("backend_kind = %q, want empty (non-backend)", task.BackendKind)
	}
	if task.Title != "Кнопка не нажимается" {
		t.Errorf("title = %q", task.Title)
	}
}

func TestParseBugForm_NoTitle(t *testing.T) {
	if _, err := parseBugForm("#баг\nОписание: что-то"); err == nil {
		t.Error("expected error when title is missing")
	}
}
