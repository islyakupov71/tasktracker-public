package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tasktracker/internal/model"
)

// GET /api/tasks?window=board|analyst|dev   -> list
// POST /api/tasks                            -> create
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request, au *authedUser) {
	switch r.Method {
	case http.MethodGet:
		win := model.Window(r.URL.Query().Get("window"))
		if !win.Valid() {
			win = model.WindowNew
		}
		tasks, err := s.st.ListTasks(win)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
	case http.MethodPost:
		var in struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Category    string `json:"category"`
			BackendKind string `json:"backend_kind"`
			Importance  string `json:"importance"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "bad json")
			return
		}
		cat := model.Category(in.Category)
		if strings.TrimSpace(in.Title) == "" || !cat.Valid() {
			writeErr(w, http.StatusBadRequest, "title and valid category required")
			return
		}
		imp := model.Importance(in.Importance)
		if !imp.Valid() {
			imp = model.ImportanceNormal
		}
		t := &model.Task{
			Title:       strings.TrimSpace(in.Title),
			Description: in.Description,
			Category:    cat,
			Importance:  imp,
			CreatedBy:   au.TgID,
		}
		if cat == model.CategoryBackend {
			bk := model.BackendKind(in.BackendKind)
			if !bk.Valid() {
				writeErr(w, http.StatusBadRequest, "backend_kind required for backend category")
				return
			}
			t.BackendKind = bk
		}
		id, err := s.st.CreateTask(t)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.notify != nil {
			s.notify.AnnounceNewTask(id)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// /api/tasks/{id}            GET detail, PATCH edit
// /api/tasks/{id}/comment    POST
// /api/tasks/{id}/notes      POST
// /api/tasks/{id}/done       POST
func (s *Server) handleTaskByID(w http.ResponseWriter, r *http.Request, au *authedUser) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.SplitN(rest, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	switch action {
	case "":
		switch r.Method {
		case http.MethodGet:
			t, err := s.st.GetTask(id)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if t == nil {
				writeErr(w, http.StatusNotFound, "not found")
				return
			}
			writeJSON(w, http.StatusOK, t)
		case http.MethodPatch:
			var fields map[string]any
			if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
				writeErr(w, http.StatusBadRequest, "bad json")
				return
			}
			if err := s.st.UpdateTask(id, fields); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		case http.MethodDelete:
			if err := s.st.DeleteTask(id); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "comment":
		var in struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
			writeErr(w, http.StatusBadRequest, "text required")
			return
		}
		if _, err := s.st.AddComment(&model.Comment{
			TaskID: id, AuthorTg: au.TgID, AuthorRole: au.Role, Text: strings.TrimSpace(in.Text),
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "notes":
		var in struct {
			Notes string `json:"notes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "bad json")
			return
		}
		if err := s.st.SetAnalystNotes(id, in.Notes); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "attach":
		s.handleAttach(w, r, au, id)
	case "assign", "assign-impl":
		// Distribute a new task: pick the executor (Участник 01|Участник 02). "assign" sends it
		// to Аналитика; "assign-impl" skips analysis straight to Реализация.
		var in struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if !model.ValidName(in.Name) {
			writeErr(w, http.StatusBadRequest, "name must be Участник 01 or Участник 02")
			return
		}
		toImpl := action == "assign-impl"
		var aerr error
		if toImpl {
			aerr = s.st.AssignImpl(id, in.Name)
		} else {
			aerr = s.st.AssignAnalyst(id, in.Name)
		}
		if aerr != nil {
			writeErr(w, http.StatusInternalServerError, aerr.Error())
			return
		}
		if s.notify != nil {
			s.notify.NotifyAssigned(id, in.Name, toImpl)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "return-to-pool":
		// Idle task -> back to Новые задачи.
		if err := s.st.ReturnToPool(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "start":
		// Flip an idle task to "в работе" on its stage.
		stage := model.Stage(reqStage(r))
		if !stage.Valid() {
			writeErr(w, http.StatusBadRequest, "bad stage")
			return
		}
		if err := s.st.StartWork(id, stage); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "help":
		// Toggle "горит очко". On -> broadcast an alert naming the assignee.
		var in struct {
			Stage string `json:"stage"`
			On    bool   `json:"on"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		stage := model.Stage(in.Stage)
		if !stage.Valid() {
			writeErr(w, http.StatusBadRequest, "bad stage")
			return
		}
		if err := s.st.SetHelp(id, stage, in.On); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if in.On && s.notify != nil {
			s.notify.NotifyHelp(id, string(stage))
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "analyst-success":
		// "Мужик" at analysis: save notes, pick the implementer -> Реализация.
		var in struct {
			Impl  string `json:"impl"`
			Notes string `json:"notes"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if !model.ValidName(in.Impl) {
			writeErr(w, http.StatusBadRequest, "impl must be Участник 01 or Участник 02")
			return
		}
		if err := s.st.AnalystSuccess(id, in.Impl, in.Notes); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.notify != nil {
			s.notify.NotifyHandoff(id, in.Impl)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "analyst-fail":
		// "Лох" at analysis: record the problem -> Проблемы.
		var in struct {
			Problem string `json:"problem"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if err := s.st.AnalystFail(id, strings.TrimSpace(in.Problem)); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.notify != nil {
			s.notify.NotifyFail(id, string(model.StageAnalyst))
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "impl-success":
		// "Мужик" at implementation: verdict on analyst + real solution -> Сделано.
		var in struct {
			AnalystLied bool   `json:"analyst_lied"`
			Solution    string `json:"solution"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if err := s.st.ImplSuccess(id, in.AnalystLied, strings.TrimSpace(in.Solution)); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "impl-fail":
		// "Лох" at implementation: record the problem -> Проблемы.
		var in struct {
			Problem string `json:"problem"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if err := s.st.ImplFail(id, strings.TrimSpace(in.Problem)); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.notify != nil {
			s.notify.NotifyFail(id, string(model.StageImpl))
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "reopen":
		// Проблемы -> Новые задачи.
		if err := s.st.ReturnToPool(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeErr(w, http.StatusNotFound, "unknown action")
	}
}

// GET /api/news            -> list feed (newest first)
// POST /api/news           -> create a feed entry
func (s *Server) handleNews(w http.ResponseWriter, r *http.Request, au *authedUser) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.st.ListNews(100)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"news": items})
	case http.MethodPost:
		var in struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
			writeErr(w, http.StatusBadRequest, "text required")
			return
		}
		name := au.Username
		if name == "" {
			name = "аноним"
		}
		id, err := s.st.AddNews(&model.News{
			AuthorTg: au.TgID, AuthorName: name, Text: strings.TrimSpace(in.Text),
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.notify != nil {
			s.notify.AnnounceNews(id)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// DELETE /api/news/{id}
func (s *Server) handleNewsByID(w http.ResponseWriter, r *http.Request, _ *authedUser) {
	if r.Method != http.MethodDelete {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/news/"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.st.DeleteNews(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// reqStage reads the "stage" field from a JSON body without consuming typed
// handlers (used where stage is the only field).
func reqStage(r *http.Request) string {
	var in struct {
		Stage string `json:"stage"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	return in.Stage
}

// handleAttach accepts a multipart "file" upload from the Mini App, stores it on
// disk, and records an attachment referencing it as "local:<name>".
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request, au *authedUser, id int64) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.ParseMultipartForm(16 << 20); err != nil { // 16 MiB cap
		writeErr(w, http.StatusBadRequest, "bad multipart")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(hdr.Filename))
	if ext == "" || len(ext) > 5 {
		ext = ".jpg"
	}
	var rnd [12]byte
	rand.Read(rnd[:])
	name := hex.EncodeToString(rnd[:]) + ext

	dst, err := os.Create(filepath.Join(s.uploadsDir, name))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store failed")
		return
	}
	if _, err := io.Copy(dst, io.LimitReader(file, 16<<20)); err != nil {
		dst.Close()
		writeErr(w, http.StatusInternalServerError, "write failed")
		return
	}
	dst.Close()

	origin := model.AttachmentOrigin(r.FormValue("origin"))
	if origin != model.OriginAnalyst && origin != model.OriginReport {
		if au.Role == model.RoleAnalyst {
			origin = model.OriginAnalyst
		} else {
			origin = model.OriginReport
		}
	}
	if _, err := s.st.AddAttachment(&model.Attachment{
		TaskID: id, TgFileID: "local:" + name, Origin: origin,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
