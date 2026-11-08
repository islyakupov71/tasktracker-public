// Package bot implements the Telegram bot side of the task tracker: role
// registration, bug submission (conversational FSM), accepting the next task
// from your queue, and marking tasks done. It shares the store with the web
// Mini App, so both stay in sync.
package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"tasktracker/internal/model"
	"tasktracker/internal/store"
)

// Button labels (reply keyboard). The task lifecycle lives in the Mini App; the
// bot only creates bugs, opens the app and pings about tasks. There are no roles
// — only pages.
const (
	btnAddBug  = "➕ Добавить баг"
	btnOpenApp = "🌐 Открыть задачник"
)

// step is a position in a multi-message conversation.
type step int

const (
	stepNone step = iota
	stepTitle
	stepDesc
	stepCategory   // awaiting inline callback
	stepBackend    // awaiting inline callback
	stepImportance // awaiting inline callback
	stepScreens    // awaiting photos or /skip (new bug)
)

// convState is per-user conversation state.
type convState struct {
	step  step
	draft model.Task
	files []string // collected screenshot file_ids
}

// Bot is the Telegram bot.
type Bot struct {
	api        *Client
	st         *store.Store
	uploadsDir string               // local screenshot storage (shared with web)
	states     map[int64]*convState // keyed by tg user id
	// urlFn returns the current public Mini App URL (may be empty).
	urlFn func() string
	// groupChat is the bound group's chat id (0 if none); when set, all
	// notifications go to the group instead of individual DMs.
	groupChat atomic.Int64
	// botUsername is resolved from getMe (for t.me/<bot>?startapp deep links).
	botUsername string
}

// New builds a Bot.
func New(api *Client, st *store.Store, uploadsDir string, urlFn func() string) *Bot {
	b := &Bot{api: api, st: st, uploadsDir: uploadsDir, states: map[int64]*convState{}, urlFn: urlFn}
	if v, _ := st.GetSetting("group_chat_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			b.groupChat.Store(id)
		}
	}
	return b
}

// targets returns the chat ids that should receive notifications: the bound
// group if set, otherwise every registered user (DMs).
func (b *Bot) targets() []int64 {
	if g := b.groupChat.Load(); g != 0 {
		return []int64{g}
	}
	users, _ := b.st.ListUsers()
	out := make([]int64, 0, len(users))
	for _, u := range users {
		out = append(out, u.TgID)
	}
	return out
}

func (b *Bot) state(uid int64) *convState {
	s := b.states[uid]
	if s == nil {
		s = &convState{}
		b.states[uid] = s
	}
	return s
}

// OnMenuURL is wired to the tunnel: re-publishes the default menu button.
func (b *Bot) OnMenuURL(url string) {
	if url == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.api.SetDefaultMenuButton(ctx, "Задачник", url); err != nil {
		log.Warn().Err(err).Msg("setChatMenuButton failed")
	} else {
		log.Info().Str("url", url).Msg("menu button updated")
	}
}

// Run drives the long-poll loop until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	if me, err := b.api.GetMe(ctx); err == nil {
		b.botUsername = me.Username
	}
	b.publishCommands(ctx)
	var offset int64
	for ctx.Err() == nil {
		updates, err := b.api.GetUpdates(ctx, offset, 50)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn().Err(err).Msg("getUpdates failed")
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			b.dispatch(ctx, u)
		}
	}
}

func (b *Bot) dispatch(ctx context.Context, u Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("bot dispatch panic")
		}
	}()
	switch {
	case u.CallbackQuery != nil:
		b.handleCallback(ctx, u.CallbackQuery)
	case u.Message != nil:
		b.handleMessage(ctx, u.Message)
	}
}

