package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/songquanpeng/weread-cli/internal/domain"
	"github.com/songquanpeng/weread-cli/internal/skills"
	"github.com/songquanpeng/weread-cli/internal/update"
	"github.com/songquanpeng/weread-cli/internal/weread"
	"github.com/spf13/cobra"
)

type action struct {
	Reason  string `json:"reason"`
	Command string `json:"command"`
}
type health struct {
	CLI      update.Status `json:"cli"`
	Skills   skills.Status `json:"skills"`
	Guidance []action      `json:"guidance"`
}

func (r *runtime) health(ctx context.Context, offline bool) (health, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return health{}, e
	}
	h := health{CLI: r.latest.Check(ctx, r.version, offline), Skills: skills.Check(home, r.version), Guidance: []action{}}
	if h.CLI.UpdateAvailable {
		h.Guidance = append(h.Guidance, action{Reason: "cli_update_available", Command: update.InstallCommand})
	}
	if !h.Skills.Ready || h.CLI.UpdateAvailable {
		h.Guidance = append(h.Guidance, action{Reason: "install_matching_skill", Command: h.Skills.InstallCommand})
	}
	return h, nil
}

func (r *runtime) hint(actions []action) {
	for _, a := range actions {
		fmt.Fprintf(r.errOut, "%s: %s\n", a.Reason, a.Command)
	}
}

func (r *runtime) skills() *cobra.Command {
	c := &cobra.Command{Use: "skills", Aliases: []string{"skill"}, Short: "Install/check the embedded companion skill for Agents and Claude"}
	install := &cobra.Command{Use: "install", Aliases: []string{"update"}, Short: "Install this binary's skill into ~/.agents/skills and ~/.claude/skills", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		home, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		v, e := skills.Install(home, r.version)
		if e != nil {
			return e
		}
		return r.write(v)
	}}
	status := &cobra.Command{Use: "status", Short: "Check local skill installation and version compatibility (offline)", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		home, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		v := skills.Check(home, r.version)
		if !v.Ready {
			r.hint([]action{{Reason: "install_matching_skill", Command: v.InstallCommand}})
		}
		return r.write(v)
	}}
	c.AddCommand(install, status)
	return c
}

func (r *runtime) status() *cobra.Command {
	var offline bool
	c := &cobra.Command{Use: "status", Short: "Check login, CLI updates, companion skills and local sync status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, e := r.cfg()
		if e != nil {
			return e
		}
		key, source, keyErr := cfg.Key()
		auth := struct {
			State   string `json:"state"`
			Checked bool   `json:"checked"`
			Error   string `json:"error,omitempty"`
		}{State: "missing"}
		authenticated := false
		if keyErr != nil {
			auth.State = "invalid"
			auth.Error = keyErr.Error()
		}
		if key != "" {
			auth.State = "unverified"
			authenticated = true
			if !offline {
				ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
				client, err := r.client()
				if err == nil {
					_, err = client.Call(ctx, "/user/notebooks", map[string]any{"count": 1})
				}
				cancel()
				auth.Checked = true
				authenticated = err == nil
				auth.State = "valid"
				if err != nil {
					var rejected *weread.AuthenticationError
					auth.State = "unknown"
					if errors.As(err, &rejected) {
						auth.State = "invalid"
					}
					auth.Error = err.Error()
				}
			}
		}
		h, e := r.health(cmd.Context(), offline)
		if e != nil {
			return e
		}
		if auth.State == "missing" || auth.State == "invalid" {
			h.Guidance = append(h.Guidance, action{Reason: "login_required", Command: "weread-cli login"})
		}
		s, e := r.store()
		if e != nil {
			return e
		}
		last, e := s.LastSync(cmd.Context())
		s.Close()
		if e != nil {
			return e
		}
		if last == nil {
			h.Guidance = append(h.Guidance, action{Reason: "initial_sync_required", Command: "weread-cli sync --json"})
		}
		r.hint(h.Guidance)
		return r.write(struct {
			Authenticated    bool               `json:"authenticated"`
			CredentialSource string             `json:"credential_source"`
			Authentication   any                `json:"authentication"`
			Home             string             `json:"home"`
			Database         string             `json:"database"`
			LastSync         *domain.SyncResult `json:"last_sync"`
			health
		}{authenticated, source, auth, cfg.Home, cfg.DB, last, h})
	}}
	c.Flags().BoolVar(&offline, "offline", false, "skip remote login validation and latest-release lookup; still inspect local skills")
	return c
}
