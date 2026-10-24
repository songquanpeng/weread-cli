package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/songquanpeng/weread-cli/internal/domain"
	"github.com/songquanpeng/weread-cli/internal/storage"
	"github.com/spf13/cobra"
)

func (r *runtime) online(cmd *cobra.Command, api string, p map[string]any) error {
	client, e := r.client()
	if e != nil {
		return e
	}
	data, e := client.Call(cmd.Context(), api, p)
	if e != nil {
		return e
	}
	return r.write(struct {
		API    string          `json:"api"`
		Params map[string]any  `json:"params"`
		Data   json.RawMessage `json:"data"`
	}{api, p, data})
}

func (r *runtime) cached(cmd *cobra.Command, api string, p map[string]any, refresh bool) error {
	s, e := r.store()
	if e != nil {
		return e
	}
	defer s.Close()
	key := storage.SnapshotKey(api, p)
	if refresh {
		client, e := r.client()
		if e != nil {
			return e
		}
		data, e := client.Call(cmd.Context(), api, p)
		if e != nil {
			return e
		}
		params, _ := json.Marshal(p)
		if p == nil {
			params = []byte("{}")
		}
		snap := domain.Snapshot{Key: key, API: api, Params: params, Data: data, SyncedAt: time.Now().UTC().Format(time.RFC3339)}
		if e = s.Cache(cmd.Context(), snap); e != nil {
			return e
		}
		return r.write(snap)
	}
	snap, e := s.Snapshot(cmd.Context(), key)
	if e != nil {
		return e
	}
	return r.write(snap)
}

func (r *runtime) shelf() *cobra.Command {
	var refresh bool
	c := &cobra.Command{Use: "shelf", Short: "Show the cached shelf snapshot; --refresh fetches remotely", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return r.cached(cmd, "/shelf/sync", nil, refresh) }}
	c.Flags().BoolVar(&refresh, "refresh", false, "fetch and cache the current shelf")
	return c
}

func (r *runtime) bookView() *cobra.Command {
	var view string
	var refresh, raw bool
	c := &cobra.Command{Use: "book <book-id>", Short: "Show cached book info, chapters or progress; --refresh goes online", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		api := map[string]string{"info": "/book/info", "chapters": "/book/chapterinfo", "progress": "/book/getprogress"}[view]
		if api == "" {
			return errors.New("view must be info, chapters or progress")
		}
		if view == "info" && !refresh {
			s, e := r.store()
			if e != nil {
				return e
			}
			defer s.Close()
			if snap, e := s.Snapshot(cmd.Context(), storage.SnapshotKey(api, map[string]any{"bookId": args[0]})); e == nil {
				return r.write(snap)
			}
			b, e := s.Book(cmd.Context(), args[0])
			if e != nil {
				return e
			}
			if !raw {
				b.Raw = nil
			}
			return r.write(b)
		}
		return r.cached(cmd, api, map[string]any{"bookId": args[0]}, refresh)
	}}
	f := c.Flags()
	f.StringVar(&view, "view", "info", "info, chapters or progress")
	f.BoolVar(&refresh, "refresh", false, "fetch and cache the requested view")
	f.BoolVar(&raw, "include-raw", false, "include original gateway metadata for local book info")
	return c
}

func (r *runtime) reading() *cobra.Command {
	var mode, date string
	var refresh bool
	c := &cobra.Command{Use: "reading", Short: "Show cached reading data; durations are seconds; --refresh goes online", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if mode != "weekly" && mode != "monthly" && mode != "annually" && mode != "overall" {
			return errors.New("mode must be weekly, monthly, annually or overall")
		}
		loc, _ := time.LoadLocation(r.timezone)
		t := time.Now().In(loc)
		base := int64(0)
		if date != "" {
			var e error
			t, e = time.ParseInLocation("2006-01-02", date, loc)
			if e != nil {
				return errors.New("date must be YYYY-MM-DD")
			}
			if mode == "overall" {
				return errors.New("overall mode does not accept --date")
			}
		}
		if mode == "annually" {
			base = time.Date(t.Year(), 1, 1, 0, 0, 0, 0, loc).Unix()
		} else if date != "" {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
			if mode == "monthly" {
				t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
			} else {
				days := (int(t.Weekday()) + 6) % 7
				t = t.AddDate(0, 0, -days)
			}
			base = t.Unix()
		}
		return r.cached(cmd, "/readdata/detail", map[string]any{"mode": mode, "baseTime": base}, refresh)
	}}
	f := c.Flags()
	f.StringVar(&mode, "mode", "monthly", "weekly, monthly, annually or overall")
	f.StringVar(&date, "date", "", "a date in the requested historical period (YYYY-MM-DD)")
	f.BoolVar(&refresh, "refresh", false, "fetch and cache the requested period")
	return c
}

