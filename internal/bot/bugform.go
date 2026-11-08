package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tasktracker/internal/model"
)

// One-shot bug submission via a copy-paste template. Unlike the conversational
// FSM (➕ Добавить баг), this works in the group too: the bot posts a fillable
// template on /баг, and any later message (text or photo caption) that starts
// with the #баг marker is parsed into a task in a single step.

// bugFormMarker is the sentinel that opens a filled-in template.
const bugFormMarkerRU = "#баг"
const bugFormMarkerEN = "#bug"

// bugTemplate is the copy-paste form. Wrapped in <pre> so Telegram shows a
// one-tap "copy" affordance on the block.
const bugTemplate = bugFormMarkerRU + "\n" +
	"Важность: срочно / норм / не срочно\n" +
	"Категория: фронт / бэк / сервер\n" +
	"Тип: парсинг / мэтчинг / торговля\n" +
	"Заголовок: \n" +
	"Описание: "

// sendBugTemplate replies with the fillable bug form.
func (b *Bot) sendBugTemplate(ctx context.Context, chat int64) {
	text := "🐞 <b>Новый баг — шаблон</b>\n" +
		"Скопируй блок ниже, заполни (важность, категория, заголовок, описание) и отправь в чат. " +
		"Можно сразу <b>с фото</b> — приложи скрин, а текст вставь в подпись.\n\n" +
		"<pre>" + htmlEsc(bugTemplate) + "</pre>"
	b.api.SendMessage(ctx, chat, text, nil)
}

// isBugForm reports whether a message body is a filled-in bug template (its
// first non-empty line starts with the #баг / #bug marker).
func isBugForm(body string) bool {
	for _, raw := range strings.Split(body, "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, bugFormMarkerRU) || strings.HasPrefix(line, bugFormMarkerEN)
	}
	return false
}

// parseBugForm turns a filled-in template into a Task. Importance defaults to
// НОРМ and category to Фронтенд when left as the unedited placeholder. Returns
// an error only when there is no title.
func parseBugForm(body string) (*model.Task, error) {
	t := &model.Task{Importance: model.ImportanceNormal}
	var desc []string
	inDesc := false
	for _, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if inDesc {
			desc = append(desc, raw)
			continue
		}
		line := strings.TrimSpace(raw)
		low := strings.ToLower(line)
		if strings.HasPrefix(low, bugFormMarkerRU) || strings.HasPrefix(low, bugFormMarkerEN) {
			continue
		}
		label, val, ok := splitLabel(line)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(label, "важн"):
			if imp := parseImportance(val); imp != "" {
				t.Importance = imp
			}
		case strings.HasPrefix(label, "катег"):
			if c := parseCategory(val); c != "" {
				t.Category = c
			}
		case strings.HasPrefix(label, "тип") || strings.HasPrefix(label, "подтип"):
			if bk := parseBackendKind(val); bk != "" {
				t.BackendKind = bk
			}
		case strings.HasPrefix(label, "заголов") || strings.HasPrefix(label, "тема") || strings.HasPrefix(label, "title"):
			t.Title = val
		case strings.HasPrefix(label, "опис"):
			if val != "" {
				desc = append(desc, val)
			}
			inDesc = true
		}
	}
	t.Description = strings.TrimSpace(strings.Join(desc, "\n"))
	if strings.TrimSpace(t.Title) == "" {
		return nil, errors.New("no title")
	}
	if !t.Category.Valid() {
		t.Category = model.CategoryFrontend
	}
	if t.Category != model.CategoryBackend {
		t.BackendKind = ""
	}
	return t, nil
}

// splitLabel splits "Label: value" into a lowercased label and a value.
func splitLabel(line string) (label, val string, ok bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(line[:i])), strings.TrimSpace(line[i+1:]), true
}

// optionField reports whether a value still holds the multi-option placeholder
// (e.g. "срочно / норм / не срочно"); such lines are treated as not-filled.
func optionField(val string) bool { return strings.ContainsAny(val, "/|") }

func parseImportance(val string) model.Importance {
	if optionField(val) {
		return ""
	}
	low := strings.ToLower(val)
	switch {
	case strings.Contains(low, "не сроч"), strings.Contains(low, "низ"), strings.Contains(low, "low"):
		return model.ImportanceLow
	case strings.Contains(low, "сроч"), strings.Contains(low, "urgent"), strings.Contains(low, "горит"):
		return model.ImportanceUrgent
	case strings.Contains(low, "норм"), strings.Contains(low, "normal"), strings.Contains(low, "обыч"):
		return model.ImportanceNormal
	}
	return ""
}

func parseCategory(val string) model.Category {
	if optionField(val) {
		return ""
	}
	low := strings.ToLower(val)
	switch {
	case strings.Contains(low, "фронт"), strings.Contains(low, "front"):
		return model.CategoryFrontend
	case strings.Contains(low, "бэк"), strings.Contains(low, "бек"), strings.Contains(low, "back"):
		return model.CategoryBackend
	case strings.Contains(low, "сервер"), strings.Contains(low, "server"):
		return model.CategoryServer
	}
	return ""
}

func parseBackendKind(val string) model.BackendKind {
	if optionField(val) {
		return ""
	}
	low := strings.ToLower(val)
	switch {
	case strings.Contains(low, "парс"), strings.Contains(low, "pars"):
		return model.BackendParsing
	case strings.Contains(low, "мэтч"), strings.Contains(low, "матч"), strings.Contains(low, "match"):
		return model.BackendMatching
	case strings.Contains(low, "торг"), strings.Contains(low, "trad"):
		return model.BackendTrading
	}
	return ""
}

// submitBugForm parses a filled-in template (optionally with a photo), creates
// the task, attaches the screenshot, announces it, and confirms in the chat.
func (b *Bot) submitBugForm(ctx context.Context, m *Message, body string) {
	chat := m.Chat.ID
	t, err := parseBugForm(body)
	if err != nil {
		b.api.SendMessage(ctx, chat,
			"⚠️ Не вижу <b>Заголовок:</b> в заявке. Заполни поле и пришли снова — шаблон по /баг.", nil)
		return
	}
	t.CreatedBy = m.From.ID
	id, err := b.st.CreateTask(t)
	if err != nil {
		b.api.SendMessage(ctx, chat, "❌ Не удалось сохранить задачу: "+htmlEsc(err.Error()), nil)
		return
	}
	// Attach the photo, if the form arrived as a photo caption.
	if len(m.Photo) > 0 {
		fileID := m.Photo[len(m.Photo)-1].FileID
		ref, derr := b.saveScreenshot(ctx, fileID)
		if derr != nil {
			ref = fileID
		}
		b.st.AddAttachment(&model.Attachment{TaskID: id, TgFileID: ref, Origin: model.OriginReport})
	}
	cat := t.Category.RU()
	if t.BackendKind != "" {
		cat += " / " + t.BackendKind.RU()
	}
	b.api.SendMessage(ctx, chat, fmt.Sprintf(
		"✅ <b>Задача #%d создана</b>\n%s <b>%s</b>\n🏷 %s",
		id, t.Importance.Emoji(), htmlEsc(t.Title), cat), nil)
	b.AnnounceNewTask(id)
}
