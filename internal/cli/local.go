package cli

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/songquanpeng/weread-cli/internal/domain"
	"github.com/songquanpeng/weread-cli/internal/storage"
	"github.com/spf13/cobra"
)

type noteFlags struct {
	book, kind, from, to, sort string
	any, raw                   bool
	limit, offset              int
}

func (n *noteFlags) bind(c *cobra.Command, paged bool) {
	n.limit = 20
	f := c.Flags()
	f.StringVar(&n.book, "book", "", "filter by exact book ID")
	f.StringVar(&n.kind, "kind", "", "filter: highlight or review")
	f.StringVar(&n.from, "from", "", "created on or after YYYY-MM-DD in --timezone")
	f.StringVar(&n.to, "to", "", "created on or before YYYY-MM-DD (inclusive)")
	f.BoolVar(&n.any, "any", false, "match any query term instead of all terms")
	f.BoolVar(&n.raw, "include-raw", false, "include original gateway metadata")
	if paged {
		f.IntVar(&n.limit, "limit", 20, "page size (1..1000)")
		f.IntVar(&n.offset, "offset", 0, "page offset")
		f.StringVar(&n.sort, "sort", "newest", "newest or oldest")
	}
}

func (n noteFlags) filter(r *runtime, query string) (storage.Filter, error) {
	f := storage.Filter{Query: query, BookID: n.book, Kind: n.kind, Any: n.any, Limit: n.limit, Offset: n.offset, Sort: n.sort}
	loc, e := time.LoadLocation(r.timezone)
	if e != nil {
		return f, e
	}
	for _, v := range []struct {
		input string
		dest  *string
		end   bool
	}{{n.from, &f.From, false}, {n.to, &f.To, true}} {
		if v.input == "" {
			continue
		}
		t, e := time.ParseInLocation("2006-01-02", v.input, loc)
		if e != nil {
			return f, fmt.Errorf("invalid date %q; use YYYY-MM-DD", v.input)
		}
		if v.end {
			t = t.AddDate(0, 0, 1)
		}
		*v.dest = t.Format(time.RFC3339)
	}
	return f, f.Validate()
}

type nextPage struct {
	Offset  int    `json:"offset"`
	Command string `json:"command"`
}
type page struct {
	Query   string    `json:"query"`
	Total   int       `json:"total"`
	Count   int       `json:"count"`
	Limit   int       `json:"limit"`
	Offset  int       `json:"offset"`
	HasMore bool      `json:"has_more"`
	Items   any       `json:"items"`
	Next    *nextPage `json:"next"`
}

func quoteArg(s string) string {
	if goruntime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func (r *runtime) search(list bool) *cobra.Command {
	var n noteFlags
	use := "search <query>"
	short := "Search local notes, quotes, books and chapters (offline)"
	args := cobra.MinimumNArgs(1)
	if list {
		use = "list"
		short = "List local personal notes (offline)"
		args = cobra.NoArgs
	}
	c := &cobra.Command{Use: use, Short: short, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		f, e := n.filter(r, query)
		if e != nil {
			return e
		}
		s, e := r.store()
		if e != nil {
			return e
		}
		defer s.Close()
		items, total, e := s.Search(cmd.Context(), f)
		if e != nil {
			return e
		}
		items = stripRaw(items, n.raw)
		v := page{Query: query, Total: total, Count: len(items), Limit: n.limit, Offset: n.offset, HasMore: n.offset+len(items) < total, Items: items}
		if v.HasMore {
			v.Next = r.nextNotes(cmd.Name(), query, n, n.offset+n.limit)
		}
		if r.wantsJSON() {
			return r.write(v)
		}
		for i, item := range items {
			fmt.Fprintf(r.out, "%d. [%s] %s · %s\n%s  %s\n", n.offset+i+1, item.Kind, item.BookTitle, item.Chapter, item.ID, item.CreatedAt)
			if item.Quote != "" {
				fmt.Fprintln(r.out, "> "+strings.ReplaceAll(item.Quote, "\n", "\n> "))
			}
			fmt.Fprintln(r.out, item.Content)
			fmt.Fprintln(r.out)
		}
		fmt.Fprintf(r.out, "%d of %d results\n", len(items), total)
		if v.Next != nil {
			fmt.Fprintln(r.out, "Next:", v.Next.Command)
		}
		return nil
	}}
	n.bind(c, true)
	return c
}

func (r *runtime) nextNotes(name, query string, n noteFlags, offset int) *nextPage {
	parts := []string{"weread-cli", name}
	if query != "" {
		parts = append(parts, quoteArg(query))
	}
	cfg, _ := r.cfg()
	for _, v := range []struct{ k, v string }{{"home", cfg.Home}, {"db", cfg.DB}, {"timezone", r.timezone}, {"book", n.book}, {"kind", n.kind}, {"from", n.from}, {"to", n.to}, {"sort", n.sort}} {
		if v.v != "" {
			parts = append(parts, "--"+v.k, quoteArg(v.v))
		}
	}
	if n.any {
		parts = append(parts, "--any")
	}
	if n.raw {
		parts = append(parts, "--include-raw")
	}
	if r.wantsJSON() {
		parts = append(parts, "--json")
	}
	parts = append(parts, "--limit", strconv.Itoa(n.limit), "--offset", strconv.Itoa(offset))
	return &nextPage{Offset: offset, Command: strings.Join(parts, " ")}
}