func (b *Bot) handleMessage(ctx context.Context, m *Message) {
	if m.From == nil {
		return
	}
	uid := m.From.ID
	chat := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	isGroup := chat < 0 // Telegram group/supergroup chat ids are negative

	// Every interaction registers the sender's chat so notifications reach them.
	b.register(uid, m.From.Username)

	// Bind the group: the first group the bot is talked to in becomes the
	// notification target (all alerts then go there instead of DMs).
	if isGroup && b.groupChat.Load() != chat {
		b.groupChat.Store(chat)
		b.st.SetSetting("group_chat_id", strconv.FormatInt(chat, 10))
		log.Info().Int64("group", chat).Msg("bound notification group")
	}

	// A filled-in bug template (text or photo caption) creates a task in one
	// shot — works in the group and in private, no FSM. Checked before the
	// command switch and the group silent-return below.
	body := text
	if body == "" {
		body = strings.TrimSpace(m.Caption)
	}
	if isBugForm(body) {
		b.submitBugForm(ctx, m, body)
		return
	}

	// Slash commands work everywhere (private and group). In a group they may
	// arrive as "/stats@botname" — botCommand strips the mention.
	switch botCommand(text) {
	case "/start":
		if isGroup {
			b.api.SendMessage(ctx, chat, "🤖 <b>Бот на месте.</b> Уведомления идут сюда.\n\n"+
				"🐞 <b>/баг</b> — завести баг по шаблону\n"+
				"➡️ <b>/new</b> — следующая задача\n"+
				"📊 <b>/stats</b> — сводка по доске\n"+
				"🌐 <b>/open</b> — открыть задачник", nil)
		} else {
			b.sendMainMenu(ctx, chat, "Привет! Это задачник демонстрационной команды.")
		}
		return
	case "/bag", "/баг", "/bug":
		b.sendBugTemplate(ctx, chat)
		return
	case "/open":
		b.sendOpenApp(ctx, chat)
		return
	case "/stats":
		b.sendStats(ctx, chat)
		return
	case "/new":
		b.sendNext(ctx, chat)
		return
	case "/member1":
		b.sendPersonReport(ctx, chat, model.NameMemberOne)
		return
	case "/member2":
		b.sendPersonReport(ctx, chat, model.NameMemberTwo)
		return
	case "/battle":
		b.sendBattle(ctx, chat)
		return
	case "/vozduh":
		b.sendVozduh(ctx, chat)
		return
	case "/week":
		b.sendWeek(ctx, chat)
		return
	case "/backend":
		b.sendBackend(ctx, chat)
		return
	case "/my":
		b.sendMy(ctx, chat, m.From.Username)
		return
	case "/burning":
		b.sendBurning(ctx, chat)
		return
	case "/problems":
		b.sendProblemsCmd(ctx, chat)
		return
	case "/slow":
		b.sendSlow(ctx, chat)
		return
	case "/idle":
		b.sendIdle(ctx, chat)
		return
	case "/task":
		b.sendTaskCard(ctx, chat, commandArgs(text))
		return
	case "/find":
		b.sendFind(ctx, chat, commandArgs(text))
		return
	case "/note":
		b.addNote(ctx, chat, uid, commandArgs(text))
		return
	case "/news":
		b.handleNews(ctx, chat, uid, m.From.Username, commandArgs(text))
		return
	}

	// Everything below is private-chat only: the add-bug FSM, photos and menu
	// buttons. In a group the bot stays silent on non-command chatter.
	if isGroup {
		return
	}

	if len(m.Photo) > 0 {
		b.handlePhoto(ctx, m)
		return
	}
	st := b.state(uid)
	if st.step != stepNone {
		b.handleConversation(ctx, m, nil, st)
		return
	}
	switch text {
	case btnAddBug:
		st.step = stepTitle
		st.draft = model.Task{CreatedBy: uid}
		st.files = nil
		b.api.SendMessage(ctx, chat, "Опишите кратко суть бага (заголовок):", nil)
	case btnOpenApp:
		b.sendOpenApp(ctx, chat)
	default:
		b.sendMainMenu(ctx, chat, "Выберите действие:")
	}
}

// botCommand extracts a normalized "/cmd" from text, stripping a trailing
// "@botname" (Telegram appends it for commands in groups). Returns "" if text
// is not a command.
func botCommand(text string) string {
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	c := strings.Fields(text)[0]
	if i := strings.Index(c, "@"); i >= 0 {
		c = c[:i]
	}
	return c
}

