package store

import (
	"path/filepath"
	"testing"

	"tasktracker/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func count(t *testing.T, st *Store, w model.Window) int {
	t.Helper()
	ts, err := st.ListTasks(w)
	if err != nil {
		t.Fatalf("ListTasks(%s): %v", w, err)
	}
	return len(ts)
}

func mustStatus(t *testing.T, st *Store, id int64) model.Status {
	t.Helper()
	full, err := st.GetTask(id)
	if err != nil || full == nil {
		t.Fatalf("GetTask(%d): %v", id, err)
	}
	return full.Status
}

// TestHappyPath walks a bug from creation all the way to Сделано.
func TestHappyPath(t *testing.T) {
	st := newTestStore(t)
	id, err := st.CreateTask(&model.Task{
		Title: "bug", Category: model.CategoryBackend, BackendKind: model.BackendParsing,
		Importance: model.ImportanceUrgent, CreatedBy: 100,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Fresh: unassigned, only on Новые.
	if mustStatus(t, st, id) != model.StatusUnassigned {
		t.Fatalf("status after create = %s", mustStatus(t, st, id))
	}
	if count(t, st, model.WindowNew) != 1 || count(t, st, model.WindowAnalyst) != 0 {
		t.Fatalf("new=%d analyst=%d", count(t, st, model.WindowNew), count(t, st, model.WindowAnalyst))
	}
	if full, _ := st.GetTask(id); full.Importance != model.ImportanceUrgent {
		t.Fatalf("importance = %s", full.Importance)
	}

	// Distribute to Участник 01 -> Аналитика, not-started.
	if err := st.AssignAnalyst(id, model.NameMemberOne); err != nil {
		t.Fatal(err)
	}
	if mustStatus(t, st, id) != model.StatusAnalystIdle {
		t.Fatalf("after assign = %s", mustStatus(t, st, id))
	}
	if count(t, st, model.WindowNew) != 0 || count(t, st, model.WindowAnalyst) != 1 {
		t.Fatalf("after assign windows wrong")
	}

	// Analyst starts work, panics, recovers.
	st.StartWork(id, model.StageAnalyst)
	if mustStatus(t, st, id) != model.StatusAnalystWIP {
		t.Fatalf("after start = %s", mustStatus(t, st, id))
	}
	st.SetHelp(id, model.StageAnalyst, true)
	if mustStatus(t, st, id) != model.StatusAnalystHelp {
		t.Fatalf("after help = %s", mustStatus(t, st, id))
	}
	if count(t, st, model.WindowAnalyst) != 1 {
		t.Fatalf("help task left analyst page")
	}
	st.SetHelp(id, model.StageAnalyst, false)

	// Analyst succeeds, hands off to Участник 02 -> Реализация.
	if err := st.AnalystSuccess(id, model.NameMemberTwo, "fix like so"); err != nil {
		t.Fatal(err)
	}
	full, _ := st.GetTask(id)
	if full.Status != model.StatusImplIdle || full.DevName != model.NameMemberTwo ||
		full.AnalystName != model.NameMemberOne || full.AnalystNotes != "fix like so" {
		t.Fatalf("after analyst success: %+v", full)
	}
	if count(t, st, model.WindowImpl) != 1 || count(t, st, model.WindowAnalyst) != 0 {
		t.Fatalf("impl windows wrong")
	}

	// Implementer works and finishes with a verdict.
	st.StartWork(id, model.StageImpl)
	if err := st.ImplSuccess(id, true, "the real fix"); err != nil {
		t.Fatal(err)
	}
	full, _ = st.GetTask(id)
	if full.Status != model.StatusDone || !full.AnalystLied || full.SolutionDesc != "the real fix" || full.ClosedAt == nil {
		t.Fatalf("after impl success: %+v", full)
	}
	if count(t, st, model.WindowDone) != 1 || count(t, st, model.WindowImpl) != 0 {
		t.Fatalf("done windows wrong")
	}
}

// TestAnalystFailToProblems checks "Лох" at analysis routes to Проблемы and
// reopen sends it back to Новые.
func TestAnalystFailToProblems(t *testing.T) {
	st := newTestStore(t)
	id, _ := st.CreateTask(&model.Task{Title: "x", Category: model.CategoryServer, CreatedBy: 1})
	st.AssignAnalyst(id, model.NameMemberOne)
	st.StartWork(id, model.StageAnalyst)
	if err := st.AnalystFail(id, "не понял как чинить"); err != nil {
		t.Fatal(err)
	}
	full, _ := st.GetTask(id)
	if full.Status != model.StatusProblem || full.ProblemStage != model.StageAnalyst || full.ProblemDesc == "" {
		t.Fatalf("after analyst fail: %+v", full)
	}
	if count(t, st, model.WindowProblems) != 1 {
		t.Fatalf("not on problems page")
	}
	// Reopen.
	if err := st.ReturnToPool(id); err != nil {
		t.Fatal(err)
	}
	full, _ = st.GetTask(id)
	if full.Status != model.StatusUnassigned || full.AnalystName != "" || full.ProblemStage != "" {
		t.Fatalf("after reopen: %+v", full)
	}
	if count(t, st, model.WindowNew) != 1 {
		t.Fatalf("reopen not back on new")
	}
}

// TestImplFailToProblems checks "Лох" at implementation routes to Проблемы with
// the impl stage tag.
func TestImplFailToProblems(t *testing.T) {
	st := newTestStore(t)
	id, _ := st.CreateTask(&model.Task{Title: "x", Category: model.CategoryServer, CreatedBy: 1})
	st.AssignAnalyst(id, model.NameMemberOne)
	st.AnalystSuccess(id, model.NameMemberTwo, "notes")
	st.StartWork(id, model.StageImpl)
	st.SetHelp(id, model.StageImpl, true)
	if mustStatus(t, st, id) != model.StatusImplHelp {
		t.Fatalf("impl help = %s", mustStatus(t, st, id))
	}
	if err := st.ImplFail(id, "не работает"); err != nil {
		t.Fatal(err)
	}
	full, _ := st.GetTask(id)
	if full.Status != model.StatusProblem || full.ProblemStage != model.StageImpl {
		t.Fatalf("after impl fail: %+v", full)
	}
}

// TestCommentsAndAttachments verifies hydration on detail reads.
func TestCommentsAndAttachments(t *testing.T) {
	st := newTestStore(t)
	id, _ := st.CreateTask(&model.Task{Title: "x", Category: model.CategoryServer, CreatedBy: 1})
	if _, err := st.AddComment(&model.Comment{TaskID: id, AuthorTg: 1, AuthorRole: model.RoleAnalyst, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttachment(&model.Attachment{TaskID: id, TgFileID: "f1", Origin: model.OriginReport}); err != nil {
		t.Fatal(err)
	}
	full, _ := st.GetTask(id)
	if len(full.Comments) != 1 || full.Comments[0].Text != "hi" {
		t.Fatalf("comments = %+v", full.Comments)
	}
	if len(full.Attachments) != 1 || full.Attachments[0].TgFileID != "f1" {
		t.Fatalf("attachments = %+v", full.Attachments)
	}
}
