package weread

import (
	"encoding/json"
	"fmt"

	"github.com/songquanpeng/weread-cli/internal/domain"
)

// Flag accommodates the gateway's integer and boolean flags, without guessing
// the semantics of business fields such as progress (which is 0..100).
type Flag bool

func (f *Flag) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "true", "1":
		*f = true
	case "false", "0", "null":
		*f = false
	default:
		return fmt.Errorf("invalid boolean flag")
	}
	return nil
}

type BookDTO struct {
	BookID         string `json:"bookId"`
	Title          string `json:"title"`
	Author         string `json:"author"`
	Intro          string `json:"intro"`
	Cover          string `json:"cover"`
	ReaderLink     string `json:"readerLink"`
	DeepLink       string `json:"deepLink"`
	Finish         Flag   `json:"finishReading"`
	Secret         Flag   `json:"secret"`
	ReadUpdateTime int64  `json:"readUpdateTime"`
}

func (b BookDTO) Domain() domain.Book {
	return domain.Book{ID: b.BookID, Kind: "book", Title: b.Title, Author: b.Author, Intro: b.Intro, Cover: b.Cover, ReaderLink: b.ReaderLink, DeepLink: b.DeepLink, Finished: bool(b.Finish), Secret: bool(b.Secret)}
}

type Shelf struct {
	Books  []json.RawMessage `json:"books"`
	Albums []struct {
		Info struct {
			ID     string `json:"albumId"`
			Name   string `json:"name"`
			Author string `json:"authorName"`
			Intro  string `json:"intro"`
			Cover  string `json:"cover"`
		} `json:"albumInfo"`
		Extra struct {
			Secret   Flag  `json:"secret"`
			ReadTime int64 `json:"lectureReadUpdateTime"`
		} `json:"albumInfoExtra"`
	} `json:"albums"`
	MP json.RawMessage `json:"mp"`
}

type Notebook struct {
	BookID        string  `json:"bookId"`
	Book          BookDTO `json:"book"`
	ReviewCount   int     `json:"reviewCount"`
	NoteCount     int     `json:"noteCount"`
	BookmarkCount int     `json:"bookmarkCount"`
	Progress      int     `json:"readingProgress"`
	MarkedStatus  int     `json:"markedStatus"`
	Sort          int64   `json:"sort"`
}

type Notebooks struct {
	Books             []json.RawMessage `json:"books"`
	TotalBooks        int               `json:"totalBookCount"`
	TotalNotes        int               `json:"totalNoteCount"`
	NoBookReviewCount int               `json:"noBookReviewCount"`
	HasMore           Flag              `json:"hasMore"`
}

type Highlight struct {
	ID           string `json:"bookmarkId"`
	BookID       string `json:"bookId"`
	Content      string `json:"markText"`
	ChapterUID   int64  `json:"chapterUid"`
	ChapterIndex int    `json:"chapterIdx"`
	Created      int64  `json:"createTime"`
	Type         int    `json:"type"`
	Range        string `json:"range"`
	Color        int    `json:"colorStyle"`
}

type Highlights struct {
	Updated  []json.RawMessage `json:"updated"`
	Chapters []struct {
		UID   int64  `json:"chapterUid"`
		Title string `json:"title"`
	} `json:"chapters"`
}

type Review struct {
	ID           string `json:"reviewId"`
	Content      string `json:"content"`
	Quote        string `json:"abstract"`
	Range        string `json:"range"`
	ChapterUID   int64  `json:"chapterUid"`
	ChapterIndex int    `json:"chapterIdx"`
	ChapterName  string `json:"chapterName"`
	ChapterTitle string `json:"chapterTitle"`
	Created      int64  `json:"createTime"`
}

type Reviews struct {
	Items []struct {
		ID     string          `json:"reviewId"`
		Review json.RawMessage `json:"review"`
	} `json:"reviews"`
	Total   int         `json:"totalCount"`
	HasMore Flag        `json:"hasMore"`
	SyncKey json.Number `json:"synckey"`
}
