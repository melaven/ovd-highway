package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ovd/internal/domain"
)

type testEnv struct {
	handler *Handler
	repo    *fakeRepo
	manager *SourceManager
	factory *fakeFactory
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	repo := newFakeRepo()
	factory := &fakeFactory{}
	out := make(chan domain.RawSignal, 8)
	ctx, cancel := context.WithCancel(context.Background())
	mgr := NewSourceManager(ctx, repo, out, newFakeTG(), factory.build, discardLog())
	settings := NewSettingsStore(repo, EngineSettings{
		SystemPrompt: "default prompt",
		MinScore:     70,
	}, discardLog())
	h := NewHandler(repo, repo, settings, mgr, discardLog())
	t.Cleanup(func() {
		mgr.Shutdown()
		cancel()
	})
	return &testEnv{handler: h, repo: repo, manager: mgr, factory: factory}
}

func do(t *testing.T, env *testEnv, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	env.handler.Routes().ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
}

func TestListSourcesEmptyReturnsArrayNotNull(t *testing.T) {
	env := newTestEnv(t)
	rec := do(t, env, http.MethodGet, "/api/v1/sources", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("body = %q, want []", rec.Body.String())
	}
}

func TestCreateSource(t *testing.T) {
	env := newTestEnv(t)
	rec := do(t, env, http.MethodPost, "/api/v1/sources",
		`{"type":"hh","target":"golang developer"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var src Source
	decodeJSON(t, rec, &src)
	if src.ID == "" || src.Type != SourceTypeHH || src.Target != "golang developer" || !src.Enabled {
		t.Fatalf("unexpected source: %+v", src)
	}
	conn := env.factory.latestByName("hh:golang developer")
	if conn == nil {
		t.Fatal("connector was not started for created source")
	}
	waitChan(t, conn.started, "created source connector")
}

func TestCreateSourceValidation(t *testing.T) {
	env := newTestEnv(t)
	for _, body := range []string{
		`{"type":"bogus","target":"x"}`,
		`{"type":"hh","target":""}`,
		`{"type":"hh"}`,
		`not-json`,
	} {
		rec := do(t, env, http.MethodPost, "/api/v1/sources", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestPatchSourceToggleAndNotFound(t *testing.T) {
	env := newTestEnv(t)
	repo := env.repo
	repo.seed(testSource("s1", SourceTypeHH, "q", true))

	rec := do(t, env, http.MethodPatch, "/api/v1/sources/s1", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var src Source
	decodeJSON(t, rec, &src)
	if src.Enabled {
		t.Error("patched source still enabled")
	}
	if s, _ := repo.get("s1"); s.Enabled {
		t.Error("repo source still enabled after patch")
	}

	rec = do(t, env, http.MethodPatch, "/api/v1/sources/missing", `{"enabled":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("patch missing status = %d, want 404", rec.Code)
	}

	rec = do(t, env, http.MethodPatch, "/api/v1/sources/s1", `{"enabled":null}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch null enabled status = %d, want 400", rec.Code)
	}
}

func TestDeleteSource(t *testing.T) {
	env := newTestEnv(t)
	env.repo.seed(testSource("s1", SourceTypeHH, "q", true))

	rec := do(t, env, http.MethodDelete, "/api/v1/sources/s1", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = do(t, env, http.MethodDelete, "/api/v1/sources/s1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", rec.Code)
	}
}

func TestSettingsGetAndPut(t *testing.T) {
	env := newTestEnv(t)

	rec := do(t, env, http.MethodGet, "/api/v1/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings status = %d", rec.Code)
	}
	var got EngineSettings
	decodeJSON(t, rec, &got)
	if got.SystemPrompt != "default prompt" || got.MinScore != 70 {
		t.Fatalf("unexpected defaults: %+v", got)
	}

	rec = do(t, env, http.MethodPut, "/api/v1/settings",
		`{"system_prompt":"custom prompt","min_score":85}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put settings status = %d; body=%s", rec.Code, rec.Body.String())
	}
	decodeJSON(t, rec, &got)
	if got.SystemPrompt != "custom prompt" || got.MinScore != 85 {
		t.Fatalf("unexpected stored settings: %+v", got)
	}

	rec = do(t, env, http.MethodGet, "/api/v1/settings", "")
	decodeJSON(t, rec, &got)
	if got.SystemPrompt != "custom prompt" || got.MinScore != 85 {
		t.Fatalf("settings not persisted: %+v", got)
	}
}

func TestSettingsValidation(t *testing.T) {
	env := newTestEnv(t)
	for _, body := range []string{
		`{"system_prompt":"","min_score":50}`,
		`{"system_prompt":"  ","min_score":50}`,
		`{"system_prompt":"ok","min_score":101}`,
		`{"system_prompt":"ok","min_score":-1}`,
	} {
		rec := do(t, env, http.MethodPut, "/api/v1/settings", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestUnknownRoute404(t *testing.T) {
	env := newTestEnv(t)
	rec := do(t, env, http.MethodGet, "/api/v1/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPatchLeadStatus(t *testing.T) {
	env := newTestEnv(t)
	id := "11111111-1111-1111-1111-111111111111"
	env.repo.seedLead(id, "new", "old pitch")

	rec := do(t, env, http.MethodPatch, "/api/v1/leads/"+id, `{"status":"contacted"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var res LeadPatchResult
	decodeJSON(t, rec, &res)
	if res.ID != id || res.Status != "contacted" || res.DraftProposal != "old pitch" {
		t.Fatalf("unexpected patch result: %+v", res)
	}
}

func TestPatchLeadStatusAndDraft(t *testing.T) {
	env := newTestEnv(t)
	id := "22222222-2222-2222-2222-222222222222"
	env.repo.seedLead(id, "new", "old pitch")

	rec := do(t, env, http.MethodPatch, "/api/v1/leads/"+id,
		`{"status":"archived","draft_proposal":"revised pitch"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var res LeadPatchResult
	decodeJSON(t, rec, &res)
	if res.Status != "archived" || res.DraftProposal != "revised pitch" {
		t.Fatalf("unexpected patch result: %+v", res)
	}
}

func TestPatchLeadValidation(t *testing.T) {
	env := newTestEnv(t)
	id := "33333333-3333-3333-3333-333333333333"
	env.repo.seedLead(id, "new", "pitch")

	for _, body := range []string{
		`{"status":"bogus"}`,
		`{}`,
		`{"draft_proposal":"   "}`,
		`{"status":null,"draft_proposal":null}`,
	} {
		rec := do(t, env, http.MethodPatch, "/api/v1/leads/"+id, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestPatchLeadInvalidUUID(t *testing.T) {
	env := newTestEnv(t)
	rec := do(t, env, http.MethodPatch, "/api/v1/leads/not-a-uuid", `{"status":"contacted"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchLeadNotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := do(t, env, http.MethodPatch,
		"/api/v1/leads/44444444-4444-4444-4444-444444444444", `{"status":"contacted"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