// sendStats posts a per-page task count summary (used by /stats, incl. groups).
func (b *Bot) sendStats(ctx context.Context, chat int64) {
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
	var sb strings.Builder
	sb.WriteString("<b>📊 Статистика задач</b>\n")
	total := 0
	for _, p := range pages {
		ts, _ := b.st.ListTasks(p.w)
		sb.WriteString(fmt.Sprintf("%s: <b>%d</b>\n", p.label, len(ts)))
		total += len(ts)
	}
	sb.WriteString(fmt.Sprintf("———\nВсего активных+архив: <b>%d</b>", total))
	b.api.SendMessage(ctx, chat, sb.String(), nil)
}

// sendNext posts the highest-priority unassigned task (urgent → normal → low).
func (b *Bot) sendNext(ctx context.Context, chat int64) {
	t, err := b.st.NextNew()
	if err != nil {
		b.api.SendMessage(ctx, chat, "Ошибка: "+err.Error(), nil)
		return
	}
	if t == nil {
		b.api.SendMessage(ctx, chat, "🎉 Новых задач нет — разбирать нечего.", nil)
		return
	}
	cat := t.Category.RU()
	if t.BackendKind != "" {
		cat += " / " + t.BackendKind.RU()
	}
	desc := t.Description
	if desc == "" {
		desc = "—"
	}
	text := fmt.Sprintf("➡️ <b>Следующая задача</b> #%d [%s]\n<b>%s</b>\nКатегория: %s\n\n%s",
		t.ID, t.Importance.RU(), t.Title, cat, desc)
	b.api.SendMessage(ctx, chat, text, b.taskMarkup(chat, t.ID))
}

// sendPersonReport posts a person's 24h activity + a roast/praise verdict.
func (b *Bot) sendPersonReport(ctx context.Context, chat int64, name string) {
	since := time.Now().Add(-24 * time.Hour)
	ps, _ := b.st.StatsFor(name, since)
	var joke string
	switch {
	case ps.Analyzed+ps.Realized > 0: // что-то довёл до конца → хвалим
		joke = pickJoke(jokesWin, name)
	case ps.Took > 0 || ps.Failed > 0 || ps.Panicked > 0: // брал, но не вывез
		joke = pickJoke(jokesFail, name)
	default: // вообще ничего
		joke = pickJoke(jokesLazy, name)
	}
	text := fmt.Sprintf(
		"<b>%s — за 24 часа</b>\n"+
			"🔧 Взял в работу: <b>%d</b>\n"+
			"🔍 Проанализировал: <b>%d</b>\n"+
			"🛠 Реализовал: <b>%d</b>\n"+
			"❌ Не выполнил: <b>%d</b>\n"+
			"🔥 Сгорело очко: <b>%d</b>\n\n%s",
		name, ps.Took, ps.Analyzed, ps.Realized, ps.Failed, ps.Panicked, joke)
	b.api.SendMessage(ctx, chat, text, nil)
}

// publishCommands sets the bot's "/" command menu with descriptions.
func (b *Bot) publishCommands(ctx context.Context) {
	cmds := []BotCommand{
		{Command: "bag", Description: "🐞 Завести баг по шаблону (/баг)"},
		{Command: "news", Description: "💡 Лента фактов: /news <текст> или просто /news"},
		{Command: "new", Description: "Следующая задача по важности"},
		{Command: "my", Description: "Мои задачи в работе"},
		{Command: "burning", Description: "У кого горит очко прямо сейчас"},
		{Command: "problems", Description: "Проблемные задачи (затыки)"},
		{Command: "slow", Description: "Дольше всего висящие в работе"},
		{Command: "task", Description: "Карточка задачи: /task <номер>"},
		{Command: "find", Description: "Поиск задачи: /find <текст>"},
		{Command: "note", Description: "Комментарий: /note <номер> <текст>"},
		{Command: "stats", Description: "Сводка по доске задач"},
		{Command: "week", Description: "Итоги недели с трендом"},
		{Command: "backend", Description: "Где больше всего багов"},
		{Command: "battle", Description: "Участник 01 vs Участник 02 за 7 дней"},
		{Command: "vozduh", Description: "Рейтинг воздуханов (вранья аналитика)"},
		{Command: "idle", Description: "Кто давно ничего не делал"},
		{Command: "member1", Description: "Статистика участника 01 за 24ч + вердикт"},
		{Command: "member2", Description: "Статистика участника 02 за 24ч + вердикт"},
		{Command: "open", Description: "Открыть задачник (в личке)"},
	}
	// Publish to the default scope and explicitly to group chats so the "/" menu
	// shows in both private chats and the team group.
	if err := b.api.SetCommands(ctx, cmds, nil); err != nil {
		log.Warn().Err(err).Msg("setMyCommands(default) failed")
	}
	if err := b.api.SetCommands(ctx, cmds, map[string]any{"type": "all_group_chats"}); err != nil {
		log.Warn().Err(err).Msg("setMyCommands(groups) failed")
	}
}

