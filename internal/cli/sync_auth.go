package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/songquanpeng/weread-cli/internal/config"
	"github.com/songquanpeng/weread-cli/internal/schedule"
	"github.com/songquanpeng/weread-cli/internal/service"
	"github.com/songquanpeng/weread-cli/internal/weread"
	"github.com/spf13/cobra"
)

func (r *runtime) login() *cobra.Command {
	var stdin bool
	c := &cobra.Command{Use: "login", Short: "Check your saved login, or paste and privately save a new API key", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, e := r.cfg()
		if e != nil {
			return e
		}
		key := ""
		if !stdin {
			key, _, e = cfg.Key()
			if e != nil && !errors.Is(e, config.ErrInvalidCredentials) {
				return e
			}
			if key != "" && !strings.ContainsAny(key, "\r\n\t ") {
				e = r.validateKey(cmd, key)
				var rejected *weread.AuthenticationError
				if e != nil && !errors.As(e, &rejected) {
					return e
				}
				if e != nil {
					key = ""
				}
			}
		}
		if stdin || key == "" || strings.ContainsAny(key, "\r\n\t ") {
			if !stdin {
				fmt.Fprintln(r.errOut, "没有有效登录态，请打开以下页面获取 API Key：\nhttps://weread.qq.com/r/weread-skills")
				fmt.Fprint(r.errOut, "请粘贴 API Key（终端输入不回显）： ")
			}
			key, e = r.readLoginKey(stdin)
			if e != nil {
				return e
			}
			if e = r.validateKey(cmd, key); e != nil {
				return fmt.Errorf("%w; 获取新的 API Key：https://weread.qq.com/r/weread-skills", e)
			}
		}
		if e = cfg.SaveKey(key); e != nil {
			return e
		}
		h, e := r.health(cmd.Context(), false)
		if e != nil {
			return e
		}
		r.hint(h.Guidance)
		return r.present(struct {
			Authenticated    bool              `json:"authenticated"`
			CredentialSource string            `json:"credential_source"`
			AuthPath         string            `json:"auth_path"`
			Next             map[string]string `json:"next"`
			health
		}{true, "file", cfg.AuthPath(), map[string]string{"command": "weread-cli sync --json"}, h})
	}}
	c.Flags().BoolVar(&stdin, "key-stdin", false, "validate and replace the saved API key using stdin")
	return c
}

func (r *runtime) validateKey(cmd *cobra.Command, key string) error {
	client, e := weread.New(r.endpoint, key)
	if e != nil {
		return e
	}
	_, e = client.Call(cmd.Context(), "/user/notebooks", map[string]any{"count": 1})
	return e
}

func (r *runtime) logout() *cobra.Command {
	return &cobra.Command{Use: "logout", Short: "Remove the saved API key, keeping local data", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		c, e := r.cfg()
		if e != nil {
			return e
		}
		if e = os.Remove(c.AuthPath()); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		return r.present(map[string]any{"saved_key_removed": true, "environment_key_present": strings.TrimSpace(os.Getenv("WEREAD_API_KEY")) != ""})
	}}
}

func (r *runtime) sync() *cobra.Command {
	var full, watch bool
	var interval, refresh time.Duration
	var progress int
	var years []int
	c := &cobra.Command{Use: "sync", Short: "Atomically mirror shelf, personal notes and reading data", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if interval < time.Minute {
			return errors.New("interval must be at least 1m")
		}
		if refresh < 0 {
			return errors.New("refresh-after cannot be negative")
		}
		if progress < 0 || progress > 100 {
			return errors.New("progress-limit must be 0..100")
		}
		client, e := r.client()
		if e != nil {
			return e
		}
		s, e := r.store()
		if e != nil {
			return e
		}
		defer s.Close()
		loc, _ := time.LoadLocation(r.timezone)
		syncer := &service.Syncer{Gateway: client, Store: s, Location: loc, Progress: func(done, total int) {
			if done == 1 || done%10 == 0 || done == total {
				fmt.Fprintf(r.errOut, "Sync notebooks: %d/%d\n", done, total)
			}
		}}
		for {
			result, e := syncer.Sync(cmd.Context(), service.Options{Full: full, RefreshAfter: refresh, ProgressLimit: progress, Years: years})
			if e != nil {
				var upgrade *weread.UpgradeError
				if !watch || errors.As(e, &upgrade) {
					return e
				}
				fmt.Fprintln(r.errOut, "Sync failed:", e)
			} else if watch && r.wantsJSON() {
				if e = json.NewEncoder(r.out).Encode(result); e != nil {
					return e
				}
			} else if r.wantsJSON() {
				if e = r.write(result); e != nil {
					return e
				}
			} else {
				fmt.Fprintf(r.out, "Synced %d books, %d shelf items, %d notebooks; %d exportable notes (%d fetched, %d skipped).\n", result.Books, result.ShelfItems, result.Notebooks, result.ExportableNotes, result.BooksFetched, result.BooksSkipped)
			}
			if !watch {
				return nil
			}
			t := time.NewTimer(interval)
			select {
			case <-cmd.Context().Done():
				t.Stop()
				return nil
			case <-t.C:
			}
		}
	}}
	f := c.Flags()
	f.BoolVar(&full, "full", false, "refresh every notebook and historical reading year")
	f.BoolVar(&watch, "watch", false, "keep syncing in the foreground until interrupted")
	f.DurationVar(&interval, "interval", time.Hour, "delay between completed syncs with --watch")
	f.DurationVar(&refresh, "refresh-after", 24*time.Hour, "force content refresh after this age even if counts are unchanged")
	f.IntVar(&progress, "progress-limit", 5, "cache progress for this many recently read shelf books (0 disables)")
	f.IntSliceVar(&years, "years", nil, "additional historical reading years (comma-separated)")
	return c
}

func (r *runtime) schedule() *cobra.Command {
	c := &cobra.Command{Use: "schedule", Short: "Manage user-scoped periodic sync (launchd/systemd)"}
	manager := func() (schedule.Manager, error) {
		cfg, e := r.cfg()
		if e != nil {
			return schedule.Manager{}, e
		}
		exe, e := executable()
		return schedule.Manager{Config: cfg, Executable: exe, Timezone: r.timezone}, e
	}
	var interval time.Duration
	install := &cobra.Command{Use: "install", Short: "Install and start periodic sync using the saved key", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		if r.endpoint != "" {
			return errors.New("scheduled jobs use the official gateway; omit --api-url")
		}
		m, e := manager()
		if e != nil {
			return e
		}
		v, e := m.Install(interval)
		if e != nil {
			return e
		}
		return r.present(v)
	}}
	install.Flags().DurationVar(&interval, "interval", time.Hour, "sync interval (at least 1m; default 1h)")
	status := &cobra.Command{Use: "status", Short: "Inspect the scheduler registration", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		m, e := manager()
		if e != nil {
			return e
		}
		v, e := m.Status()
		if e != nil {
			return e
		}
		return r.present(v)
	}}
	remove := &cobra.Command{Use: "remove", Short: "Stop and remove periodic sync, keeping credentials and data", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		m, e := manager()
		if e != nil {
			return e
		}
		v, e := m.Remove()
		if e != nil {
			return e
		}
		return r.present(v)
	}}
	c.AddCommand(install, status, remove)
	return c
}
