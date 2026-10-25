package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/songquanpeng/weread-cli/internal/config"
	"github.com/songquanpeng/weread-cli/internal/update"
)

func loginRun(t *testing.T, input string, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	e := execute(context.Background(), &runtime{version: "v0.2.0", latest: testLatest(t), in: strings.NewReader(input), out: &out, errOut: &errOut}, args)
	return out.String(), errOut.String(), e
}

func TestGuidedLoginAndSavedKeyPriority(t *testing.T) {
	home := cliFixture(t)
	cfg, _ := config.Load(home, "")
	requests := []string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		requests = append(requests, key)
		if key == "expired-key" {
			w.WriteHeader(401)
			return
		}
		if key != "fresh-key" {
			t.Error("unexpected key selected")
		}
		w.Write([]byte(`{"books":[]}`))
	}))
	defer s.Close()
	for _, initial := range []string{"", "expired-key"} {
		if initial != "" {
			if e := cfg.SaveKey(initial); e != nil {
				t.Fatal(e)
			}
		}
		out, prompt, e := loginRun(t, "fresh-key\n", "--api-url", s.URL, "login", "--json")
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(prompt, "https://weread.qq.com/r/weread-skills") || !strings.Contains(prompt, "请粘贴") {
			t.Fatal("missing acquisition/paste guidance")
		}
		if !json.Valid([]byte(out)) || strings.Contains(out+prompt, "fresh-key") {
			t.Fatal("key echo or non-JSON stdout")
		}
		key, source, e := cfg.Key()
		if e != nil || key != "fresh-key" || source != "file" {
			t.Fatal("validated key not saved")
		}
		if !strings.Contains(out, `"command": "weread-cli sync --json"`) {
			t.Fatal("missing next sync command")
		}
	}
	t.Setenv("WEREAD_API_KEY", "expired-key")
	requests = nil
	_, prompt, e := loginRun(t, "", "--api-url", s.URL, "login", "--json")
	if e != nil || len(requests) != 1 || requests[0] != "fresh-key" || strings.Contains(prompt, "请粘贴") {
		t.Fatal("saved key did not win over environment")
	}
}

func TestLoginFailurePreservesSavedKey(t *testing.T) {
	home := cliFixture(t)
	cfg, _ := config.Load(home, "")
	cfg.SaveKey("previous-key")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer s.Close()
	_, prompt, e := loginRun(t, "invalid-new-key\n", "--api-url", s.URL, "login", "--json")
	if e == nil || !strings.Contains(prompt, "请粘贴") {
		t.Fatal("rejected key did not prompt")
	}
	key, _, _ := cfg.Key()
	if key != "previous-key" {
		t.Fatal("invalid replacement overwrote saved key")
	}
	_, _, e = loginRun(t, "invalid-new-key\n", "--api-url", s.URL, "login", "--key-stdin", "--json")
	if e == nil {
		t.Fatal("invalid stdin key accepted")
	}
	key, _, _ = cfg.Key()
	if key != "previous-key" {
		t.Fatal("invalid stdin replacement overwrote saved key")
	}
}

func TestLoginNetworkFailureIsNotExpiryAndEmptyInputGuides(t *testing.T) {
	home := cliFixture(t)
	cfg, _ := config.Load(home, "")
	cfg.SaveKey("previous-key")
	_, prompt, e := loginRun(t, "replacement-key\n", "--api-url", "http://127.0.0.1:1", "login")
	if e == nil || strings.Contains(prompt, "请粘贴") {
		t.Fatal("network failure treated as an expired login")
	}
	key, _, _ := cfg.Key()
	if key != "previous-key" {
		t.Fatal("network error changed credentials")
	}
	os.Remove(cfg.AuthPath())
	_, prompt, e = loginRun(t, "", "login", "--json")
	if e == nil || !strings.Contains(prompt, "https://weread.qq.com/r/weread-skills") {
		t.Fatal("empty stdin lacks human guidance")
	}
}

func TestStatusReportsUpdatesSkillsAndCommands(t *testing.T) {
	home := cliFixture(t)
	cfg, _ := config.Load(home, "")
	cfg.SaveKey("saved-key")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"books":[]}`)) }))
	defer s.Close()
	newest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("secret sent to GitHub")
		}
		w.Write([]byte(`{"tag_name":"v0.3.0"}`))
	}))
	defer newest.Close()
	var out, errOut bytes.Buffer
	e := execute(context.Background(), &runtime{version: "v0.2.0", latest: update.Checker{URL: newest.URL}, in: strings.NewReader(""), out: &out, errOut: &errOut}, []string{"--api-url", s.URL, "status", "--json"})
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Authenticated  bool
		Authentication struct{ State string }
		CLI            update.Status
		Skills         struct{ Ready bool }
		Guidance       []action
	}
	if e = json.Unmarshal(out.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if !v.Authenticated || v.Authentication.State != "valid" || !v.CLI.UpdateAvailable || v.Skills.Ready || len(v.Guidance) < 2 {
		t.Fatalf("incomplete status: %s", out.String())
	}
	commands := map[string]bool{}
	for _, g := range v.Guidance {
		commands[g.Command] = true
	}
	if !commands[update.InstallCommand] || !commands["weread-cli skills install"] {
		t.Fatal("missing executable repair commands")
	}
	if _, e = run(t, "skills", "install", "--json"); e != nil {
		t.Fatal(e)
	}
	for _, dir := range []string{".agents", ".claude"} {
		if _, e = os.Stat(filepath.Join(home, dir, "skills", "weread-cli", "SKILL.md")); e != nil {
			t.Fatal(e)
		}
	}
	local, e := run(t, "status", "--offline", "--json")
	if e != nil || !strings.Contains(local, `"ready": true`) || !strings.Contains(local, `"state": "offline"`) {
		t.Fatalf("offline installed status: %s %v", local, e)
	}
}
