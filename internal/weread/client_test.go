package weread

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect request profile")
		}
		var p map[string]any
		if e := json.NewDecoder(r.Body).Decode(&p); e != nil {
			t.Fatal(e)
		}
		if p["api_name"] != "/user/notebooks" || p["skill_version"] != SkillVersion || p["count"] != float64(100) || p["lastSort"] != float64(123) {
			t.Errorf("business params must be flat: %v", p)
		}
		if _, ok := p["params"]; ok {
			t.Error("nested params")
		}
		w.Write([]byte(`{"books":[],"hasMore":0}`))
	}))
	defer srv.Close()
	c, e := New(srv.URL, "test-key")
	if e != nil {
		t.Fatal(e)
	}
	c.Interval = 0
	if _, e = c.Call(context.Background(), "/user/notebooks", map[string]any{"count": 100, "lastSort": 123}); e != nil {
		t.Fatal(e)
	}
	for _, p := range []map[string]any{{"api_name": "/shelf/sync"}, {"skill_version": "old"}, {"params": map[string]any{}}} {
		if _, e = c.Call(context.Background(), "/user/notebooks", p); e == nil {
			t.Fatal("reserved fields accepted")
		}
	}
	if _, e = c.Call(context.Background(), "/review/delete", nil); e == nil {
		t.Fatal("write API accepted")
	}
}

func TestErrorsAndUpgradeStop(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		upgrade    bool
	}{
		{"expired", `{"errmsg":"test-key"}`, 401, false},
		{"business", `{"errcode":42,"errmsg":"rejected test-key"}`, 200, false},
		{"upgrade", `{"upgrade_info":{"message":"new skill test-key"},"books":[]}`, 200, true},
		{"invalid", `<html>test-key</html>`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer srv.Close()
			c, _ := New(srv.URL, "test-key")
			_, e := c.Call(context.Background(), "/shelf/sync", nil)
			if e == nil || strings.Contains(e.Error(), "test-key") {
				t.Fatalf("unsafe error: %v", e)
			}
			var u *UpgradeError
			if errors.As(e, &u) != tc.upgrade {
				t.Fatalf("upgrade type: %v", e)
			}
		})
	}
}

func TestNoRedirectAndBoundedReadRetry(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "test-key")
	if _, e := c.Call(context.Background(), "/shelf/sync", nil); e == nil || forwarded {
		t.Fatal("authorization followed redirect")
	}
	attempts := 0
	retry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"books":[]}`))
	}))
	defer retry.Close()
	c, _ = New(retry.URL, "test-key")
	c.Interval = 0
	if _, e := c.Call(context.Background(), "/shelf/sync", nil); e != nil || attempts != 2 {
		t.Fatalf("retry %d: %v", attempts, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.Call(ctx, "/shelf/sync", nil); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancellation: %v", e)
	}
}

func TestTransportRestrictions(t *testing.T) {
	for _, url := range []string{"http://example.com/api", "https://user:password@example.com", "https://example.com?key=x", "ftp://example.com"} {
		if _, e := New(url, "test-key"); e == nil {
			t.Fatalf("unsafe URL accepted: %s", url)
		}
	}
	if _, e := New(GatewayURL, ""); e == nil {
		t.Fatal("empty key accepted")
	}
	var f Flag
	for _, s := range []string{"0", "1", "true", "false"} {
		if e := json.Unmarshal([]byte(s), &f); e != nil {
			t.Fatal(e)
		}
	}
	if e := json.Unmarshal([]byte("2"), &f); e == nil {
		t.Fatal("ambiguous flag accepted")
	}
}
