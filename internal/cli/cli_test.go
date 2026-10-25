package cli

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/songquanpeng/weread-cli/internal/domain"
	"github.com/songquanpeng/weread-cli/internal/storage"
	"github.com/songquanpeng/weread-cli/internal/update"
)

func cliFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	s, e := storage.Open(filepath.Join(home, "weread.db"))
	if e != nil {
		t.Fatal(e)
	}
	b := domain.Batch{Books: []domain.Book{{ID: "b1", Kind: "book", Title: "测试书", HasNotebook: true}}, Notes: map[string][]domain.Note{"b1": {
		{ID: "highlight:b1:h1", Kind: "highlight", BookID: "b1", Content: "人工智能与写作", CreatedUnix: 100, CreatedAt: "1970-01-01T08:01:40+08:00", Raw: json.RawMessage(`{"private":"metadata"}`)},
		{ID: "review:b1:r1", Kind: "review", BookID: "b1", Content: "读书的想法", Quote: "人工智能", CreatedUnix: 200, CreatedAt: "1970-01-01T08:03:20+08:00"},
	}}}
	if e = s.Commit(context.Background(), b); e != nil {
		t.Fatal(e)
	}
	s.Close()
	t.Setenv("WEREAD_API_KEY", "")
	t.Setenv("WEREAD_HOME", home)
	t.Setenv("WEREAD_DB", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	e := execute(context.Background(), &runtime{version: "v0.2.0", latest: testLatest(t), in: strings.NewReader(""), out: &out, errOut: &errOut}, args)
	return out.String(), e
}

func TestOfflineCommandsNeverRequireNetworkOrKey(t *testing.T) {
	cliFixture(t)
	for _, args := range [][]string{{"search", "人工智能", "--json"}, {"list", "--json"}, {"get", "highlight:b1:h1", "--json"}, {"books", "--json"}, {"book", "b1", "--json"}, {"stats", "--json"}, {"status", "--offline", "--json"}, {"export", "--output", "json"}} {
		args = append([]string{"--api-url", "http://127.0.0.1:1"}, args...)
		out, e := run(t, args...)
		if e != nil {
			t.Fatalf("%v: %v", args, e)
		}
		if !json.Valid([]byte(out)) {
			t.Fatalf("invalid JSON: %s", out)
		}
	}
}

func TestPaginationAndStableIDs(t *testing.T) {
	home := cliFixture(t)
	out, e := run(t, "list", "--home", home, "--limit", "1", "--json")
	if e != nil {
		t.Fatal(e)
	}
	var p struct {
		Total, Count int
		HasMore      bool `json:"has_more"`
		Next         *nextPage
		Items        []domain.Note
	}
	if e = json.Unmarshal([]byte(out), &p); e != nil {
		t.Fatal(e)
	}
	if p.Total != 2 || p.Count != 1 || !p.HasMore || p.Next.Offset != 1 || !strings.Contains(p.Next.Command, quoteArg(home)) || p.Items[0].ID != "review:b1:r1" {
		t.Fatalf("pagination: %s", out)
	}
	if strings.Contains(out, "private") {
		t.Fatal("raw metadata leaked by default")
	}
	out, e = run(t, "list", "--offset", "1", "--limit", "1", "--json")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out, `"id": "highlight:b1:h1"`) || !strings.Contains(out, `"has_more": false`) {
		t.Fatalf("page 2: %s", out)
	}
	for _, args := range [][]string{{"list", "--limit", "0"}, {"list", "--offset", "-1"}, {"list", "--kind", "bookmark"}, {"search", "x", "--from", "bad"}, {"list", "--from", "2026-10-25", "--to", "2026-10-24"}} {
		if _, e = run(t, args...); e == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
}

func TestStreamingExportsAndInclusiveDates(t *testing.T) {
	cliFixture(t)
	out, e := run(t, "export")
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatal("JSONL row count")
	}
	for _, l := range lines {
		if !json.Valid([]byte(l)) {
			t.Fatal("invalid JSONL")
		}
	}
	out, e = run(t, "export", "--output", "csv")
	if e != nil {
		t.Fatal(e)
	}
	rows, e := csv.NewReader(strings.NewReader(out)).ReadAll()
	if e != nil || len(rows) != 3 {
		t.Fatalf("CSV: %d %v", len(rows), e)
	}
	out, e = run(t, "export", "--output", "markdown")
	if e != nil || !strings.Contains(out, "人工智能") {
		t.Fatalf("Markdown: %v", e)
	}
	out, e = run(t, "list", "--from", "1970-01-01", "--to", "1970-01-01", "--json")
	if e != nil || !strings.Contains(out, `"total": 2`) {
		t.Fatalf("inclusive dates: %s %v", out, e)
	}
	if _, e = run(t, "export", "--entity", "reading", "--book", "b1"); e == nil {
		t.Fatal("silently ignored filters")
	}
}

func TestLoginDoesNotExposeKeyAndLogoutKeepsMirror(t *testing.T) {
	home := cliFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-secret" {
			t.Error("wrong key")
		}
		w.Write([]byte(`{"books":[]}`))
	}))
	defer srv.Close()
	var out, errOut bytes.Buffer
	e := execute(context.Background(), &runtime{version: "v0.2.0", latest: testLatest(t), in: strings.NewReader("synthetic-secret\n"), out: &out, errOut: &errOut}, []string{"--api-url", srv.URL, "login", "--key-stdin", "--json"})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.String()+errOut.String(), "synthetic-secret") {
		t.Fatal("login exposed key")
	}
	if _, e = os.Stat(filepath.Join(home, "auth.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = run(t, "logout", "--json"); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(e) {
		t.Fatal("saved key remains")
	}
	if _, e = run(t, "get", "highlight:b1:h1", "--json"); e != nil {
		t.Fatal("logout deleted mirror")
	}
}

func testLatest(t *testing.T) update.Checker {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("release request received credentials")
		}
		w.Write([]byte(`{"tag_name":"v0.2.0"}`))
	}))
	t.Cleanup(s.Close)
	return update.Checker{URL: s.URL, HTTP: s.Client()}
}

func TestRawAPIFlatParams(t *testing.T) {
	cliFixture(t)
	t.Setenv("WEREAD_API_KEY", "synthetic-secret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		json.NewDecoder(r.Body).Decode(&v)
		if v["scope"] != float64(10) || v["keyword"] != "测试" {
			t.Error("flat params missing")
		}
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	if _, e := run(t, "--api-url", srv.URL, "api", "/store/search", "--params", `{"keyword":"测试","scope":10}`, "--json"); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{`{}`, `[]`, `{} {}`, `{"api_name":"other"}`, `{"params":{}}`} {
		if p == `{}` {
			continue
		}
		if _, e := run(t, "api", "/store/search", "--params", p); e == nil {
			t.Fatalf("invalid params accepted: %s", p)
		}
	}
}
