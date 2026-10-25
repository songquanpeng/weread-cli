package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallBothTargetsAndMatchBinary(t *testing.T) {
	home := t.TempDir()
	v := Check(home, "v0.2.0")
	if v.Ready || len(v.Targets) != 2 {
		t.Fatal("missing installation not detected")
	}
	v, e := Install(home, "v0.2.0")
	if e != nil || !v.Ready {
		t.Fatalf("install: %+v %v", v, e)
	}
	for _, target := range v.Targets {
		if target.Version != "v0.2.0" || !target.Matches {
			t.Fatalf("incorrect embedded version: %+v", target)
		}
		b, e := os.ReadFile(target.Path)
		if e != nil || string(b) != string(Content("v0.2.0")) {
			t.Fatal("installed file is not embedded skill")
		}
		if e = os.WriteFile(filepath.Join(filepath.Dir(target.Path), "personal.txt"), []byte("keep"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if Check(home, "v0.2.1").Ready {
		t.Fatal("older skill accepted as matching newer binary")
	}
	v, e = Install(home, "v0.2.1")
	if e != nil || !v.Ready {
		t.Fatal(e)
	}
	for _, target := range v.Targets {
		if _, e = os.Stat(filepath.Join(filepath.Dir(target.Path), "personal.txt")); e != nil {
			t.Fatal("update removed unrelated content")
		}
	}
	if e = os.WriteFile(v.Targets[0].Path, []byte("modified"), 0600); e != nil {
		t.Fatal(e)
	}
	if Check(home, "v0.2.1").Ready {
		t.Fatal("modified skill not detected")
	}
}

func TestPartialInstallReportsFailure(t *testing.T) {
	home := t.TempDir()
	if e := os.WriteFile(filepath.Join(home, ".claude"), []byte("obstruction"), 0600); e != nil {
		t.Fatal(e)
	}
	v, e := Install(home, "v0.2.0")
	if e == nil || v.Ready {
		t.Fatal("partial install reported success")
	}
}
