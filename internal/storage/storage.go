// Package storage owns the SQLite mirror and its offline search index.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/songquanpeng/weread-cli/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	Path string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Precreate privately, before SQLite creates the WAL and shared-memory files.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, Path: path}
	if err = s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) init() error {
	for _, q := range []string{`PRAGMA busy_timeout=5000`, `PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`} {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return errors.New("database was created by a newer weread-cli; upgrade before opening it")
	}
	if version == 1 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE books(id TEXT PRIMARY KEY,kind TEXT NOT NULL,title TEXT NOT NULL,author TEXT NOT NULL,in_shelf INTEGER NOT NULL,has_notebook INTEGER NOT NULL,data TEXT NOT NULL)`,
		`CREATE TABLE notes(id TEXT PRIMARY KEY,book_id TEXT NOT NULL REFERENCES books(id),kind TEXT NOT NULL,created_unix INTEGER NOT NULL,search_text TEXT NOT NULL,data TEXT NOT NULL)`,
		`CREATE INDEX notes_book ON notes(book_id)`,
		`CREATE INDEX notes_created ON notes(created_unix DESC,id DESC)`,
		`CREATE VIRTUAL TABLE notes_fts USING fts5(search_text,content='notes',content_rowid='rowid',tokenize='trigram')`,
		`CREATE TRIGGER notes_ai AFTER INSERT ON notes BEGIN INSERT INTO notes_fts(rowid,search_text) VALUES(new.rowid,new.search_text); END`,
		`CREATE TRIGGER notes_ad AFTER DELETE ON notes BEGIN INSERT INTO notes_fts(notes_fts,rowid,search_text) VALUES('delete',old.rowid,old.search_text); END`,
		`CREATE TRIGGER notes_au AFTER UPDATE ON notes BEGIN INSERT INTO notes_fts(notes_fts,rowid,search_text) VALUES('delete',old.rowid,old.search_text); INSERT INTO notes_fts(rowid,search_text) VALUES(new.rowid,new.search_text); END`,
		`CREATE TABLE snapshots(key TEXT PRIMARY KEY,api TEXT NOT NULL,params TEXT NOT NULL,data TEXT NOT NULL,synced_at TEXT NOT NULL)`,
		`CREATE TABLE state(key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
		`PRAGMA user_version=1`,
	} {
		if _, err = tx.Exec(q); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) Books(ctx context.Context) ([]domain.Book, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM books ORDER BY title,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	books := []domain.Book{}
	for rows.Next() {
		var b []byte
		var book domain.Book
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &book); err != nil {
			return nil, err
		}
		books = append(books, book)
	}
	return books, rows.Err()
}

func (s *Store) Book(ctx context.Context, id string) (domain.Book, error) {
	var b []byte
	var book domain.Book
	err := s.db.QueryRowContext(ctx, `SELECT data FROM books WHERE id=?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return book, fmt.Errorf("book %q not in local mirror; run weread-cli sync", id)
	}
	if err == nil {
		err = json.Unmarshal(b, &book)
	}
	return book, err
}

func (s *Store) Commit(ctx context.Context, b domain.Batch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Remove absent shelf/notebook entries, but retain books as local history.
	if _, err = tx.ExecContext(ctx, `UPDATE books SET in_shelf=0,has_notebook=0,data=json_set(data,'$.in_shelf',json('false'),'$.has_notebook',json('false'))`); err != nil {
		return err
	}
	for _, book := range b.Books {
		data, e := json.Marshal(book)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO books VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,title=excluded.title,author=excluded.author,in_shelf=excluded.in_shelf,has_notebook=excluded.has_notebook,data=excluded.data`, book.ID, book.Kind, book.Title, book.Author, book.InShelf, book.HasNotebook, string(data)); err != nil {
			return err
		}
	}
	// A complete notebook listing is mandatory before Commit is called.
	if _, err = tx.ExecContext(ctx, `DELETE FROM notes WHERE book_id IN (SELECT id FROM books WHERE has_notebook=0)`); err != nil {
		return err
	}
	for bookID, notes := range b.Notes {
		if _, err = tx.ExecContext(ctx, `DELETE FROM notes WHERE book_id=?`, bookID); err != nil {
			return err
		}
		for _, n := range notes {
			if n.ID == "" || n.BookID != bookID {
				return errors.New("invalid note identity")
			}
			data, e := json.Marshal(n)
			if e != nil {
				return e
			}
			text := strings.Join([]string{n.Content, n.Quote, n.BookTitle, n.Author, n.Chapter}, "\n")
			if _, err = tx.ExecContext(ctx, `INSERT INTO notes(id,book_id,kind,created_unix,search_text,data) VALUES(?,?,?,?,?,?)`, n.ID, n.BookID, n.Kind, n.CreatedUnix, text, string(data)); err != nil {
				return err
			}
		}
	}
	for _, snap := range b.Snapshots {
		if !json.Valid(snap.Data) || !json.Valid(snap.Params) {
			return errors.New("invalid snapshot JSON")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO snapshots VALUES(?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET api=excluded.api,params=excluded.params,data=excluded.data,synced_at=excluded.synced_at`, snap.Key, snap.API, string(snap.Params), string(snap.Data), snap.SyncedAt); err != nil {
			return err
		}
	}
	data, err := json.Marshal(b.Result)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO state VALUES('last_sync',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(data)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LastSync(ctx context.Context) (*domain.SyncResult, error) {
	var data []byte
	var result domain.SyncResult
	err := s.db.QueryRowContext(ctx, `SELECT value FROM state WHERE key='last_sync'`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type Filter struct {
	Query, BookID, Kind, From, To, Sort string
	Any                                 bool
	Limit, Offset                       int
}

func (f Filter) Validate() error {
	if f.Limit < 1 || f.Limit > 1000 {
		return errors.New("limit must be between 1 and 1000")
	}
	if f.Offset < 0 {
		return errors.New("offset cannot be negative")
	}
	if f.Kind != "" && f.Kind != "highlight" && f.Kind != "review" {
		return errors.New("kind must be highlight or review")
	}
	if f.Sort != "" && f.Sort != "newest" && f.Sort != "oldest" {
		return errors.New("sort must be newest or oldest")
	}
	var from, to time.Time
	var err error
	if f.From != "" {
		from, err = time.Parse(time.RFC3339, f.From)
		if err != nil {
			return err
		}
	}
	if f.To != "" {
		to, err = time.Parse(time.RFC3339, f.To)
		if err != nil {
			return err
		}
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return errors.New("--from must be earlier than or equal to --to")
	}
	return nil
}

func conditions(f Filter) (string, []any) {
	conds := []string{"1=1"}
	args := []any{}
	if f.BookID != "" {
		conds = append(conds, "n.book_id=?")
		args = append(args, f.BookID)
	}
	if f.Kind != "" {
		conds = append(conds, "n.kind=?")
		args = append(args, f.Kind)
	}
	if f.From != "" {
		t, _ := time.Parse(time.RFC3339, f.From)
		conds = append(conds, "n.created_unix>=?")
		args = append(args, t.Unix())
	}
	if f.To != "" {
		t, _ := time.Parse(time.RFC3339, f.To)
		conds = append(conds, "n.created_unix<?")
		args = append(args, t.Unix())
	}
	terms := strings.Fields(f.Query)
	if len(terms) > 0 {
		allLong := true
		for _, t := range terms {
			if utf8.RuneCountInString(t) < 3 {
				allLong = false
			}
		}
		if allLong {
			quoted := []string{}
			for _, t := range terms {
				quoted = append(quoted, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
			}
			join := " AND "
			if f.Any {
				join = " OR "
			}
			conds = append(conds, `n.rowid IN (SELECT rowid FROM notes_fts WHERE notes_fts MATCH ?)`)
			args = append(args, strings.Join(quoted, join))
		} else {
			likes := []string{}
			for _, t := range terms {
				likes = append(likes, `n.search_text LIKE ? ESCAPE '\'`)
				args = append(args, "%"+escapeLike(t)+"%")
			}
			join := " AND "
			if f.Any {
				join = " OR "
			}
			conds = append(conds, "("+strings.Join(likes, join)+")")
		}
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}

