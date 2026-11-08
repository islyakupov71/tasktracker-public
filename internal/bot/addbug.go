package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tasktracker/internal/model"
)

// handleConversation routes free-text input while a conversation is active.
func (b *Bot) handleConversation(ctx context.Context, m *Message, user *model.User, st *convState) {
	uid := m.From.ID
	chat := m.Chat.ID
	text := strings.TrimSpace(m.Text)

	switch st.step {
	case stepTitle:
		if text == "" {
			b.api.SendMessage(ctx, chat, "Заголовок не может быть пустым. Введите заголовок:", nil)
			return
		}
		st.draft.Title = text
		st.step = stepDesc
		b.api.SendMessage(ctx, chat, "Теперь подробное описание (что происходит, шаги, ожидаемое поведение):", nil)

	case stepDesc:
		st.draft.Description = text
		st.step = stepCategory
		b.api.SendMessage(ctx, chat, "К какой части относится баг?", categoryKeyboard())

	case stepScreens:
		if text == "/skip" {
			b.finishNewBug(ctx, chat, uid, st)
			return
		}
		b.api.SendMessage(ctx, chat, "Пришлите скриншот(ы) или /skip для завершения.", nil)
	}
}

// handlePhoto collects screenshots during a conversation.
func (b *Bot) handlePhoto(ctx context.Context, m *Message) {
	uid := m.From.ID
	chat := m.Chat.ID
	st := b.state(uid)
	if st.step != stepScreens {
		return // ignore stray photos
	}
	// Telegram sends multiple sizes; the last is the largest. Download and store
	// it locally so screenshots render reliably and travel with the data; fall
	// back to the Telegram file_id if the download fails.
	fileID := m.Photo[len(m.Photo)-1].FileID
	ref, err := b.saveScreenshot(ctx, fileID)
	if err != nil {
		ref = fileID
	}
	st.files = append(st.files, ref)
	b.api.SendMessage(ctx, chat,
		fmt.Sprintf("Скриншот добавлен (%d). Ещё фото или /skip для завершения.", len(st.files)), nil)
}

// saveScreenshot downloads a Telegram photo by file_id and stores it locally,
// returning a "local:<name>" reference for the attachment.
func (b *Bot) saveScreenshot(ctx context.Context, fileID string) (string, error) {
	data, ext, err := b.api.DownloadFile(ctx, fileID)
	if err != nil {
		return "", err
	}
	var rnd [12]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	name := hex.EncodeToString(rnd[:]) + ext
	if err := os.WriteFile(filepath.Join(b.uploadsDir, name), data, 0o644); err != nil {
		return "", err
	}
	return "local:" + name, nil
}

func (b *Bot) askScreens(ctx context.Context, chat int64) {
	b.api.SendMessage(ctx, chat, "Пришлите скриншот(ы) или /skip, если их нет.", nil)
}

// finishNewBug persists the drafted bug plus its screenshots.
func (b *Bot) finishNewBug(ctx context.Context, chat, uid int64, st *convState) {
	id, err := b.st.CreateTask(&st.draft)
	if err != nil {
		b.api.SendMessage(ctx, chat, "Ошибка сохранения задачи: "+err.Error(), nil)
		st.step = stepNone
		return
	}
	for _, f := range st.files {
		b.st.AddAttachment(&model.Attachment{TaskID: id, TgFileID: f, Origin: model.OriginReport})
	}
	cat := st.draft.Category.RU()
	if st.draft.BackendKind != "" {
		cat += " / " + st.draft.BackendKind.RU()
	}
	imp := st.draft.Importance
	if !imp.Valid() {
		imp = model.ImportanceNormal
	}
	st.step = stepNone
	st.files = nil
	b.api.SendMessage(ctx, chat, fmt.Sprintf(
		"✅ <b>Баг #%d создан</b> (%s)\n🏷 %s", id, imp.Label(), cat), nil)
	// Announce the new unassigned task to everyone (tracked so we can reply when
	// it gets picked up).
	b.AnnounceNewTask(id)
	b.sendMainMenu(ctx, chat, "Готово 👍")
}
