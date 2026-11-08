package bot

import (
	"context"
	"strings"

	"tasktracker/internal/model"
)

func (b *Bot) handleCallback(ctx context.Context, cq *CallbackQuery) {
	if cq.From == nil || cq.Message == nil {
		return
	}
	uid := cq.From.ID
	chat := cq.Message.Chat.ID
	data := cq.Data
	// Always ack so the client stops the spinner.
	defer b.api.AnswerCallbackQuery(ctx, cq.ID, "")

	switch {
	case strings.HasPrefix(data, "cat:"):
		cat := model.Category(strings.TrimPrefix(data, "cat:"))
		if !cat.Valid() {
			return
		}
		st := b.state(uid)
		st.draft.Category = cat
		if cat == model.CategoryBackend {
			st.step = stepBackend
			b.api.SendMessage(ctx, chat, "Тип бэкенд-ошибки:", backendKindKeyboard())
			return
		}
		st.step = stepImportance
		b.api.SendMessage(ctx, chat, "Насколько срочно?", importanceKeyboard())

	case strings.HasPrefix(data, "bk:"):
		bk := model.BackendKind(strings.TrimPrefix(data, "bk:"))
		if !bk.Valid() {
			return
		}
		st := b.state(uid)
		st.draft.BackendKind = bk
		st.step = stepImportance
		b.api.SendMessage(ctx, chat, "Насколько срочно?", importanceKeyboard())

	case strings.HasPrefix(data, "imp:"):
		imp := model.Importance(strings.TrimPrefix(data, "imp:"))
		if !imp.Valid() {
			return
		}
		st := b.state(uid)
		st.draft.Importance = imp
		st.step = stepScreens
		b.askScreens(ctx, chat)
	}
}

func categoryKeyboard() InlineKeyboardMarkup {
	return InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "🎨 Frontend", CallbackData: "cat:frontend"},
		{Text: "⚙️ Backend", CallbackData: "cat:backend"},
		{Text: "🖥 Server", CallbackData: "cat:server"},
	}}}
}

func backendKindKeyboard() InlineKeyboardMarkup {
	return InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "Парсинг", CallbackData: "bk:parsing"},
		{Text: "Мэтчинг", CallbackData: "bk:matching"},
		{Text: "Торговля", CallbackData: "bk:trading"},
	}}}
}

func importanceKeyboard() InlineKeyboardMarkup {
	return InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "🔴 СРОЧНО", CallbackData: "imp:urgent"},
		{Text: "🟡 НОРМ", CallbackData: "imp:normal"},
		{Text: "🟢 НЕ СРОЧНО", CallbackData: "imp:low"},
	}}}
}
