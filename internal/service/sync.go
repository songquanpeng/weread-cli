package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/gofrs/flock"
	"github.com/songquanpeng/weread-cli/internal/domain"
	"github.com/songquanpeng/weread-cli/internal/storage"
	"github.com/songquanpeng/weread-cli/internal/weread"
)

type Syncer struct {
	Gateway  weread.Gateway
	Store    *storage.Store
	Location *time.Location
	Now      func() time.Time
	Progress func(done, total int)
}

type Options struct {
	Full          bool
	RefreshAfter  time.Duration
	ProgressLimit int
	Years         []int
}

func (s *Syncer) Sync(ctx context.Context, opt Options) (domain.SyncResult, error) {
	lock := flock.New(s.Store.Path + ".sync.lock")
	ok, err := lock.TryLock()
	if err != nil {
		return domain.SyncResult{}, err
	}
	if !ok {
		return domain.SyncResult{}, errors.New("another sync is running for this database")
	}
	defer lock.Unlock()
	if s.Location == nil {
		s.Location = time.UTC
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	now := s.Now()
	at := now.UTC().Format(time.RFC3339)
	batch := domain.Batch{Notes: map[string][]domain.Note{}, Result: domain.SyncResult{SyncedAt: at}}
	call := func(api string, p map[string]any, target any) error {
		data, err := s.Gateway.Call(ctx, api, p)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, target); err != nil {
			return fmt.Errorf("%s: unexpected response shape: %w", api, err)
		}
		params, _ := json.Marshal(p)
		if p == nil {
			params = []byte("{}")
		}
		batch.Snapshots = append(batch.Snapshots, domain.Snapshot{Key: storage.SnapshotKey(api, p), API: api, Params: params, Data: data, SyncedAt: at})
		return nil
	}
	old, err := s.Store.Books(ctx)
	if err != nil {
		return batch.Result, err
	}
	previous := map[string]domain.Book{}
	for _, b := range old {
		previous[b.ID] = b
	}
	books := map[string]domain.Book{}
	var shelf weread.Shelf
	if err = call("/shelf/sync", nil, &shelf); err != nil {
		return batch.Result, err
	}
	// Missing mandatory arrays are a protocol error, never a signal to delete data.
	if shelf.Books == nil {
		return batch.Result, errors.New("shelf response is missing books; local mirror preserved")
	}
	for _, raw := range shelf.Books {
		var wire weread.BookDTO
		if err = json.Unmarshal(raw, &wire); err != nil {
			return batch.Result, err
		}
		if wire.BookID == "" {
			return batch.Result, errors.New("shelf contains a book without bookId")
		}
		b := wire.Domain()
		b.InShelf = true
		b.SyncedAt = at
		b.Raw = raw
		b.ReadAt = s.date(wire.ReadUpdateTime)
		books[b.ID] = b
	}
	batch.Result.ShelfItems = len(shelf.Books) + len(shelf.Albums)
	for _, a := range shelf.Albums {
		if a.Info.ID == "" {
			return batch.Result, errors.New("album missing albumId")
		}
		b := domain.Book{ID: "album:" + a.Info.ID, Kind: "album", Title: a.Info.Name, Author: a.Info.Author, Intro: a.Info.Intro, Cover: a.Info.Cover, InShelf: true, Secret: bool(a.Extra.Secret), ReadAt: s.date(a.Extra.ReadTime), SyncedAt: at}
		b.Raw, _ = json.Marshal(a)
		books[b.ID] = b
	}
	if len(shelf.MP) > 0 && string(shelf.MP) != "null" && string(shelf.MP) != "{}" {
		var mp struct {
			Book weread.BookDTO `json:"book"`
		}
		if err = json.Unmarshal(shelf.MP, &mp); err != nil {
			return batch.Result, err
		}
		b := mp.Book.Domain()
		b.ID = "mp:" + b.ID
		b.Kind = "mp"
		b.InShelf = true
		b.SyncedAt = at
		b.Raw = shelf.MP
		if b.Title == "" {
			b.Title = "文章收藏"
		}
		books[b.ID] = b
		batch.Result.ShelfItems++
	}
	// Enumerate the COMPLETE notebook catalog with the official lastSort cursor.
	notebooks := []string{}
	seen := map[string]bool{}
	cursors := map[int64]bool{}
	var cursor int64
	expectedBooks := -1
	for page := 0; ; page++ {
		if page >= 10000 {
			return batch.Result, errors.New("notebook pagination exceeds safety limit")
		}
		p := map[string]any{"count": 100}
		if page > 0 {
			p["lastSort"] = cursor
		}
		var response weread.Notebooks
		if err = call("/user/notebooks", p, &response); err != nil {
			return batch.Result, err
		}
		if response.Books == nil {
			return batch.Result, errors.New("notebook response is missing books; local mirror preserved")
		}
		if page == 0 {
			expectedBooks = response.TotalBooks
			batch.Result.RemoteNoteCount = response.TotalNotes
			batch.Result.RemoteNoBookReviewCount = response.NoBookReviewCount
		}
		var last int64
		for _, raw := range response.Books {
			var n weread.Notebook
			if err = json.Unmarshal(raw, &n); err != nil {
				return batch.Result, err
			}
			if n.BookID == "" {
				n.BookID = n.Book.BookID
			}
			if n.BookID == "" {
				return batch.Result, errors.New("notebook missing bookId")
			}
			last = n.Sort
			if seen[n.BookID] {
				continue
			}
			seen[n.BookID] = true
			b, exists := books[n.BookID]
			if !exists {
				b = n.Book.Domain()
				b.ID = n.BookID
				b.SyncedAt = at
			}
			b.HasNotebook = true
			b.Highlights = n.NoteCount
			b.Reviews = n.ReviewCount
			b.Bookmarks = n.BookmarkCount
			b.NoteCount = b.Highlights + b.Reviews + b.Bookmarks
			b.Progress = n.Progress
			b.Finished = n.MarkedStatus == 1
			b.NotebookSort = n.Sort
			b.Raw = raw
			sig, _ := json.Marshal([]any{n.Sort, n.NoteCount, n.ReviewCount, n.BookmarkCount, b.Title, b.Author, b.ReaderLink, b.DeepLink})
			hash := sha256.Sum256(sig)
			b.Signature = hex.EncodeToString(hash[:])
			b.NotesSyncedAt = previous[b.ID].NotesSyncedAt
			books[b.ID] = b
			notebooks = append(notebooks, b.ID)
			batch.Result.Highlights += b.Highlights
			batch.Result.Reviews += b.Reviews
			batch.Result.Bookmarks += b.Bookmarks
		}
		if !response.HasMore {
			break
		}
		if len(response.Books) == 0 || last == 0 || cursors[last] || last == cursor {
			return batch.Result, errors.New("notebook pagination cursor did not advance; local mirror preserved")
		}
		cursor = last
		cursors[last] = true
	}
	if expectedBooks > len(notebooks) {
		return batch.Result, fmt.Errorf("incomplete notebook catalog: got %d of %d; local mirror preserved", len(notebooks), expectedBooks)
	}
	batch.Result.Notebooks = len(notebooks)
	refresh := opt.RefreshAfter
	if refresh == 0 {
		refresh = 24 * time.Hour
	}
	for i, id := range notebooks {
		b := books[id]
		prev := previous[id]
		last, e := time.Parse(time.RFC3339, prev.NotesSyncedAt)
		if !opt.Full && e == nil && prev.Signature == b.Signature && now.Sub(last) < refresh && now.Sub(last) >= 0 {
			batch.Result.BooksSkipped++
		} else {
			notes, e := s.fetchNotes(ctx, b, at, call)
			if e != nil {
				return batch.Result, e
			}
			batch.Notes[id] = notes
			b.NotesSyncedAt = at
			books[id] = b
			batch.Result.BooksFetched++
		}
		if s.Progress != nil {
			s.Progress(i+1, len(notebooks))
		}
	}
	// Recent reading progress only: avoid one extra request per shelf item.
	recent := []domain.Book{}
	for _, b := range books {
		if b.Kind == "book" && b.InShelf {
			recent = append(recent, b)
		}
	}
	sort.Slice(recent, func(i, j int) bool {
		if recent[i].ReadAt == recent[j].ReadAt {
			return recent[i].ID < recent[j].ID
		}
		return recent[i].ReadAt > recent[j].ReadAt
	})
	for i := 0; i < opt.ProgressLimit && i < len(recent); i++ {
		var progress struct {
			Book struct {
				Progress   int   `json:"progress"`
				FinishTime int64 `json:"finishTime"`
			} `json:"book"`
		}
		if err = call("/book/getprogress", map[string]any{"bookId": recent[i].ID}, &progress); err != nil {
			return batch.Result, err
		}
		b := books[recent[i].ID]
		b.Progress = progress.Book.Progress
		b.Finished = progress.Book.Progress == 100 && progress.Book.FinishTime > 0
		books[b.ID] = b
	}
	var overall struct {
		ReadTimes map[string]json.RawMessage `json:"readTimes"`
	}
	if err = call("/readdata/detail", map[string]any{"mode": "overall", "baseTime": 0}, &overall); err != nil {
		return batch.Result, err
	}
	batch.Result.ReadingPeriods++
	for _, mode := range []string{"weekly", "monthly"} {
		var v map[string]any
		if err = call("/readdata/detail", map[string]any{"mode": mode, "baseTime": 0}, &v); err != nil {
			return batch.Result, err
		}
		batch.Result.ReadingPeriods++
	}
	years := map[int]bool{now.In(s.Location).Year(): true}
	for _, y := range opt.Years {
		years[y] = true
	}
	for k := range overall.ReadTimes {
		ts, e := strconv.ParseInt(k, 10, 64)
		if e != nil {
			return batch.Result, errors.New("invalid reading-year bucket")
		}
		y := time.Unix(ts, 0).In(s.Location).Year()
		if y >= 2000 && y <= now.In(s.Location).Year() {
			years[y] = true
		}
	}
	yearList := []int{}
	for y := range years {
		yearList = append(yearList, y)
	}
	sort.Ints(yearList)
	for _, y := range yearList {
		if y < 2000 || y > now.In(s.Location).Year() {
			return batch.Result, errors.New("reading year outside supported range")
		}
		p := map[string]any{"mode": "annually", "baseTime": time.Date(y, 1, 1, 0, 0, 0, 0, s.Location).Unix()}
		if !opt.Full && y < now.In(s.Location).Year() {
			if _, e := s.Store.Snapshot(ctx, storage.SnapshotKey("/readdata/detail", p)); e == nil {
				continue
			}
		}
		var v map[string]any
		if err = call("/readdata/detail", p, &v); err != nil {
			return batch.Result, err
		}
		batch.Result.ReadingPeriods++
	}
	for _, b := range books {
		batch.Books = append(batch.Books, b)
	}
	sort.Slice(batch.Books, func(i, j int) bool { return batch.Books[i].ID < batch.Books[j].ID })
	batch.Result.Books = len(books)
	// Count actual exportable rows, including skipped notebooks (not bookmarks).
	for _, id := range notebooks {
		if notes, ok := batch.Notes[id]; ok {
			batch.Result.ExportableNotes += len(notes)
		} else {
			_, n, e := s.Store.Search(ctx, storage.Filter{BookID: id, Limit: 1})
			if e != nil {
				return batch.Result, e
			}
			batch.Result.ExportableNotes += n
		}
	}
	if err = s.Store.Commit(ctx, batch); err != nil {
		return batch.Result, err
	}
	return batch.Result, nil
}

