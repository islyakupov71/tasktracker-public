package bot

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"tasktracker/internal/model"
)

// htmlEsc escapes user text for HTML parse_mode messages.
func htmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// commandArgs returns everything after the first token of a command.
func commandArgs(text string) string {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// taskLine is a one-line task summary for list outputs.
func (b *Bot) taskLine(t *model.Task) string {
	who := ""
	switch {
	case t.Status == model.StatusUnassigned:
		who = "🙋 свободна"
	case strings.HasPrefix(string(t.Status), "analyst") && t.AnalystName != "":
		who = "🔍 " + t.AnalystName
	case strings.HasPrefix(string(t.Status), "impl") && t.DevName != "":
		who = "🛠 " + t.DevName
	}
	return fmt.Sprintf("• #%d «%s» [%s] %s — %s", t.ID, htmlEsc(t.Title), t.Importance.RU(), t.Status.RU(), who)
}

// ---- /battle ----
func (b *Bot) sendBattle(ctx context.Context, chat int64) {
	since := time.Now().Add(-7 * 24 * time.Hour)
	var sb strings.Builder
	sb.WriteString("<b>⚔️ БИТВА за 7 дней</b>\n\n")
	scores := map[string]int{}
	for _, n := range teamNames() {
		ps, _ := b.st.StatsFor(n, since)
		score := ps.Realized*3 + ps.Analyzed*2 + ps.Took - ps.Failed*2 - ps.Panicked
		scores[n] = score
		sb.WriteString(fmt.Sprintf("<b>%s</b> — счёт %d\n  🛠%d 🔍%d 🔧взял%d ❌%d 🔥%d\n",
			n, score, ps.Realized, ps.Analyzed, ps.Took, ps.Failed, ps.Panicked))
	}
	a, b2 := teamNames()[0], teamNames()[1]
	sb.WriteString("\n")
	switch {
	case scores[a] == scores[b2]:
		sb.WriteString("🤝 Ничья. Оба одинаково (бес)полезны.")
	default:
		win, lose := a, b2
		if scores[b2] > scores[a] {
			win, lose = b2, a
		}
		sb.WriteString(fmt.Sprintf("🏆 Победитель недели: <b>%s</b>\n💩 Лох недели: %s\n%s",
			win, mention(lose), pickJoke(jokesFail, lose)))
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /vozduh ----
func (b *Bot) sendVozduh(ctx context.Context, chat int64) {
	rows, _ := b.st.VozduhCounts()
	if len(rows) == 0 {
		b.api.SendMessage(ctx, chat, "🌬 Воздуханов пока нет — все аналитики кристально честны. Подозрительно.", nil)
		return
	}
	var sb strings.Builder
	sb.WriteString("<b>🌬 Рейтинг воздуханов</b>\n(сколько раз анализ оказался гоном)\n\n")
	for i, r := range rows {
		sb.WriteString(fmt.Sprintf("%d. <b>%s</b> — %d раз\n", i+1, r.Label, r.Count))
	}
	sb.WriteString("\n" + mention(rows[0].Label) + ", ты чемпион по воздуху 🤥")
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /week ----
func (b *Bot) sendWeek(ctx context.Context, chat int64) {
	now := time.Now()
	wk := now.Add(-7 * 24 * time.Hour)
	prevFrom := now.Add(-14 * 24 * time.Hour)
	created, _ := b.st.CountCreatedSince(wk)
	done, _ := b.st.CountDoneBetween(wk, now)
	donePrev, _ := b.st.CountDoneBetween(prevFrom, wk)
	probs, _ := b.st.CountEventsSince(wk, "analyst_fail", "impl_fail")
	trend := "→ как и было"
	if done > donePrev {
		trend = "📈 лучше"
	} else if done < donePrev {
		trend = "📉 хуже"
	}
	var sb strings.Builder
	sb.WriteString("<b>📅 Итоги недели</b>\n")
	sb.WriteString(fmt.Sprintf("Создано: <b>%d</b>\nЗакрыто: <b>%d</b> (%s, неделей раньше %d)\nПровалов: <b>%d</b>\n\n",
		created, done, trend, donePrev, probs))
	sb.WriteString("<b>По людям:</b>\n")
	for _, n := range teamNames() {
		ps, _ := b.st.StatsFor(n, wk)
		sb.WriteString(fmt.Sprintf("%s: 🛠%d 🔍%d ❌%d 🔥%d\n", n, ps.Realized, ps.Analyzed, ps.Failed, ps.Panicked))
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /backend ----
func (b *Bot) sendBackend(ctx context.Context, chat int64) {
	cat, _ := b.st.CategoryCounts()
	bk, _ := b.st.BackendBugCounts()
	var sb strings.Builder
	sb.WriteString("<b>🔧 Где болит</b>\n\nПо категориям:\n")
	for _, r := range cat {
		sb.WriteString(fmt.Sprintf("• %s: <b>%d</b>\n", model.Category(r.Label).RU(), r.Count))
	}
	if len(bk) > 0 {
		sb.WriteString("\nБэкенд по типам:\n")
		for _, r := range bk {
			sb.WriteString(fmt.Sprintf("• %s: <b>%d</b>\n", model.BackendKind(r.Label).RU(), r.Count))
		}
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /my ----
func (b *Bot) sendMy(ctx context.Context, chat int64, username string) {
	name := nameFromUsername(username)
	if name == "" {
		b.api.SendMessage(ctx, chat, "Не узнаю тебя 🤔 /my работает только для участника 01 и участника 02 (нужен их @username).", nil)
		return
	}
	ts, _ := b.st.MyActiveTasks(name)
	if len(ts) == 0 {
		b.api.SendMessage(ctx, chat, name+", на тебе сейчас пусто. Самое время взять задачу (или ставку 🎰).", nil)
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<b>На %s сейчас (%d):</b>\n", name, len(ts)))
	for _, t := range ts {
		sb.WriteString(b.taskLine(t) + "\n")
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /burning ----
func (b *Bot) sendBurning(ctx context.Context, chat int64) {
	ts, _ := b.st.TasksByStatuses("analyst_help", "impl_help")
	if len(ts) == 0 {
		b.api.SendMessage(ctx, chat, "🧯 Ни у кого не горит. Тишина да гладь.", nil)
		return
	}
	var sb strings.Builder
	sb.WriteString("<b>🔥 Горит очко — нужна помощь:</b>\n")
	for _, t := range ts {
		who := t.AnalystName
		if strings.HasPrefix(string(t.Status), "impl") {
			who = t.DevName
		}
		sb.WriteString(fmt.Sprintf("• #%d «%s» — %s\n", t.ID, htmlEsc(t.Title), mention(who)))
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /problems ----
func (b *Bot) sendProblemsCmd(ctx context.Context, chat int64) {
	ts, _ := b.st.ListTasks(model.WindowProblems)
	if len(ts) == 0 {
		b.api.SendMessage(ctx, chat, "✅ Проблемных задач нет.", nil)
		return
	}
	var sb strings.Builder
	sb.WriteString("<b>❌ Проблемы:</b>\n")
	for _, t := range ts {
		stage, who := "Аналитика", t.AnalystName
		if t.ProblemStage == model.StageImpl {
			stage, who = "Реализация", t.DevName
		}
		sb.WriteString(fmt.Sprintf("• #%d «%s» — затык на %s (%s)\n   %s\n",
			t.ID, htmlEsc(t.Title), stage, who, htmlEsc(t.ProblemDesc)))
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /task <id> ----
func (b *Bot) sendTaskCard(ctx context.Context, chat int64, args string) {
	id, err := strconv.ParseInt(strings.TrimSpace(args), 10, 64)
	if err != nil {
		b.api.SendMessage(ctx, chat, "Использование: /task &lt;номер&gt;", nil)
		return
	}
	t, _ := b.st.GetTask(id)
	if t == nil {
		b.api.SendMessage(ctx, chat, fmt.Sprintf("Задача #%d не найдена.", id), nil)
		return
	}
	cat := t.Category.RU()
	if t.BackendKind != "" {
		cat += " / " + t.BackendKind.RU()
	}
	who := ""
	if t.AnalystName != "" {
		who += "🔍 " + t.AnalystName + " "
	}
	if t.DevName != "" {
		who += "🛠 " + t.DevName
	}
	text := fmt.Sprintf("<b>#%d — %s</b>\n[%s] %s %s\nКатегория: %s\n\nОписание:\n%s",
		t.ID, htmlEsc(t.Title), t.Importance.RU(), t.Status.RU(), who, cat, htmlEsc(t.Description))
	if t.AnalystNotes != "" {
		text += "\n\n📋 Заметки аналитика:\n" + htmlEsc(t.AnalystNotes)
	}
	if t.ProblemDesc != "" {
		text += "\n\n⚠️ Проблема:\n" + htmlEsc(t.ProblemDesc)
	}
	if t.SolutionDesc != "" {
		text += "\n\n✅ Решение:\n" + htmlEsc(t.SolutionDesc)
	}
	b.api.SendMessage(ctx, chat, text, b.taskMarkup(chat, t.ID))
}

// ---- /find <text> ----
func (b *Bot) sendFind(ctx context.Context, chat int64, args string) {
	q := strings.TrimSpace(args)
	if q == "" {
		b.api.SendMessage(ctx, chat, "Использование: /find &lt;текст&gt;", nil)
		return
	}
	ts, _ := b.st.SearchTasks(q, 10)
	if len(ts) == 0 {
		b.api.SendMessage(ctx, chat, "Ничего не нашёл по «"+htmlEsc(q)+"».", nil)
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<b>🔎 Найдено по «%s»:</b>\n", htmlEsc(q)))
	for _, t := range ts {
		sb.WriteString(b.taskLine(t) + "\n")
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /slow ----
func (b *Bot) sendSlow(ctx context.Context, chat int64) {
	ts, _ := b.st.TasksByStatuses("analyst_wip", "analyst_help", "impl_wip", "impl_help")
	var withStart []*model.Task
	for _, t := range ts {
		if t.WorkStarted != nil {
			withStart = append(withStart, t)
		}
	}
	if len(withStart) == 0 {
		b.api.SendMessage(ctx, chat, "🐢 В работе ничего не залежалось (или ничего не в работе).", nil)
		return
	}
	sort.Slice(withStart, func(i, j int) bool { return withStart[i].WorkStarted.Before(*withStart[j].WorkStarted) })
	now := time.Now()
	var sb strings.Builder
	sb.WriteString("<b>🐢 Дольше всего висят в работе:</b>\n")
	for i, t := range withStart {
		if i >= 5 {
			break
		}
		who := t.AnalystName
		if strings.HasPrefix(string(t.Status), "impl") {
			who = t.DevName
		}
		sb.WriteString(fmt.Sprintf("• #%d «%s» — %s (%s)\n", t.ID, htmlEsc(t.Title), humanDur(now.Sub(*t.WorkStarted)), who))
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /idle ----
func (b *Bot) sendIdle(ctx context.Context, chat int64) {
	now := time.Now()
	var sb strings.Builder
	sb.WriteString("<b>💤 Активность:</b>\n")
	for _, n := range teamNames() {
		last, ok, _ := b.st.LastEventTime(n)
		if !ok {
			sb.WriteString(fmt.Sprintf("• %s — НИ РАЗУ ничего не делал.\n", mention(n)))
			continue
		}
		sb.WriteString(fmt.Sprintf("• %s — последнее действие %s назад\n", n, humanDur(now.Sub(last))))
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// ---- /note <id> <text> ----
func (b *Bot) addNote(ctx context.Context, chat, fromTg int64, args string) {
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) < 2 {
		b.api.SendMessage(ctx, chat, "Использование: /note &lt;номер&gt; &lt;текст&gt;", nil)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		b.api.SendMessage(ctx, chat, "Первым аргументом — номер задачи. Пример: /note 5 проверь логи", nil)
		return
	}
	if t, _ := b.st.GetTask(id); t == nil {
		b.api.SendMessage(ctx, chat, fmt.Sprintf("Задача #%d не найдена.", id), nil)
		return
	}
	if _, err := b.st.AddComment(&model.Comment{TaskID: id, AuthorTg: fromTg, Text: strings.TrimSpace(parts[1])}); err != nil {
		b.api.SendMessage(ctx, chat, "Ошибка: "+err.Error(), nil)
		return
	}
	b.api.SendMessage(ctx, chat, fmt.Sprintf("💬 Комментарий к #%d добавлен.", id), nil)
}

// ---- daily digest (scheduled) ----

// sendDigest posts the 14:00 board summary + 24h standup + what's burning.
func (b *Bot) sendDigest(ctx context.Context, chat int64) {
	var sb strings.Builder
	sb.WriteString("<b>📅 Дайджест дня</b>\n\n<b>Доска:</b>\n")
	pages := []struct {
		w     model.Window
		label string
	}{
		{model.WindowNew, "🆕 Новые"},
		{model.WindowAnalyst, "🔍 Аналитика"},
		{model.WindowImpl, "🛠 Реализация"},
		{model.WindowProblems, "❌ Проблемы"},
		{model.WindowDone, "✅ Сделано"},
	}
	for _, p := range pages {
		ts, _ := b.st.ListTasks(p.w)
		sb.WriteString(fmt.Sprintf("%s: <b>%d</b>\n", p.label, len(ts)))
	}
	sb.WriteString("\n<b>Стендап за 24ч:</b>\n")
	since := time.Now().Add(-24 * time.Hour)
	for _, n := range teamNames() {
		ps, _ := b.st.StatsFor(n, since)
		sb.WriteString(fmt.Sprintf("%s: взял %d · сделал %d (🔍%d 🛠%d) · провал %d · 🔥%d\n",
			n, ps.Took, ps.Analyzed+ps.Realized, ps.Analyzed, ps.Realized, ps.Failed, ps.Panicked))
	}
	burn, _ := b.st.TasksByStatuses("analyst_help", "impl_help")
	if len(burn) > 0 {
		sb.WriteString("\n🔥 Горит прямо сейчас:\n")
		for _, t := range burn {
			who := t.AnalystName
			if strings.HasPrefix(string(t.Status), "impl") {
				who = t.DevName
			}
			sb.WriteString(fmt.Sprintf("• #%d «%s» — %s\n", t.ID, htmlEsc(t.Title), mention(who)))
		}
	}
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// maybeDigest posts the daily digest once, when local time reaches DIGEST_HOUR
// (default 14:00). Called from the reminder loop on every tick.
func (b *Bot) maybeDigest(now time.Time) {
	if now.Hour() != envInt("DIGEST_HOUR", 14) {
		return
	}
	today := now.Format("2006-01-02")
	if last, _ := b.st.GetSetting("last_digest_date"); last == today {
		return
	}
	b.st.SetSetting("last_digest_date", today)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, chat := range b.targets() {
		b.sendDigest(ctx, chat)
	}
}
