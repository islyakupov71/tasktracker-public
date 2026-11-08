package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tasktracker/internal/model"

	"github.com/rs/zerolog/log"
)

// The "Интересные факты" feed: ideas, findings and notes worth reading that are
// not tasks. Added via /news <текст> (or the Mini App), shown by bare /news.

// newsAuthor resolves a display name for a feed author from their Telegram
// username (friendly team name if known, else @username, else "кто-то").
func newsAuthor(username string) string {
	if n := nameFromUsername(username); n != "" {
		return n
	}
	if username != "" {
		return "@" + strings.TrimPrefix(username, "@")
	}
	return "кто-то"
}

// handleNews routes the /news command: with text it adds a feed entry, bare it
// shows the latest entries.
func (b *Bot) handleNews(ctx context.Context, chat, uid int64, username, args string) {
	text := strings.TrimSpace(args)
	if text == "" {
		b.sendNewsFeed(ctx, chat)
		return
	}
	author := newsAuthor(username)
	id, err := b.st.AddNews(&model.News{AuthorTg: uid, AuthorName: author, Text: text})
	if err != nil {
		b.api.SendMessage(ctx, chat, "❌ Не удалось сохранить: "+htmlEsc(err.Error()), nil)
		return
	}
	b.api.SendMessage(ctx, chat, fmt.Sprintf("💡 <b>Добавлено в ленту фактов</b> #%d", id), nil)
	// Share to the bound group too, unless it was posted there already.
	body := newsItemText(&model.News{ID: id, AuthorName: author, Text: text})
	for _, c := range b.targets() {
		if c == chat {
			continue
		}
		b.api.SendMessage(ctx, c, body, nil)
	}
}

// AnnounceNews posts a feed entry created elsewhere (e.g. the Mini App) to the
// notification targets (the group). Best-effort.
func (b *Bot) AnnounceNews(newsID int64) {
	n, _ := b.st.GetNews(newsID)
	if n == nil {
		return
	}
	if n.AuthorName != "" {
		n.AuthorName = newsAuthor(n.AuthorName) // map @username → team name if known
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	body := newsItemText(n)
	chats := b.targets()
	log.Info().Int("recipients", len(chats)).Int64("news", newsID).Msg("announce news")
	for _, c := range chats {
		b.api.SendMessage(ctx, c, body, nil)
	}
}

// newsItemText formats a single feed entry for a chat message.
func newsItemText(n *model.News) string {
	who := n.AuthorName
	if who == "" {
		who = "кто-то"
	}
	return fmt.Sprintf("💡 <b>Интересный факт</b>\n%s\n\n— %s", htmlEsc(n.Text), htmlEsc(who))
}

// sendNewsFeed posts the latest feed entries.
func (b *Bot) sendNewsFeed(ctx context.Context, chat int64) {
	items, _ := b.st.ListNews(10)
	if len(items) == 0 {
		b.api.SendMessage(ctx, chat,
			"💡 <b>Лента фактов пуста.</b>\nДобавь первый: <code>/news твоя идея или находка</code>", nil)
		return
	}
	var sb strings.Builder
	sb.WriteString("💡 <b>Интересные факты</b> — последние:\n")
	for _, n := range items {
		who := htmlEsc(n.AuthorName)
		sb.WriteString(fmt.Sprintf("\n• %s\n  <i>— %s</i>\n", htmlEsc(n.Text), who))
	}
	sb.WriteString("\nДобавить: <code>/news текст</code>")
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}
