package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/songquanpeng/weread-cli/internal/storage"
)

type fakeGateway struct {
	calls                  []string
	params                 []map[string]any
	broken, empty, removed bool
}

func (f *fakeGateway) Call(ctx context.Context, api string, p map[string]any) (json.RawMessage, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	f.calls = append(f.calls, api)
	f.params = append(f.params, p)
	var body string
	switch api {
	case "/shelf/sync":
		body = `{"books":[{"bookId":"b1","title":"测试书","author":"测试作者","readUpdateTime":100}],"albums":[{"albumInfo":{"albumId":"a1","name":"测试专辑"}}],"mp":{"book":{"bookId":"mp1","title":"文章收藏"}}}`
	case "/user/notebooks":
		if f.empty {
			return json.RawMessage(`{"hasMore":0}`), nil
		}
		if f.removed {
			return json.RawMessage(`{"books":[],"totalBookCount":0,"hasMore":0}`), nil
		}
		if p["lastSort"] == nil {
			body = `{"books":[{"bookId":"b1","book":{"bookId":"b1","title":"测试书"},"sort":100,"noteCount":1,"reviewCount":2,"bookmarkCount":1}],"totalBookCount":2,"totalNoteCount":5,"hasMore":1}`
		} else {
			if p["lastSort"] != int64(100) {
				return nil, errors.New("lastSort must be flattened")
			}
			body = `{"books":[{"bookId":"b2","book":{"bookId":"b2","title":"第二本书"},"sort":50,"noteCount":1}],"totalBookCount":2,"hasMore":0}`
		}
	case "/book/bookmarklist":
		body = `{"updated":[{"bookmarkId":"h1","markText":"人工智能与读书","type":1,"chapterUid":10,"createTime":100},{"bookmarkId":"bookmark-only","type":0}],"chapters":[{"chapterUid":10,"title":"第一章"}]}`
	case "/review/list/mine":
		if p["bookid"] == "b2" {
			body = `{"reviews":[],"hasMore":0,"totalCount":0}`
			break
		}
		if string(p["synckey"].(json.Number)) == "0" {
			body = `{"reviews":[{"review":{"reviewId":"r1","content":"想法一","abstract":"引用原文","createTime":101}}],"hasMore":1,"synckey":123,"totalCount":2}`
		} else {
			if p["bookid"] == nil || p["bookId"] != nil {
				return nil, errors.New("personal review uses lowercase bookid")
			}
			if f.broken {
				body = `{"reviews":[{"review":{"reviewId":"r1"}}],"hasMore":1,"synckey":123}`
			} else {
				body = `{"reviews":[{"reviewId":"r2","review":{"content":"想法二","createTime":102}}],"hasMore":0,"synckey":124}`
			}
		}
	case "/book/getprogress":
		body = `{"book":{"progress":1,"recordReadingTime":7200}}`
	case "/readdata/detail":
		body = `{"totalReadTime":7200,"readDays":2,"readTimes":{"1735660800":7200}}`
	default:
		return nil, errors.New("unexpected API")
	}
	return json.RawMessage(body), nil
}

func fixture(t *testing.T) (*Syncer, *fakeGateway) {
	t.Helper()
	s, e := storage.Open(filepath.Join(t.TempDir(), "mirror.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	f := &fakeGateway{}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return &Syncer{Gateway: f, Store: s, Location: loc, Now: func() time.Time { return time.Date(2026, 10, 25, 0, 0, 0, 0, loc) }}, f
}

func TestCompleteSyncMappingAndIncremental(t *testing.T) {
	s, f := fixture(t)
	ctx := context.Background()
	r, e := s.Sync(ctx, Options{ProgressLimit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if r.ShelfItems != 3 || r.Notebooks != 2 || r.BooksFetched != 2 || r.ExportableNotes != 4 || r.Bookmarks != 1 {
		t.Fatalf("incorrect totals: %+v", r)
	}
	books, e := s.Store.Books(ctx)
	if e != nil || len(books) != 4 {
		t.Fatalf("books %d: %v", len(books), e)
	}
	notes, total, e := s.Store.Search(ctx, storage.Filter{Limit: 10})
	if e != nil || total != 4 {
		t.Fatalf("notes %d: %v", total, e)
	}
	for _, n := range notes {
		if n.Kind == "highlight" && n.Chapter != "第一章" {
			t.Fatal("chapter mapping missing")
		}
		if n.ID == "review:b1:r1" && n.Quote != "引用原文" {
			t.Fatal("quote lost")
		}
	}
	book, e := s.Store.Book(ctx, "b1")
	if e != nil || book.Progress != 1 || book.Finished {
		t.Fatal("progress=1 must mean 1%, not finished")
	}
	f.calls = nil
	f.params = nil
	r, e = s.Sync(ctx, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if r.BooksFetched != 0 || r.BooksSkipped != 2 || r.ExportableNotes != 4 {
		t.Fatalf("incremental: %+v", r)
	}
	for _, a := range f.calls {
		if a == "/book/bookmarklist" || a == "/review/list/mine" {
			t.Fatal("unchanged fresh notebook fetched")
		}
	}
	s.Now = func() time.Time { return time.Date(2026, 10, 26, 1, 0, 0, 0, s.Location) }
	r, e = s.Sync(ctx, Options{})
	if e != nil || r.BooksFetched != 2 {
		t.Fatalf("24h refresh: %+v %v", r, e)
	}
}

func TestFailedPaginationPreservesEntireMirror(t *testing.T) {
	s, f := fixture(t)
	ctx := context.Background()
	before, e := s.Sync(ctx, Options{})
	if e != nil {
		t.Fatal(e)
	}
	f.broken = true
	if _, e = s.Sync(ctx, Options{Full: true}); e == nil {
		t.Fatal("repeated review cursor accepted")
	}
	_, n, _ := s.Store.Search(ctx, storage.Filter{Limit: 1})
	after, _ := s.Store.LastSync(ctx)
	if n != 4 || after.SyncedAt != before.SyncedAt {
		t.Fatal("failed sync changed mirror or checkpoint")
	}
	f.broken = false
	f.empty = true
	if _, e = s.Sync(ctx, Options{Full: true}); e == nil {
		t.Fatal("missing catalog treated as deletion")
	}
	_, n, _ = s.Store.Search(ctx, storage.Filter{Limit: 1})
	if n != 4 {
		t.Fatal("malformed response erased notes")
	}
}

func TestRemoteDeletionAndSyncLock(t *testing.T) {
	s, f := fixture(t)
	ctx := context.Background()
	if _, e := s.Sync(ctx, Options{}); e != nil {
		t.Fatal(e)
	}
	lock := flock.New(s.Store.Path + ".sync.lock")
	ok, e := lock.TryLock()
	if !ok || e != nil {
		t.Fatal(e)
	}
	if _, e = s.Sync(ctx, Options{}); e == nil {
		t.Fatal("concurrent sync accepted")
	}
	if _, n, e := s.Store.Search(ctx, storage.Filter{Limit: 1}); e != nil || n != 4 {
		t.Fatal("sync lock blocked offline search")
	}
	lock.Unlock()
	f.removed = true
	if _, e = s.Sync(ctx, Options{}); e != nil {
		t.Fatal(e)
	}
	if _, n, e := s.Store.Search(ctx, storage.Filter{Limit: 1}); e != nil || n != 0 {
		t.Fatal("remote deletion not reconciled")
	}
}