// register ensures the user is in the store so broadcasts reach them.
func (b *Bot) register(uid int64, username string) {
	if u, _ := b.st.GetUser(uid); u == nil {
		b.st.UpsertUser(uid, username, "")
	}
}

func (b *Bot) sendMainMenu(ctx context.Context, chat int64, header string) {
	rows := [][]KeyboardButton{{{Text: btnAddBug}}}
	if url := b.currentURL(); url != "" {
		rows = append(rows, []KeyboardButton{{Text: btnOpenApp, WebApp: &WebAppInfo{URL: url}}})
	}
	kb := ReplyKeyboardMarkup{Keyboard: rows, ResizeKeyboard: true, IsPersistent: true}
	b.api.SendMessage(ctx, chat, header+"\nДобавляйте баги и открывайте задачник 👇", kb)
}

// taskButton returns an inline web_app deep-link button for a task (or nil).
func (b *Bot) taskButton(taskID int64) any {
	url := b.currentURL()
	if url == "" || taskID <= 0 {
		return nil
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	full := url + sep + "task=" + strconv.FormatInt(taskID, 10)
	return InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "🔗 Открыть задачу", WebApp: &WebAppInfo{URL: full}},
	}}}
}

// taskMarkup returns the deep-link button for a chat — but only for private
// chats (chat id > 0), since Telegram forbids web_app buttons in groups.
func (b *Bot) taskMarkup(chat, taskID int64) any {
	if chat < 0 {
		return nil
	}
	return b.taskButton(taskID)
}

// BroadcastTask sends a message (+ deep-link in DMs) to the notification targets
// (group if bound, else every registered user). Best-effort.
func (b *Bot) BroadcastTask(text string, taskID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	chats := b.targets()
	log.Info().Int("recipients", len(chats)).Int64("task", taskID).Msg("broadcast")
	for _, chat := range chats {
		if err := b.api.SendMessage(ctx, chat, text, b.taskMarkup(chat, taskID)); err != nil {
			log.Debug().Err(err).Int64("tg", chat).Msg("broadcast: send")
		}
	}
}

// AnnounceNewTask posts a "new task — who'll take it?" message to the targets
// and records each message so it can later reply (tagging who took it) when
// picked up. Red exclamation marks (instead of a "NEW" badge) make a fresh task
// impossible to miss; both teammates are tagged as a call to action.
func (b *Bot) AnnounceNewTask(taskID int64) {
	t, _ := b.st.GetTask(taskID)
	if t == nil {
		return
	}
	cat := t.Category.RU()
	if t.BackendKind != "" {
		cat += " / " + t.BackendKind.RU()
	}
	text := fmt.Sprintf(
		"‼️❗ <b>НОВАЯ ЗАДАЧА</b> #%d\n%s <b>%s</b>\n«%s»\n🏷 %s\n\n👉 Кто берёт? %s",
		t.ID, t.Importance.Emoji(), t.Importance.RU(), htmlEsc(t.Title), cat, mentionAll())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	chats := b.targets()
	log.Info().Int("recipients", len(chats)).Int64("task", taskID).Msg("announce new task")
	for _, chat := range chats {
		mid, err := b.api.SendMessageID(ctx, chat, text, b.taskMarkup(chat, taskID))
		if err != nil {
			log.Debug().Err(err).Int64("tg", chat).Msg("announce: send")
			continue
		}
		b.st.AddAnnouncement(taskID, chat, mid)
	}
}

