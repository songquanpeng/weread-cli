package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCredentialsPermissionsAndEnvPrecedence(t *testing.T) {
	c, e := Load(t.TempDir(), "")
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("WEREAD_API_KEY", "")
	if e = c.SaveKey("test-secret"); e != nil {
		t.Fatal(e)
	}
	key, source, e := c.Key()
	if e != nil || key != "test-secret" || source != "file" {
		t.Fatalf("key source %s: %v", source, e)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{c.Home, c.AuthPath()} {
			i, e := os.Stat(p)
			if e != nil {
				t.Fatal(e)
			}
			if i.Mode().Perm()&0077 != 0 {
				t.Fatalf("unsafe mode: %s %v", filepath.Base(p), i.Mode())
			}
		}
	}
	t.Setenv("WEREAD_API_KEY", "env-key")
	key, source, e = c.Key()
	if e != nil || key != "env-key" || source != "environment" {
		t.Fatal("environment did not take precedence")
	}
	if e = c.SaveKey("key\nother"); e == nil {
		t.Fatal("multiline secret accepted")
	}
}