func (r *runtime) get() *cobra.Command {
	var raw bool
	c := &cobra.Command{Use: "get <note-id>", Short: "Get a local note by stable ID (offline)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, e := r.store()
		if e != nil {
			return e
		}
		defer s.Close()
		n, e := s.Note(cmd.Context(), args[0])
		if e != nil {
			return e
		}
		if !raw {
			n.Raw = nil
		}
		if r.wantsJSON() {
			return r.write(n)
		}
		fmt.Fprintf(r.out, "[%s] %s · %s\n%s\n", n.Kind, n.BookTitle, n.Chapter, n.CreatedAt)
		if n.Quote != "" {
			fmt.Fprintln(r.out, "> "+n.Quote)
		}
		_, e = fmt.Fprintln(r.out, n.Content)
		return e
	}}
	c.Flags().BoolVar(&raw, "include-raw", false, "include original gateway metadata")
	return c
}

func (r *runtime) books() *cobra.Command {
	var shelf, notebooks, raw bool
	var kind string
	var limit, offset int
	c := &cobra.Command{Use: "books [query]", Short: "Search/list local books, albums and notebook counts (offline)", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if limit < 1 || limit > 1000 || offset < 0 {
			return errors.New("limit must be 1..1000; offset cannot be negative")
		}
		if kind != "" && kind != "book" && kind != "album" && kind != "mp" {
			return errors.New("kind must be book, album or mp")
		}
		s, e := r.store()
		if e != nil {
			return e
		}
		defer s.Close()
		books, e := s.Books(cmd.Context())
		if e != nil {
			return e
		}
		q := ""
		if len(args) > 0 {
			q = args[0]
		}
		items := []domain.Book{}
		for _, b := range books {
			if shelf && !b.InShelf || notebooks && !b.HasNotebook || kind != "" && b.Kind != kind {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(b.Title+"\n"+b.Author+"\n"+b.Intro), strings.ToLower(q)) {
				continue
			}
			if !raw {
				b.Raw = nil
			}
			items = append(items, b)
		}
		total := len(items)
		start := offset
		if start > total {
			start = total
		}
		end := start + limit
		if end > total {
			end = total
		}
		items = items[start:end]
		v := page{Query: q, Total: total, Count: len(items), Limit: limit, Offset: offset, HasMore: end < total, Items: items}
		if v.HasMore {
			parts := []string{"weread-cli", "books"}
			if q != "" {
				parts = append(parts, quoteArg(q))
			}
			cfg, _ := r.cfg()
			parts = append(parts, "--home", quoteArg(cfg.Home), "--db", quoteArg(cfg.DB))
			if shelf {
				parts = append(parts, "--shelf")
			}
			if notebooks {
				parts = append(parts, "--notebooks")
			}
			if raw {
				parts = append(parts, "--include-raw")
			}
			if kind != "" {
				parts = append(parts, "--kind", kind)
			}
			if r.wantsJSON() {
				parts = append(parts, "--json")
			}
			parts = append(parts, "--limit", strconv.Itoa(limit), "--offset", strconv.Itoa(end))
			v.Next = &nextPage{Offset: end, Command: strings.Join(parts, " ")}
		}
		if r.wantsJSON() {
			return r.write(v)
		}
		for i, b := range items {
			fmt.Fprintf(r.out, "%d. %s · %s [%s] %s\n   %d highlights, %d personal reviews, %d bookmarks; progress %d%%\n", start+i+1, b.Title, b.Author, b.Kind, b.ID, b.Highlights, b.Reviews, b.Bookmarks, b.Progress)
		}
		fmt.Fprintf(r.out, "%d of %d books\n", len(items), total)
		if v.Next != nil {
			fmt.Fprintln(r.out, "Next:", v.Next.Command)
		}
		return nil
	}}
	f := c.Flags()
	f.BoolVar(&shelf, "shelf", false, "only current shelf items")
	f.BoolVar(&notebooks, "notebooks", false, "only books with personal notebooks")
	f.BoolVar(&raw, "include-raw", false, "include original gateway metadata")
	f.StringVar(&kind, "kind", "", "book, album or mp")
	f.IntVar(&limit, "limit", 50, "page size (1..1000)")
	f.IntVar(&offset, "offset", 0, "page offset")
	return c
}