// NotifyAssigned replies to the task's announcement messages, tagging (pinging)
// who took it for analysis or implementation. Falls back to a broadcast if no
// announcement was recorded.
func (b *Bot) NotifyAssigned(taskID int64, name string, toImpl bool) {
	emoji, stage := "🔍", "В аналитику"
	if toImpl {
		emoji, stage = "🛠", "В реализацию"
	}
	text := fmt.Sprintf("%s <b>%s:</b> %s", emoji, stage, mention(name))
	anns, _ := b.st.ListAnnouncements(taskID)
	if len(anns) == 0 {
		b.BroadcastTask(text, taskID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, a := range anns {
		if err := b.api.SendReply(ctx, a.ChatID, a.MessageID, text, nil); err != nil {
			log.Debug().Err(err).Int64("tg", a.ChatID).Msg("assign reply")
		}
	}
}

// NotifyHelp announces that the assignee on the given stage needs help ("горит
// очко"), tagging them so they get pinged.
func (b *Bot) NotifyHelp(taskID int64, stage string) {
	t, _ := b.st.GetTask(taskID)
	if t == nil {
		return
	}
	who := t.AnalystName
	if model.Stage(stage) == model.StageImpl {
		who = t.DevName
	}
	b.BroadcastTask(fmt.Sprintf("🔥 <b>Горит очко!</b> У %s по задаче «%s» (#%d) — нужна помощь!",
		mention(who), htmlEsc(t.Title), t.ID), taskID)
}

// NotifyHandoff announces an analyst→implementation hand-off, tagging the
// chosen implementer.
func (b *Bot) NotifyHandoff(taskID int64, implName string) {
	t, _ := b.st.GetTask(taskID)
	if t == nil {
		return
	}
	b.BroadcastTask(fmt.Sprintf("📦 <b>В реализацию:</b> задачу «%s» (#%d) делает %s",
		htmlEsc(t.Title), t.ID, mention(implName)), taskID)
}

// NotifyFail announces that a task dead-ended on the given stage and moved to
// Проблемы, tagging the person who got stuck.
func (b *Bot) NotifyFail(taskID int64, stage string) {
	t, _ := b.st.GetTask(taskID)
	if t == nil {
		return
	}
	who, stageRU := t.AnalystName, "аналитике"
	if model.Stage(stage) == model.StageImpl {
		who, stageRU = t.DevName, "реализации"
	}
	b.BroadcastTask(fmt.Sprintf("❌ <b>Затык на %s:</b> «%s» (#%d), %s. → Проблемы",
		stageRU, htmlEsc(t.Title), t.ID, mention(who)), taskID)
}

func (b *Bot) sendOpenApp(ctx context.Context, chat int64) {
	url := b.currentURL()
	if url == "" {
		b.api.SendMessage(ctx, chat,
			"Мини-приложение пока недоступно (туннель ещё поднимается). Попробуйте позже.", nil)
		return
	}
	// web_app buttons are private-chat only; in a group, open the Main App via a
	// t.me deep link (URL buttons ARE allowed in groups).
	if chat < 0 {
		if b.botUsername == "" {
			b.api.SendMessage(ctx, chat,
				"Откройте задачник в личке с ботом (кнопка меню).", nil)
			return
		}
		kb := InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
			{Text: "🌐 Открыть задачник", URL: "https://t.me/" + b.botUsername + "?startapp"},
		}}}
		b.api.SendMessage(ctx, chat, "Откройте задачник:", kb)
		return
	}
	kb := InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "🌐 Открыть задачник", WebApp: &WebAppInfo{URL: url}},
	}}}
	b.api.SendMessage(ctx, chat, "Откройте задачник:", kb)
}

func (b *Bot) currentURL() string {
	if b.urlFn == nil {
		return ""
	}
	return b.urlFn()
}