func (s *Store) Search(ctx context.Context, f Filter) ([]domain.Note, int, error) {
	if err := f.Validate(); err != nil {
		return nil, 0, err
	}
	where, args := conditions(f)
	// Count and page belong to the same SQLite read snapshot during background sync.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM notes n`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "DESC"
	if f.Sort == "oldest" {
		order = "ASC"
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := tx.QueryContext(ctx, `SELECT n.data FROM notes n`+where+` ORDER BY n.created_unix `+order+`,n.id `+order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	notes := []domain.Note{}
	for rows.Next() {
		var data []byte
		var n domain.Note
		if err = rows.Scan(&data); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if err = json.Unmarshal(data, &n); err != nil {
			rows.Close()
			return nil, 0, err
		}
		notes = append(notes, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	return notes, total, tx.Commit()
}

func (s *Store) Note(ctx context.Context, id string) (domain.Note, error) {
	var data []byte
	var n domain.Note
	err := s.db.QueryRowContext(ctx, `SELECT data FROM notes WHERE id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return n, fmt.Errorf("note %q not found locally; use a stable id from weread-cli list/search", id)
	}
	if err == nil {
		err = json.Unmarshal(data, &n)
	}
	return n, err
}

func (s *Store) Snapshot(ctx context.Context, key string) (domain.Snapshot, error) {
	var v domain.Snapshot
	var params, data []byte
	err := s.db.QueryRowContext(ctx, `SELECT key,api,params,data,synced_at FROM snapshots WHERE key=?`, key).Scan(&v.Key, &v.API, &params, &data, &v.SyncedAt)
	v.Params, v.Data = params, data
	if errors.Is(err, sql.ErrNoRows) {
		return v, fmt.Errorf("snapshot %q not cached; run weread-cli sync or use --refresh", key)
	}
	return v, err
}

func (s *Store) Cache(ctx context.Context, v domain.Snapshot) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO snapshots VALUES(?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET data=excluded.data,synced_at=excluded.synced_at`, v.Key, v.API, string(v.Params), string(v.Data), v.SyncedAt)
	return err
}

// Export streams a single consistent SQLite snapshot, without pagination drift.
func (s *Store) Export(ctx context.Context, entity string, f Filter, emit func(json.RawMessage) error) error {
	var query string
	var args []any
	switch entity {
	case "notes":
		where, a := conditions(f)
		args = a
		query = `SELECT n.data FROM notes n` + where + ` ORDER BY n.created_unix,n.id`
	case "books":
		query = `SELECT data FROM books ORDER BY id`
	case "reading":
		query = `SELECT json_object('key',key,'api',api,'params',json(params),'data',json(data),'synced_at',synced_at) FROM snapshots WHERE api='/readdata/detail' ORDER BY key`
	case "snapshots":
		query = `SELECT json_object('key',key,'api',api,'params',json(params),'data',json(data),'synced_at',synced_at) FROM snapshots ORDER BY key`
	default:
		return errors.New("entity must be notes, books, reading, or snapshots")
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if err = emit(raw); err != nil {
			return err
		}
	}
	return rows.Err()
}

func SnapshotKey(api string, params map[string]any) string {
	if len(params) == 0 {
		return api
	}
	b, _ := json.Marshal(params)
	return api + ":" + string(b)
}
