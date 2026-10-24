// Package domain defines the stable, local models, independently of gateway DTOs.
package domain

import "encoding/json"

type Book struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	Title         string          `json:"title"`
	Author        string          `json:"author"`
	Intro         string          `json:"intro,omitempty"`
	Cover         string          `json:"cover,omitempty"`
	ReaderLink    string          `json:"reader_link,omitempty"`
	DeepLink      string          `json:"deep_link,omitempty"`
	InShelf       bool            `json:"in_shelf"`
	HasNotebook   bool            `json:"has_notebook"`
	Highlights    int             `json:"highlight_count"`
	Reviews       int             `json:"review_count"`
	Bookmarks     int             `json:"bookmark_count"`
	NoteCount     int             `json:"note_count"`
	Progress      int             `json:"progress_percent"`
	Finished      bool            `json:"finished"`
	Secret        bool            `json:"secret"`
	ReadAt        string          `json:"last_read_at,omitempty"`
	NotebookSort  int64           `json:"-"`
	Signature     string          `json:"sync_signature,omitempty"`
	NotesSyncedAt string          `json:"notes_synced_at,omitempty"`
	SyncedAt      string          `json:"synced_at"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

type Note struct {
	ID           string          `json:"id"`
	SourceID     string          `json:"source_id"`
	Kind         string          `json:"kind"`
	BookID       string          `json:"book_id"`
	BookTitle    string          `json:"book_title"`
	Author       string          `json:"author"`
	Content      string          `json:"content"`
	Quote        string          `json:"quote,omitempty"`
	ChapterUID   int64           `json:"chapter_uid"`
	ChapterIndex int             `json:"chapter_index"`
	Chapter      string          `json:"chapter,omitempty"`
	Range        string          `json:"range,omitempty"`
	Color        int             `json:"color,omitempty"`
	CreatedAt    string          `json:"created_at,omitempty"`
	CreatedUnix  int64           `json:"-"`
	ReaderLink   string          `json:"reader_link,omitempty"`
	DeepLink     string          `json:"deep_link,omitempty"`
	SyncedAt     string          `json:"synced_at"`
	Raw          json.RawMessage `json:"raw,omitempty"`
}

type Snapshot struct {
	Key      string          `json:"key"`
	API      string          `json:"api"`
	Params   json.RawMessage `json:"params"`
	Data     json.RawMessage `json:"data"`
	SyncedAt string          `json:"synced_at"`
}

type SyncResult struct {
	Books                   int    `json:"books"`
	ShelfItems              int    `json:"shelf_items"`
	Notebooks               int    `json:"notebooks"`
	BooksFetched            int    `json:"books_fetched"`
	BooksSkipped            int    `json:"books_skipped"`
	Highlights              int    `json:"highlights"`
	Reviews                 int    `json:"reviews"`
	Bookmarks               int    `json:"bookmarks"`
	ExportableNotes         int    `json:"exportable_notes"`
	RemoteNoteCount         int    `json:"remote_note_count"`
	RemoteNoBookReviewCount int    `json:"remote_no_book_review_count"`
	ReadingPeriods          int    `json:"reading_periods"`
	SyncedAt                string `json:"synced_at"`
}

// Batch is committed atomically only after all requested API calls succeeded.
type Batch struct {
	Books     []Book
	Notes     map[string][]Note // complete replacement for each fetched notebook
	Snapshots []Snapshot
	Result    SyncResult
}
