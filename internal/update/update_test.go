package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseVersionsAndOffline(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Error("credentials sent to release API")
		}
		w.Write([]byte(`{"tag_name":"v0.10.0"}`))
	}))
	defer s.Close()
	c := Checker{URL: s.URL, HTTP: s.Client()}
	v := c.Check(context.Background(), "v0.2.0", false)
	if !v.UpdateAvailable || v.State != "outdated" || v.UpdateCommand != InstallCommand {
		t.Fatalf("upgrade: %+v", v)
	}
	v = c.Check(context.Background(), "v0.10.0", false)
	if v.UpdateAvailable || v.State != "current" {
		t.Fatalf("current: %+v", v)
	}
	v = c.Check(context.Background(), "v0.11.0", false)
	if v.UpdateAvailable {
		t.Fatal("downgrade suggested")
	}
	v = c.Check(context.Background(), "dev", false)
	if v.State != "development" {
		t.Fatal("development build treated as release")
	}
	before := calls
	v = c.Check(context.Background(), "v0.2.0", true)
	if calls != before || v.State != "offline" {
		t.Fatal("offline lookup contacted network")
	}
}

func TestUnavailableReleaseIsNotUpToDate(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer s.Close()
	v := (Checker{URL: s.URL}).Check(context.Background(), "v0.2.0", false)
	if v.State != "unknown" || v.Latest != "" || v.Error == "" {
		t.Fatalf("unavailable check: %+v", v)
	}
}