func (r *runtime) stats() *cobra.Command {
	return &cobra.Command{Use: "stats", Short: "Summarize local books, note counts and sync status (offline)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, e := r.store()
		if e != nil {
			return e
		}
		defer s.Close()
		books, e := s.Books(cmd.Context())
		if e != nil {
			return e
		}
		_, highlights, e := s.Search(cmd.Context(), storage.Filter{Limit: 1, Kind: "highlight"})
		if e != nil {
			return e
		}
		_, reviews, e := s.Search(cmd.Context(), storage.Filter{Limit: 1, Kind: "review"})
		if e != nil {
			return e
		}
		last, e := s.LastSync(cmd.Context())
		if e != nil {
			return e
		}
		shelf, notebooks, bookmarks := 0, 0, 0
		for _, b := range books {
			if b.InShelf {
				shelf++
			}
			if b.HasNotebook {
				notebooks++
				bookmarks += b.Bookmarks
			}
		}
		return r.present(map[string]any{"books": len(books), "shelf_items": shelf, "notebooks": notebooks, "highlights": highlights, "reviews": reviews, "bookmarks": bookmarks, "exportable_notes": highlights + reviews, "database": s.Path, "last_sync": last})
	}}
}

func (r *runtime) export() *cobra.Command {
	var n noteFlags
	var output, entity string
	c := &cobra.Command{Use: "export [query]", Short: "Stream the complete local corpus for analysis or backup (offline)", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if output != "jsonl" && output != "json" && output != "csv" && output != "markdown" {
			return errors.New("output must be jsonl, json, csv or markdown")
		}
		if entity != "notes" && entity != "books" && entity != "reading" && entity != "snapshots" {
			return errors.New("entity must be notes, books, reading or snapshots")
		}
		if (output == "csv" || output == "markdown") && entity != "notes" {
			return errors.New("CSV/Markdown output currently supports --entity notes; use jsonl or json for other entities")
		}
		q := ""
		if len(args) > 0 {
			q = args[0]
		}
		f, e := n.filter(r, q)
		if e != nil {
			return e
		}
		if entity != "notes" && (q != "" || n.book != "" || n.kind != "" || n.from != "" || n.to != "") {
			return errors.New("query and note filters require --entity notes")
		}
		s, e := r.store()
		if e != nil {
			return e
		}
		defer s.Close()
		csvOut := csv.NewWriter(r.out)
		if output == "csv" {
			if e = csvOut.Write([]string{"id", "kind", "book_id", "book_title", "author", "chapter", "content", "quote", "created_at", "reader_link", "deep_link"}); e != nil {
				return e
			}
		}
		if output == "json" {
			if _, e = fmt.Fprintln(r.out, "["); e != nil {
				return e
			}
		}
		first := true
		e = s.Export(cmd.Context(), entity, f, func(raw json.RawMessage) error {
			if entity == "notes" {
				var note domain.Note
				if e := json.Unmarshal(raw, &note); e != nil {
					return e
				}
				if !n.raw {
					note.Raw = nil
				}
				switch output {
				case "csv":
					return csvOut.Write([]string{note.ID, note.Kind, note.BookID, note.BookTitle, note.Author, note.Chapter, note.Content, note.Quote, note.CreatedAt, note.ReaderLink, note.DeepLink})
				case "markdown":
					_, e := fmt.Fprintf(r.out, "## %s\n\n- 作者：%s\n- 章节：%s\n- 类型：%s\n- 日期：%s\n- ID：%s\n\n", note.BookTitle, note.Author, note.Chapter, note.Kind, note.CreatedAt, note.ID)
					if e != nil {
						return e
					}
					if note.Quote != "" {
						if _, e = fmt.Fprintln(r.out, "> "+strings.ReplaceAll(note.Quote, "\n", "\n> ")+"\n"); e != nil {
							return e
						}
					}
					_, e = fmt.Fprintln(r.out, note.Content+"\n")
					return e
				}
				raw, _ = json.Marshal(note)
			} else if entity == "books" && !n.raw {
				var b domain.Book
				if e := json.Unmarshal(raw, &b); e != nil {
					return e
				}
				b.Raw = nil
				raw, _ = json.Marshal(b)
			}
			if output == "json" && !first {
				if _, e := fmt.Fprintln(r.out, ","); e != nil {
					return e
				}
			}
			first = false
			_, e := r.out.Write(raw)
			if e != nil {
				return e
			}
			if output == "jsonl" {
				_, e = fmt.Fprintln(r.out)
			}
			return e
		})
		if e != nil {
			return e
		}
		if output == "json" {
			_, e = fmt.Fprintln(r.out, "\n]")
		}
		if output == "csv" {
			csvOut.Flush()
			return csvOut.Error()
		}
		return e
	}}
	n.bind(c, false)
	c.Flags().StringVar(&output, "output", "jsonl", "jsonl, json, csv or markdown (stdout)")
	c.Flags().StringVar(&entity, "entity", "notes", "notes, books, reading or snapshots")
	return c
}
