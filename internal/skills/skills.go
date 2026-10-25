// Package skills ships the companion instructions inside the executable.
package skills

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

//go:embed weread-cli/SKILL.md
var template string

type Target struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Matches   bool   `json:"matches"`
	Error     string `json:"error,omitempty"`
}

type Status struct {
	ExpectedVersion string   `json:"expected_version"`
	Ready           bool     `json:"ready"`
	Targets         []Target `json:"targets"`
	InstallCommand  string   `json:"install_command"`
}

func Content(version string) []byte {
	// strconv.Quote produces a valid YAML double-quoted scalar as well as JSON.
	return []byte(strings.ReplaceAll(template, `"{{CLI_VERSION}}"`, strconv.Quote(version)))
}

func Check(home, version string) Status {
	v := Status{ExpectedVersion: version, Ready: true, InstallCommand: "weread-cli skills install", Targets: []Target{}}
	expected := sha256.Sum256(Content(version))
	for _, name := range []string{".agents", ".claude"} {
		t := Target{Name: strings.TrimPrefix(name, "."), Path: filepath.Join(home, name, "skills", "weread-cli", "SKILL.md")}
		data, err := os.ReadFile(t.Path)
		if err == nil {
			t.Installed = true
			t.Matches = sha256.Sum256(data) == expected
			if m := regexp.MustCompile(`(?m)^  version: ("[^\r\n]*")$`).FindSubmatch(data); len(m) > 1 {
				t.Version, _ = strconv.Unquote(string(m[1]))
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Error = err.Error()
		}
		v.Ready = v.Ready && t.Matches
		v.Targets = append(v.Targets, t)
	}
	return v
}

func Install(home, version string) (Status, error) {
	v := Check(home, version)
	for _, t := range v.Targets {
		if t.Matches {
			continue
		}
		if err := installFile(t.Path, Content(version)); err != nil {
			return Check(home, version), fmt.Errorf("install %s skill: %w", t.Name, err)
		}
	}
	return Check(home, version), nil
}

func installFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".SKILL-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
