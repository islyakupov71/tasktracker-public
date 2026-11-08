package bot

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"tasktracker/internal/model"
)

// remindCfg holds the reminder cadence. Defaults: nag urgent tasks every 1h,
// normal every 2h, low never (except the all-clear nudge) — applied both to
// tasks nobody has taken yet and to tasks sitting in work. Overridable via env
// for testing (REMIND_TICK_SEC / REMIND_URGENT_MIN / REMIND_NORMAL_MIN).
type remindCfg struct {
	tick   time.Duration
	urgent time.Duration
	normal time.Duration
}

func loadRemindCfg() remindCfg {
	return remindCfg{
		tick:   time.Duration(envInt("REMIND_TICK_SEC", 600)) * time.Second,
		urgent: time.Duration(envInt("REMIND_URGENT_MIN", 60)) * time.Minute,
		normal: time.Duration(envInt("REMIND_NORMAL_MIN", 120)) * time.Minute,
	}
}

// importanceInterval returns the nag cadence for an importance (0 = never nag).
func (c remindCfg) importanceInterval(imp model.Importance) time.Duration {
	switch imp {
	case model.ImportanceUrgent:
		return c.urgent
	case model.ImportanceNormal:
		return c.normal
	default:
		return 0
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// RemindLoop periodically nags assignees about tasks they're sitting on.
func (b *Bot) RemindLoop(ctx context.Context) {
	cfg := loadRemindCfg()
	log.Info().Dur("tick", cfg.tick).Dur("urgent", cfg.urgent).Dur("normal", cfg.normal).Msg("reminder loop started")
	t := time.NewTicker(cfg.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.remindTick(ctx, cfg)
		}
	}
}

func (b *Bot) remindTick(ctx context.Context, cfg remindCfg) {
	now := time.Now()

	b.maybeDigest(now) // 14:00 daily digest

	// 1) Unassigned tasks nobody has picked up yet: nag the group to take them
	// (urgent hourly, normal every 2h). This is the "task hung for hours and
	// nobody took it" alert.
	unassigned, err := b.st.ListTasks(model.WindowNew)
	if err != nil {
		log.Warn().Err(err).Msg("remind: list unassigned")
	}
	for _, t := range unassigned {
		interval := cfg.importanceInterval(t.Importance)
		if interval == 0 {
			continue // low priority: no periodic nag
		}
		ref := t.CreatedAt
		if t.LastReminder != nil && t.LastReminder.After(ref) {
			ref = *t.LastReminder
		}
		if now.Sub(ref) >= interval {
			b.sendUnassignedNag(t, now)
			b.st.SetLastReminder(t.ID)
		}
	}

	// 2) Tasks sitting in work: nag the assignee on the same cadence.
	inProg, err := b.st.ListInProgress()
	if err != nil {
		log.Warn().Err(err).Msg("remind: list in-progress")
		return
	}
	for _, t := range inProg {
		interval := cfg.importanceInterval(t.Importance)
		if interval == 0 {
			continue // low priority: no periodic nag
		}
		if t.WorkStarted == nil {
			continue
		}
		ref := *t.WorkStarted
		if t.LastReminder != nil && t.LastReminder.After(ref) {
			ref = *t.LastReminder
		}
		if now.Sub(ref) >= interval {
			b.sendReminder(t)
			b.st.SetLastReminder(t.ID)
		}
	}

	// Low-priority all-clear nudge: only when nothing urgent/normal is in work.
	urg, _ := b.st.CountActiveByImportance(model.ImportanceUrgent)
	nor, _ := b.st.CountActiveByImportance(model.ImportanceNormal)
	if urg == 0 && nor == 0 {
		waiting, _ := b.st.ListWaiting(model.ImportanceLow)
		if len(waiting) > 0 {
			t := waiting[0]
			if t.LastReminder == nil || now.Sub(*t.LastReminder) >= cfg.normal {
				b.BroadcastTask(fmt.Sprintf(
					"😌 Срочных и обычных задач нет. Гляньте несрочную: 🟢 #%d «%s»", t.ID, htmlEsc(t.Title)), t.ID)
				b.st.SetLastReminder(t.ID)
			}
		}
	}
	_ = ctx
}

// sendUnassignedNag pings the whole team that a task is still unclaimed.
func (b *Bot) sendUnassignedNag(t *model.Task, now time.Time) {
	waited := humanDur(now.Sub(t.CreatedAt))
	b.BroadcastTask(fmt.Sprintf(
		"‼️ <b>Задачу никто не взял</b> — висит уже %s!\n%s #%d «%s»\n👉 Разберите: %s",
		waited, t.Importance.Label(), t.ID, htmlEsc(t.Title), mentionAll()), t.ID)
}

func (b *Bot) sendReminder(t *model.Task) {
	name, stageRU := t.AnalystName, "Аналитика"
	if strings.HasPrefix(string(t.Status), "impl") {
		name, stageRU = t.DevName, "Реализация"
	}
	if name == "" {
		name = "кто-то"
	}
	el := ""
	if t.WorkStarted != nil {
		el = " — висит " + humanDur(time.Since(*t.WorkStarted))
	}
	b.BroadcastTask(fmt.Sprintf("⏰ %s, задача #%d «%s» (<b>%s</b>)%s. Глянь!",
		mention(name), t.ID, htmlEsc(t.Title), stageRU, el), t.ID)
}

func humanDur(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dч %dм", h, m)
	}
	return fmt.Sprintf("%dм", m)
}
