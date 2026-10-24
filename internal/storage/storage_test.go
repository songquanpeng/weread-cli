package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/songquanpeng/weread-cli/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "data.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seed(t *testing.T, s *Store) {
	t.Helper()
	b := domain.Batch{Books: []domain.Book{{ID: "b1", Kind: "book", Title: "思考的方法", Author: "作者", HasNotebook: true, InShelf: true}}, Notes: map[string][]domain.Note{"b1": {
		{ID: "h1", BookID: "b1", Kind: "highlight", Content: "人工智能推动创新，读书和写作", BookTitle: "思考的方法", CreatedUnix: 100, CreatedAt: "1970-01-01T08:01:40+08:00"},
		{ID: "r1", BookID: "b1", Kind: "review", Content: "机器学习与写作", Quote: "人工智能", BookTitle: "思考的方法", CreatedUnix: 200, CreatedAt: "1970-01-01T08:03:20+08:00"},
		{ID: "h2", BookID: "b1", Kind: "highlight", Content: "literal 100% _ under_score OR \"quotes\"", CreatedUnix: 300, CreatedAt: "1970-01-01T08:05:00+08:00"},
	}}, Result: domain.SyncResult{SyncedAt: "2026-01-01T00:00:00Z"}}
	if e := s.Commit(context.Background(), b); e != nil {
		t.Fatal(e)
	}
}

func TestChineseOfflineSearchAndPagination(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	for _, tc := range []struct {
		query string
		any   bool
		want  int
	}{
		{"人工智能", false, 2}, {"读书", false, 1}, {"人工智能 写作", false, 2}, {"人工智能 读书", false, 1}, {"机器学习 读书", true, 2}, {"思考的方法", false, 2}, {"100%", false, 1}, {"_", false, 1}, {`"quotes"`, false, 1}, {"无匹配", false, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			items, n, e := s.Search(context.Background(), Filter{Query: tc.query, Any: tc.any, Limit: 10})
			if e != nil || n != tc.want || len(items) != tc.want {
				t.Fatalf("got %d/%d, want %d, err %v", n, len(items), tc.want, e)
			}
		})
	}
	a, n, e := s.Search(context.Background(), Filter{Limit: 1})
	if e != nil || n != 3 || a[0].ID != "h2" {
		t.Fatalf("page 1 %v %v", a, e)
	}
	b, _, e := s.Search(context.Background(), Filter{Limit: 1, Offset: 1})
	if e != nil || b[0].ID != "r1" {
		t.Fatalf("page 2 %v %v", b, e)
	}
	_, n, e = s.Search(context.Background(), Filter{Limit: 1, Kind: "review", From: "1970-01-01T00:02:00Z", To: "1970-01-01T00:04:00Z"})
	if e != nil || n != 1 {
		t.Fatalf("date filter %d %v", n, e)
	}
}

func TestAtomicRollbackAndRemoval(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	bad := domain.Batch{Books: []domain.Book{{ID: "b1", Kind: "book", HasNotebook: true}}, Notes: map[string][]domain.Note{"b1": {{ID: "", BookID: "b1"}}}}
	if e := s.Commit(context.Background(), bad); e == nil {
		t.Fatal("invalid batch accepted")
	}
	_, n, _ := s.Search(context.Background(), Filter{Limit: 10})
	if n != 3 {
		t.Fatal("failed commit destroyed notes")
	}
	last, _ := s.LastSync(context.Background())
	if last.SyncedAt != "2026-01-01T00:00:00Z" {
		t.Fatal("failure advanced sync state")
	}
	if e := s.Commit(context.Background(), domain.Batch{Notes: map[string][]domain.Note{}}); e != nil {
		t.Fatal(e)
	}
	_, n, _ = s.Search(context.Background(), Filter{Limit: 10})
	if n != 0 {
		t.Fatal("removed notebooks left stale content")
	}
	book, e := s.Book(context.Background(), "b1")
	if e != nil || book.InShelf || book.HasNotebook {
		t.Fatal("history flags not reset")
	}
}

func TestReplacementUpdatesFTSAndExport(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	b := domain.Batch{Books: []domain.Book{{ID: "b1", Kind: "book", HasNotebook: true}}, Notes: map[string][]domain.Note{"b1": {{ID: "h1", BookID: "b1", Kind: "highlight", Content: "完全不同的内容", CreatedUnix: 100}}}}
	if e := s.Commit(context.Background(), b); e != nil {
		t.Fatal(e)
	}
	_, n, e := s.Search(context.Background(), Filter{Limit: 10, Query: "人工智能"})
	if e != nil || n != 0 {
		t.Fatal("stale FTS rows")
	}
	_, n, e = s.Search(context.Background(), Filter{Limit: 10, Query: "不同的内容"})
	if e != nil || n != 1 {
		t.Fatal("missing refreshed FTS row")
	}
	count := 0
	if e = s.Export(context.Background(), "notes", Filter{}, func(raw json.RawMessage) error { count++; return nil }); e != nil || count != 1 {
		t.Fatalf("export %d %v", count, e)
	}
	v := domain.Snapshot{Key: "example", API: "/readdata/detail", Params: json.RawMessage(`{}`), Data: json.RawMessage(`{"totalReadTime":100}`)}
	if e = s.Cache(context.Background(), v); e != nil {
		t.Fatal(e)
	}
	snap, e := s.Snapshot(context.Background(), "example")
	if e != nil || string(snap.Data) != string(v.Data) {
		t.Fatalf("snapshot %v %v", snap, e)
	}
}