type caller func(string, map[string]any, any) error

func (s *Syncer) fetchNotes(ctx context.Context, b domain.Book, at string, call caller) ([]domain.Note, error) {
	notes := []domain.Note{}
	seen := map[string]bool{}
	var highlights weread.Highlights
	if err := call("/book/bookmarklist", map[string]any{"bookId": b.ID}, &highlights); err != nil {
		return nil, err
	}
	if highlights.Updated == nil {
		return nil, errors.New("highlight response missing updated; local mirror preserved")
	}
	chapters := map[int64]string{}
	for _, c := range highlights.Chapters {
		chapters[c.UID] = c.Title
	}
	for _, raw := range highlights.Updated {
		var h weread.Highlight
		if err := json.Unmarshal(raw, &h); err != nil {
			return nil, err
		}
		if h.Type != 1 {
			continue
		}
		if h.ID == "" {
			return nil, errors.New("highlight missing bookmarkId")
		}
		id := "highlight:" + b.ID + ":" + h.ID
		if seen[id] {
			continue
		}
		seen[id] = true
		n := s.baseNote(b, at)
		n.ID = id
		n.SourceID = h.ID
		n.Kind = "highlight"
		n.Content = h.Content
		n.Range = h.Range
		n.Color = h.Color
		n.ChapterUID = h.ChapterUID
		n.ChapterIndex = h.ChapterIndex
		n.Chapter = chapters[h.ChapterUID]
		n.CreatedUnix = h.Created
		n.CreatedAt = s.date(h.Created)
		n.Raw = raw
		notes = append(notes, n)
	}
	var cursor json.Number = "0"
	cursors := map[string]bool{}
	reviewCount := 0
	expected := 0
	for page := 0; ; page++ {
		if page >= 10000 {
			return nil, errors.New("review pagination exceeds safety limit")
		}
		p := map[string]any{"bookid": b.ID, "count": 100, "synckey": cursor}
		var response weread.Reviews
		if err := call("/review/list/mine", p, &response); err != nil {
			return nil, err
		}
		if response.Items == nil {
			return nil, errors.New("personal reviews response missing reviews; local mirror preserved")
		}
		if page == 0 {
			expected = response.Total
		}
		for _, item := range response.Items {
			var r weread.Review
			if err := json.Unmarshal(item.Review, &r); err != nil {
				return nil, err
			}
			if r.ID == "" {
				r.ID = item.ID
			}
			if r.ID == "" {
				return nil, errors.New("review missing reviewId")
			}
			id := "review:" + b.ID + ":" + r.ID
			if seen[id] {
				continue
			}
			seen[id] = true
			n := s.baseNote(b, at)
			n.ID = id
			n.SourceID = r.ID
			n.Kind = "review"
			n.Content = r.Content
			n.Quote = r.Quote
			n.Range = r.Range
			n.ChapterUID = r.ChapterUID
			n.ChapterIndex = r.ChapterIndex
			n.Chapter = r.ChapterName
			if n.Chapter == "" {
				n.Chapter = r.ChapterTitle
			}
			if n.Chapter == "" {
				n.Chapter = chapters[r.ChapterUID]
			}
			n.CreatedUnix = r.Created
			n.CreatedAt = s.date(r.Created)
			n.Raw = item.Review
			notes = append(notes, n)
			reviewCount++
		}
		if !response.HasMore {
			break
		}
		key := string(response.SyncKey)
		if len(response.Items) == 0 || key == "" || key == "0" || key == string(cursor) || cursors[key] {
			return nil, errors.New("review pagination cursor did not advance; local mirror preserved")
		}
		cursor = response.SyncKey
		cursors[key] = true
	}
	if reviewCount < expected {
		return nil, fmt.Errorf("incomplete personal review list for book %s: got %d of %d", b.ID, reviewCount, expected)
	}
	return notes, nil
}

func (s *Syncer) baseNote(b domain.Book, at string) domain.Note {
	return domain.Note{BookID: b.ID, BookTitle: b.Title, Author: b.Author, ReaderLink: b.ReaderLink, DeepLink: b.DeepLink, SyncedAt: at}
}

func (s *Syncer) date(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).In(s.Location).Format(time.RFC3339)
}
