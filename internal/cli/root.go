package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
	_ "time/tzdata" // Keep IANA zones available in standalone binaries.

	"github.com/songquanpeng/weread-cli/internal/config"
	"github.com/songquanpeng/weread-cli/internal/domain"
	"github.com/songquanpeng/weread-cli/internal/storage"
	"github.com/songquanpeng/weread-cli/internal/update"
	"github.com/songquanpeng/weread-cli/internal/weread"
	"github.com/spf13/cobra"
)

type runtime struct {
	version                              string
	latest                               update.Checker
	home, db, endpoint, format, timezone string
	json                                 bool
	in                                   io.Reader
	out, errOut                          io.Writer
}

func Execute(ctx context.Context, version string, args []string, in io.Reader, out, errOut io.Writer) error {
	return execute(ctx, &runtime{version: version, in: in, out: out, errOut: errOut}, args)
}

func execute(ctx context.Context, r *runtime, args []string) error {
	root := &cobra.Command{Use: "weread-cli", Short: "Sync and search your WeRead library, offline first", Version: r.version, SilenceErrors: true, SilenceUsage: true,
		Long: "weread-cli mirrors your WeRead shelf, highlights, personal reviews and reading data into SQLite.\nSearch, list, get, books, stats and export are offline. Run sync for fresh data.\nRemote views use cached data unless --refresh is set. Run agent-guide for scripting guidance.",
		PersistentPreRunE: func(*cobra.Command, []string) error {
			if r.format != "text" && r.format != "json" {
				return fmt.Errorf("format must be text or json")
			}
			_, err := time.LoadLocation(r.timezone)
			return err
		},
	}
	root.SetIn(r.in)
	root.SetOut(r.out)
	root.SetErr(r.errOut)
	root.SetArgs(args)
	f := root.PersistentFlags()
	f.StringVar(&r.home, "home", "", "private state directory (env: WEREAD_HOME)")
	f.StringVar(&r.db, "db", "", "SQLite database (env: WEREAD_DB)")
	f.StringVar(&r.endpoint, "api-url", "", "gateway URL override for testing (HTTPS required)")
	f.StringVar(&r.format, "format", "text", "output format: text or json")
	f.BoolVar(&r.json, "json", false, "emit stable machine-readable JSON")
	f.StringVar(&r.timezone, "timezone", "Asia/Shanghai", "IANA timezone used for dates and reading periods")
	root.AddCommand(r.login(), r.logout(), r.status(), r.sync(), r.search(false), r.search(true), r.get(), r.books(), r.stats(), r.export(), r.schedule(), r.remote(), r.bookView(), r.shelf(), r.reading(), r.api(), r.agentGuide(), r.update(), r.skills())
	return root.ExecuteContext(ctx)
}

func (r *runtime) cfg() (config.Config, error) { return config.Load(r.home, r.db) }
func (r *runtime) store() (*storage.Store, error) {
	c, e := r.cfg()
	if e != nil {
		return nil, e
	}
	if e = c.Ensure(); e != nil {
		return nil, e
	}
	return storage.Open(c.DB)
}
func (r *runtime) client() (*weread.Client, error) {
	c, e := r.cfg()
	if e != nil {
		return nil, e
	}
	key, _, e := c.Key()
	if e != nil {
		return nil, e
	}
	return weread.New(r.endpoint, key)
}
func (r *runtime) wantsJSON() bool { return r.json || r.format == "json" }
func (r *runtime) write(v any) error {
	e := json.NewEncoder(r.out)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func (r *runtime) present(v any) error { return r.write(v) }

func (r *runtime) update() *cobra.Command {
	return &cobra.Command{Use: "update", Short: "Print the install/update command", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		const command = update.InstallCommand
		if r.wantsJSON() {
			return r.write(map[string]string{"command": command})
		}
		_, e := fmt.Fprintln(r.out, command)
		return e
	}}
}

func (r *runtime) agentGuide() *cobra.Command {
	return &cobra.Command{Use: "agent-guide", Short: "Print concise operating instructions for agents", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		_, e := fmt.Fprintln(r.out, `Agent workflow for weread-cli

1. Freshness: check "weread-cli status --json" for last_sync. If it is absent, or
   the user needs current/recent data, run "weread-cli sync --json" BEFORE searching.
   An unsynced empty index does not mean the user's remote library is empty.
2. Discovery: "weread-cli search '主题 关键词' --json --limit 50" searches LOCAL notes,
   quotes, book titles, authors and chapter names. Terms use AND; --any uses OR.
3. Pagination: follow next.command verbatim while has_more is true.
4. Narrowing: --book <book-id>, --kind highlight|review, --from/--to YYYY-MM-DD.
5. Retrieval: "weread-cli get <stable-note-id> --json"; "weread-cli books --json" for books.
6. Analysis: "weread-cli export --output jsonl" streams all local notes.
   --entity books|reading|snapshots exports metadata or raw gateway snapshots.
   --output csv|markdown|json and --book are also supported.
7. Remote discovery: "weread-cli remote search <keyword> --json" (online).
   "weread-cli book <id> --view info|chapters|progress --refresh --json" caches responses.
   shelf/reading/book views use the cache by default; --refresh explicitly goes online.
8. Schedule: "weread-cli schedule install --interval 1h" (macOS/Linux), or
   "weread-cli sync --watch --interval 1h" (foreground, any OS).
9. Authentication: run "weread-cli login" to validate the current key or paste a new
   one from https://weread.qq.com/r/weread-skills. Saved keys take precedence over
   WEREAD_API_KEY. Use "weread-cli login --key-stdin" to explicitly replace a key.
   Never pass keys in argv. logout deletes the saved key; it keeps the local mirror.
10. Sync skips unchanged notebooks fetched within 24h; --full forces content refresh
    and historical reading refresh. A complete successful sync atomically replaces
    the mirror, including remotely deleted notes. Back up exports to retain history.
11. Setup: "weread-cli status --json" checks login, CLI updates and companion skills.
    Follow guidance[].command as needed; update the CLI before "weread-cli skills
    install" to install matching skills into both ~/.agents/skills and ~/.claude/skills.
    Use "weread-cli status --offline --json" when operating without network access.

JSON pagination fields: query, total, count, limit, offset, has_more, items, next.
Note kinds: highlight (划线), review (个人想法/点评). Bookmarks are counts only.
Remote note totals include bookmarks; exportable local notes do not.
Reading durations are seconds; progress is an integer 0..100, never a fraction.
Public reviews and popular highlights are separate from personal notes.
No command downloads full copyrighted book text. Raw JSON is included only when
--include-raw is set, except snapshot/reading exports which are raw by definition.`)
		return e
	}}
}

func stripRaw(notes []domain.Note, include bool) []domain.Note {
	if include {
		return notes
	}
	for i := range notes {
		notes[i].Raw = nil
	}
	return notes
}

func executable() (string, error) { p, e := os.Executable(); return p, e }
