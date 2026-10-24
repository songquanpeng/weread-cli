package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/songquanpeng/weread-cli/internal/schedule"
	"github.com/songquanpeng/weread-cli/internal/service"
	"github.com/songquanpeng/weread-cli/internal/weread"
	"github.com/spf13/cobra"
)

func (r *runtime) login() *cobra.Command {
	var stdin bool
	c := &cobra.Command{Use: "login", Short: "Validate and privately save an API key from env or stdin", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		key := strings.TrimSpace(os.Getenv("WEREAD_API_KEY"))
		if stdin {
			b, e := io.ReadAll(io.LimitReader(r.in, 8193))
			if e != nil {
				return e
			}
			if len(b) > 8192 {
				return errors.New("API key input too long")
			}
			key = strings.TrimSpace(string(b))
		}
		if key == "" || strings.ContainsAny(key, "\r\n\t ") {
			return errors.New("set WEREAD_API_KEY or pipe a single key into weread-cli login --key-stdin")
		}
		client, e := weread.New(r.endpoint, key)
		if e != nil {
			return e
		}
		if _, e = client.Call(cmd.Context(), "/user/notebooks", map[string]any{"count": 1}); e != nil {
			return e
		}
		cfg, e := r.cfg()
		if e != nil {
			return e
		}
		if e = cfg.SaveKey(key); e != nil {
			return e
		}
		return r.present(map[string]any{"authenticated": true, "credential_source": "file", "auth_path": cfg.AuthPath()})
	}}
	c.Flags().BoolVar(&stdin, "key-stdin", false, "read API key from stdin instead of the environment")
	return c
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
