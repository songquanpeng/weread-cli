// Package schedule installs user-scoped OS schedulers without putting keys in
// process arguments, environment blocks, or scheduler configuration files.
package schedule

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/songquanpeng/weread-cli/internal/config"
)

type Manager struct {
	Config               config.Config
	Executable, Timezone string
}
type Status struct {
	Supported  bool   `json:"supported"`
	Installed  bool   `json:"installed"`
	Loaded     bool   `json:"loaded"`
	Backend    string `json:"backend"`
	ConfigPath string `json:"config_path"`
	Label      string `json:"label"`
}

func (m Manager) label() string {
	h := sha256.Sum256([]byte(m.Config.DB))
	return fmt.Sprintf("io.github.weread-cli.sync.%x", h[:6])
}
func (m Manager) paths() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", m.label()+".plist"), "launchd", nil
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", m.label()+".timer"), "systemd", nil
	default:
		return "", "", errors.New("OS scheduling supports macOS and Linux; use weread-cli sync --watch --interval 1h on this system")
	}
}

func (m Manager) args() []string {
	return []string{m.Executable, "--home", m.Config.Home, "--db", m.Config.DB, "--timezone", m.Timezone, "sync", "--json"}
}

func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func Plist(label string, args []string, home string, interval time.Duration) ([]byte, error) {
	if interval < time.Minute || interval > 365*24*time.Hour || interval%time.Second != 0 {
		return nil, errors.New("interval must be whole seconds between 1m and 8760h")
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>` + escape(label) + `</string><key>ProgramArguments</key><array>`)
	for _, a := range args {
		b.WriteString("<string>" + escape(a) + "</string>")
	}
	b.WriteString(`</array><key>StartInterval</key><integer>` + strconv.FormatInt(int64(interval/time.Second), 10) + `</integer><key>RunAtLoad</key><true/><key>ProcessType</key><string>Background</string><key>Umask</key><integer>63</integer><key>StandardOutPath</key><string>` + escape(filepath.Join(home, "sync.log")) + `</string><key>StandardErrorPath</key><string>` + escape(filepath.Join(home, "sync-error.log")) + `</string></dict></plist>`)
	return []byte(b.String()), nil
}

func Systemd(label string, args []string, interval time.Duration) ([]byte, []byte, error) {
	if interval < time.Minute || interval > 365*24*time.Hour || interval%time.Second != 0 {
		return nil, nil, errors.New("interval must be whole seconds between 1m and 8760h")
	}
	parts := []string{}
	for _, a := range args {
		if strings.ContainsAny(a, "\r\n\x00") {
			return nil, nil, errors.New("invalid control character in scheduler argument")
		}
		a = strings.ReplaceAll(a, `\`, `\\`)
		a = strings.ReplaceAll(a, `"`, `\"`)
		a = strings.ReplaceAll(a, "%", "%%")
		a = strings.ReplaceAll(a, "$", "$$")
		parts = append(parts, `"`+a+`"`)
	}
	service := []byte("[Unit]\nDescription=WeRead local mirror sync\n\n[Service]\nType=oneshot\nUMask=0077\nExecStart=" + strings.Join(parts, " ") + "\n")
	timer := []byte(fmt.Sprintf("[Unit]\nDescription=Periodic WeRead sync\n\n[Timer]\nOnStartupSec=1m\nOnUnitInactiveSec=%ds\nUnit=%s.service\n\n[Install]\nWantedBy=timers.target\n", int64(interval/time.Second), label))
	return service, timer, nil
}

func (m Manager) Install(interval time.Duration) (Status, error) {
	p, backend, err := m.paths()
	if err != nil {
		return Status{}, err
	}
	if err = m.Config.Ensure(); err != nil {
		return Status{}, err
	}
	// Scheduled processes do not source shell profiles. Require saved credentials.
	data, readErr := os.ReadFile(m.Config.AuthPath())
	var credentials struct {
		Key string `json:"api_key"`
	}
	if readErr != nil || json.Unmarshal(data, &credentials) != nil || strings.TrimSpace(credentials.Key) == "" {
		return Status{}, errors.New("scheduled sync requires a saved API key: run weread-cli login using WEREAD_API_KEY or --key-stdin first")
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return Status{}, err
	}
	if backend == "launchd" {
		data, e := Plist(m.label(), m.args(), m.Config.Home, interval)
		if e != nil {
			return Status{}, e
		}
		if err = os.WriteFile(p, data, 0600); err != nil {
			return Status{}, err
		}
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = exec.Command("launchctl", "bootout", domain+"/"+m.label()).Run()
		if out, e := exec.Command("launchctl", "bootstrap", domain, p).CombinedOutput(); e != nil {
			return Status{}, fmt.Errorf("launchctl bootstrap failed: %s", strings.TrimSpace(string(out)))
		}
	} else {
		service, timer, e := Systemd(m.label(), m.args(), interval)
		if e != nil {
			return Status{}, e
		}
		if err = os.WriteFile(strings.TrimSuffix(p, ".timer")+".service", service, 0600); err != nil {
			return Status{}, err
		}
		if err = os.WriteFile(p, timer, 0600); err != nil {
			return Status{}, err
		}
		for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", m.label() + ".timer"}} {
			if out, e := exec.Command("systemctl", args...).CombinedOutput(); e != nil {
				return Status{}, fmt.Errorf("systemctl failed: %s", strings.TrimSpace(string(out)))
			}
		}
	}
	return m.Status()
}

func (m Manager) Status() (Status, error) {
	p, backend, err := m.paths()
	if err != nil {
		return Status{Supported: false}, nil
	}
	v := Status{Supported: true, Backend: backend, ConfigPath: p, Label: m.label()}
	_, e := os.Stat(p)
	v.Installed = e == nil
	if backend == "launchd" {
		v.Loaded = exec.Command("launchctl", "print", "gui/"+strconv.Itoa(os.Getuid())+"/"+m.label()).Run() == nil
	} else {
		v.Loaded = exec.Command("systemctl", "--user", "is-active", "--quiet", m.label()+".timer").Run() == nil
	}
	return v, nil
}

func (m Manager) Remove() (Status, error) {
	p, backend, err := m.paths()
	if err != nil {
		return Status{}, err
	}
	before, err := m.Status()
	if err != nil {
		return Status{}, err
	}
	if backend == "launchd" {
		if before.Loaded {
			if out, e := exec.Command("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+m.label()).CombinedOutput(); e != nil {
				return Status{}, fmt.Errorf("launchctl bootout failed: %s", strings.TrimSpace(string(out)))
			}
		}
	} else if before.Installed || before.Loaded {
		if out, e := exec.Command("systemctl", "--user", "disable", "--now", m.label()+".timer").CombinedOutput(); e != nil {
			return Status{}, fmt.Errorf("systemctl disable failed: %s", strings.TrimSpace(string(out)))
		}
	}
	if err = os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	if backend == "systemd" {
		if err = os.Remove(strings.TrimSuffix(p, ".timer") + ".service"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Status{}, err
		}
		if err = exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
			return Status{}, err
		}
	}
	return m.Status()
}
