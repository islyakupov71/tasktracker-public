// Command tasktracker runs the Telegram bug-tracker bot and its Mini App web
// server in a single process. It uses a compact deployment pattern:
// one static Go binary, config via flags + environment, run under systemd.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"tasktracker/internal/bot"
	"tasktracker/internal/store"
	"tasktracker/internal/tunnel"
	"tasktracker/internal/web"
)

func main() {
	var (
		httpAddr       = flag.String("http", envOr("HTTP_ADDR", "127.0.0.1:8099"), "Mini App HTTP listen address")
		dbPath         = flag.String("db", envOr("DB_PATH", "tasks.db"), "SQLite database path")
		logLevel       = flag.String("log", envOr("LOG_LEVEL", "info"), "log level: debug|info|warn|error")
		tunnelOn       = flag.Bool("tunnel", envOr("TUNNEL", "on") != "off", "manage a cloudflared quick tunnel")
		cfBin          = flag.String("cloudflared", envOr("CLOUDFLARED_BIN", "cloudflared"), "cloudflared binary path")
		miniAppURL     = flag.String("miniapp-url", os.Getenv("MINIAPP_URL"), "fixed Mini App URL (overrides tunnel)")
		miniAppURLFile = flag.String("miniapp-url-file", os.Getenv("MINIAPP_URL_FILE"), "file whose contents are the current Mini App URL (re-read each use; for an external tunnel manager)")
		dev            = flag.Bool("dev", false, "dev mode: skip initData auth, no bot/tunnel without token")
	)
	flag.Parse()

	zerolog.TimeFieldFormat = time.RFC3339
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	if lvl, err := zerolog.ParseLevel(*logLevel); err == nil {
		zerolog.SetGlobalLevel(lvl)
	}

	token := os.Getenv("BOT_TOKEN")

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal().Err(err).Msg("open store")
	}
	defer st.Close()
	log.Info().Str("db", *dbPath).Msg("store opened")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Screenshots (mini-app uploads + bot downloads) live next to the DB.
	uploadsDir := filepath.Join(filepath.Dir(*dbPath), "uploads")
	os.MkdirAll(uploadsDir, 0o755)

	// Current Mini App URL provider: url-file > fixed override > tunnel > empty.
	// The url-file is re-read on every use, so an external tunnel manager (e.g. a
	// pinggy reconnect loop) can rotate the URL without restarting the bot.
	var tun *tunnel.Tunnel
	urlFn := func() string { return strings.TrimSpace(*miniAppURL) }
	if *miniAppURLFile != "" {
		urlFn = func() string {
			b, _ := os.ReadFile(*miniAppURLFile)
			return strings.TrimSpace(string(b))
		}
	} else if *miniAppURL == "" && *tunnelOn {
		tun = tunnel.New(*cfBin, "http://"+localTarget(*httpAddr))
		urlFn = tun.URL
	}

	// Bot (only if a token is configured).
	var b *bot.Bot
	if token != "" {
		api := bot.NewClient(token)
		// getMe can be slow on the first cold TLS handshake to api.telegram.org
		// from throttled networks (DPI on non-US IPs) — retry a few times with a
		// generous per-attempt timeout before declaring the token bad.
		var me *bot.User
		var err error
		for attempt := 1; attempt <= 5; attempt++ {
			mctx, mcancel := context.WithTimeout(ctx, 30*time.Second)
			me, err = api.GetMe(mctx)
			mcancel()
			if err == nil || ctx.Err() != nil {
				break
			}
			log.Warn().Err(err).Int("attempt", attempt).Msg("getMe failed, retrying")
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
		if err != nil {
			log.Fatal().Err(err).Msg("getMe: invalid BOT_TOKEN or telegram unreachable")
		}
		log.Info().Str("bot", "@"+me.Username).Msg("telegram bot authenticated")
		b = bot.New(api, st, uploadsDir, urlFn)
		if tun != nil {
			tun.OnURL(b.OnMenuURL)
		} else if u := urlFn(); u != "" {
			b.OnMenuURL(u)
		}
		go b.Run(ctx)
	} else {
		log.Warn().Msg("BOT_TOKEN not set: bot disabled (web runs in dev auth mode)")
		*dev = true
	}

	if tun != nil {
		go tun.Run(ctx)
	}

	// Web server. When the bot is live, let the web layer push lifecycle
	// notifications (new task / assigned / горит очко / hand-off / problem), and
	// run the reminder scheduler.
	var notifier web.Notifier
	if b != nil {
		notifier = b
		go b.RemindLoop(ctx)
	}
	srv := web.New(st, token, uploadsDir, *dev, notifier)
	httpSrv := &http.Server{Addr: *httpAddr, Handler: srv.Handler()}
	go func() {
		log.Info().Str("addr", *httpAddr).Bool("dev", *dev).Msg("mini app server listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("http server")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")
	shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(shctx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// localTarget turns a listen address into a dialable host:port for cloudflared
// (0.0.0.0 -> 127.0.0.1).
func localTarget(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return addr
}
