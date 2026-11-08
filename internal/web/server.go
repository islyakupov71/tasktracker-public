// Package web serves the Mini App static assets and the JSON API. All API
// requests are authenticated via Telegram WebApp initData (HMAC over the bot
// token); in dev mode a fixed fake user is used so the UI can be exercised
// without Telegram.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"tasktracker/internal/model"
	"tasktracker/internal/store"
)

//go:embed static
var staticFS embed.FS

// Server is the HTTP server for the Mini App.
type Server struct {
	st         *store.Store
	botToken   string
	uploadsDir string   // where mini-app-uploaded screenshots are stored
	dev        bool     // when true, skip initData auth (local testing)
	debugAuth  bool     // when true, log initData + validation outcome (diagnostics)
	notify     Notifier // bot-side notifications (may be nil)
	devRole    model.Role
	devTgID    int64
}

// Notifier is the subset of the bot the web layer needs to push notifications.
// Implemented by *bot.Bot; nil when the bot is disabled.
type Notifier interface {
	AnnounceNewTask(taskID int64)
	NotifyAssigned(taskID int64, name string, toImpl bool)
	NotifyHelp(taskID int64, stage string)
	NotifyHandoff(taskID int64, implName string)
	NotifyFail(taskID int64, stage string)
	AnnounceNews(newsID int64)
	BroadcastTask(text string, taskID int64)
}

// New builds the server. If dev is true, requests without valid initData are
// accepted as a fake user (tg id 1) so the UI works locally. notify, if non-nil,
// delivers Telegram notifications; pass nil when the bot is off.
func New(st *store.Store, botToken, uploadsDir string, dev bool, notify Notifier) *Server {
	os.MkdirAll(uploadsDir, 0o755)
	return &Server{
		st: st, botToken: botToken, uploadsDir: uploadsDir, dev: dev,
		debugAuth: os.Getenv("DEBUG_AUTH") == "1",
		notify:    notify,
		devRole:   model.RoleAnalyst, devTgID: 1,
	}
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static Mini App assets, served from the embedded "static" subtree.
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// API.
	mux.HandleFunc("/api/me", s.auth(s.handleMe))
	mux.HandleFunc("/api/tasks", s.auth(s.handleTasks))     // GET list, POST create
	mux.HandleFunc("/api/tasks/", s.auth(s.handleTaskByID)) // GET/PATCH/sub-actions
	mux.HandleFunc("/api/news", s.auth(s.handleNews))       // GET feed, POST create
	mux.HandleFunc("/api/news/", s.auth(s.handleNewsByID))  // DELETE
	mux.HandleFunc("/api/file", s.auth(s.handleFile))       // screenshot proxy
	mux.HandleFunc("/api/clientdebug", s.handleClientDebug) // unauth diagnostics beacon

	return logging(mux)
}

// authedUser carries the resolved Telegram user plus their stored role.
type authedUser struct {
	TgID     int64
	Username string
	Role     model.Role // empty if not registered
}

// auth wraps a handler, resolving the authenticated user from initData.
func (s *Server) auth(next func(http.ResponseWriter, *http.Request, *authedUser)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := r.Header.Get("X-Telegram-Init-Data")
		if initData == "" {
			initData = r.URL.Query().Get("initData")
		}
		var au authedUser
		u, verr := validateInitData(initData, s.botToken)
		if s.debugAuth {
			log.Warn().Str("path", r.URL.Path).Int("initdata_len", len(initData)).
				Err(verr).Str("initdata", initData).Msg("AUTH DEBUG")
		}
		if verr == nil {
			au.TgID = u.ID
			au.Username = u.Username
		} else if s.dev {
			au.TgID = s.devTgID
			au.Username = "dev"
		} else {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// Resolve role from store (best-effort).
		if usr, err := s.st.GetUser(au.TgID); err == nil && usr != nil {
			au.Role = usr.Role
		} else if s.dev && au.Role == "" {
			au.Role = s.devRole
		}
		next(w, r, &au)
	}
}

// handleClientDebug logs a diagnostic payload from the Mini App client. It is
// intentionally unauthenticated (used to diagnose why initData is missing).
func (s *Server) handleClientDebug(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	log.Warn().Str("client", string(body)).Msg("CLIENT DEBUG")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, au *authedUser) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tg_id":    au.TgID,
		"username": au.Username,
		"role":     au.Role,
	})
}

// fetchFile downloads a Telegram file by file_id and streams it to w.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request, _ *authedUser) {
	fileID := r.URL.Query().Get("id")
	if fileID == "" {
		writeErr(w, http.StatusBadRequest, "missing id")
		return
	}
	// Locally-uploaded screenshots (from the Mini App) are stored on disk and
	// referenced as "local:<name>".
	if name, ok := strings.CutPrefix(fileID, "local:"); ok {
		name = filepath.Base(name) // prevent path traversal
		w.Header().Set("Cache-Control", "private, max-age=3600")
		http.ServeFile(w, r, filepath.Join(s.uploadsDir, name))
		return
	}
	path, err := s.telegramFilePath(r.Context(), fileID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "getFile failed")
		return
	}
	url := "https://api.telegram.org/file/bot" + s.botToken + "/" + path
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "download failed")
		return
	}
	defer resp.Body.Close()
	// Telegram serves images as application/octet-stream, which some webviews
	// refuse to render in <img>. Derive a proper image content-type from the
	// file path extension.
	ct := resp.Header.Get("Content-Type")
	if ct == "" || ct == "application/octet-stream" {
		if byExt := mime.TypeByExtension(filepath.Ext(path)); byExt != "" {
			ct = byExt
		} else {
			ct = "image/jpeg"
		}
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	io.Copy(w, resp.Body)
}

func (s *Server) telegramFilePath(ctx context.Context, fileID string) (string, error) {
	url := "https://api.telegram.org/bot" + s.botToken + "/getFile?file_id=" + fileID
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Result.FilePath, nil
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Debug().Str("method", r.Method).Str("path", r.URL.Path).
			Dur("took", time.Since(start)).Msg("http")
	})
}