func (r *runtime) remote() *cobra.Command {
	c := &cobra.Command{Use: "remote", Short: "Explicitly online book discovery and public content (separate from personal notes)"}
	var scope, count, max int
	search := &cobra.Command{Use: "search <keyword>", Short: "Search the online catalog (scope 10=ebooks, 0=all, 12=full text)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if count < 0 || count > 100 || max < 0 {
			return errors.New("count must be 0..100; max-idx cannot be negative")
		}
		valid := map[int]bool{0: true, 10: true, 16: true, 14: true, 6: true, 12: true, 13: true, 2: true, 4: true}
		if !valid[scope] {
			return errors.New("unsupported scope; see --help")
		}
		p := map[string]any{"keyword": args[0], "scope": scope, "maxIdx": max}
		if count > 0 {
			p["count"] = count
		}
		return r.online(cmd, "/store/search", p)
	}}
	f := search.Flags()
	f.IntVar(&scope, "scope", 10, "0 all, 10 ebooks, 16 web novels, 14 audio, 6 authors, 12 full text, 13 lists, 2 accounts, 4 articles")
	f.IntVar(&count, "count", 0, "page size (0 uses server default)")
	f.IntVar(&max, "max-idx", 0, "last searchIdx from previous response")
	var similar, session string
	var recCount, recMax int
	recommend := &cobra.Command{Use: "recommend", Short: "Get personalized recommendations, or --similar <book-id>", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if recCount < 1 || recCount > 100 || recMax < 0 {
			return errors.New("count must be 1..100; max-idx cannot be negative")
		}
		p := map[string]any{"count": recCount, "maxIdx": recMax}
		api := "/book/recommend"
		if similar != "" {
			api = "/book/similar"
			p["bookId"] = similar
			if session != "" {
				p["sessionId"] = session
			}
		} else if session != "" {
			return errors.New("session requires --similar")
		}
		return r.online(cmd, api, p)
	}}
	f = recommend.Flags()
	f.StringVar(&similar, "similar", "", "recommend books similar to this book ID")
	f.StringVar(&session, "session", "", "similar recommendation sessionId for pagination")
	f.IntVar(&recCount, "count", 12, "page size")
	f.IntVar(&recMax, "max-idx", 0, "last idx/searchIdx from the previous response")
	var chapter int64
	popular := &cobra.Command{Use: "popular <book-id>", Short: "Get up to 20 public popular highlights (no pagination)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if chapter < 0 {
			return errors.New("chapter cannot be negative")
		}
		return r.online(cmd, "/book/bestbookmarks", map[string]any{"bookId": args[0], "chapterUid": chapter, "synckey": 0})
	}}
	popular.Flags().Int64Var(&chapter, "chapter", 0, "chapter UID (0 for whole book)")
	var typ, reviewCount, reviewMax int
	var syncKey int64
	reviews := &cobra.Command{Use: "reviews <book-id>", Short: "Get other readers' PUBLIC reviews (not personal notes)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if typ < 0 || typ > 4 || reviewCount < 1 || reviewCount > 100 || reviewMax < 0 || syncKey < 0 {
			return errors.New("invalid review pagination/filter")
		}
		return r.online(cmd, "/review/list", map[string]any{"bookId": args[0], "reviewListType": typ, "count": reviewCount, "maxIdx": reviewMax, "synckey": syncKey})
	}}
	f = reviews.Flags()
	f.IntVar(&typ, "type", 0, "0 all, 1 recommended, 2 negative, 3 latest, 4 average")
	f.IntVar(&reviewCount, "count", 20, "page size")
	f.IntVar(&reviewMax, "max-idx", 0, "last idx from the previous response")
	f.Int64Var(&syncKey, "sync-key", 0, "previous response synckey")
	review := &cobra.Command{Use: "review <review-id>", Short: "Get a remote review and its comments", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return r.online(cmd, "/review/single", map[string]any{"reviewId": args[0]})
	}}
	c.AddCommand(search, recommend, popular, reviews, review)
	return c
}

func (r *runtime) api() *cobra.Command {
	var params string
	c := &cobra.Command{Use: "api <api-name>", Short: "Call an allowlisted official read API with flat JSON parameters (online)", Args: cobra.ExactArgs(1), Example: `  weread-cli api /_list --json
  weread-cli api /user/notebooks --params '{"count":100,"lastSort":1700000000}' --json`, RunE: func(cmd *cobra.Command, args []string) error {
		var p map[string]any
		d := json.NewDecoder(bytes.NewBufferString(params))
		d.UseNumber()
		if e := d.Decode(&p); e != nil || p == nil {
			return errors.New("params must be one JSON object")
		}
		if e := d.Decode(new(any)); e != io.EOF {
			return errors.New("params must contain exactly one JSON object")
		}
		return r.online(cmd, args[0], p)
	}}
	c.Flags().StringVar(&params, "params", "{}", "flat JSON object; api_name and skill_version are added automatically")
	return c
}
