package plg_widget_ai

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	. "github.com/mickael-kerjean/filestash/server/common"
)

// long term memory: facts the assistant learned about the user (preferences,
// naming conventions, where things belong, ...) and a journal of the actions
// it performed so it can pick up on the way things get organised over time.

var db *sql.DB

func init() {
	Hooks.Register.Onload(func() {
		var err error
		if db, err = sql.Open("sqlite3", GetAbsolutePath(DB_PATH, "ai.db")); err != nil {
			Log.Error("plg_widget_ai::db err=cannot_open msg=%s", err.Error())
			return
		}
		if err = initSchema(db); err != nil {
			Log.Error("plg_widget_ai::db err=cannot_init msg=%s", err.Error())
		}
	})
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS journal (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user TEXT NOT NULL,
			action TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_memories_user ON memories(user);
		CREATE INDEX IF NOT EXISTS idx_journal_user ON journal(user);
	`)
	return err
}

type Memory struct {
	ID      int64  `json:"id"`
	Content string `json:"content"`
}

func remember(user, content string) (int64, error) {
	if db == nil {
		return 0, ErrNotAvailable
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return 0, ErrNotValid
	}
	var id int64
	if err := db.QueryRow(`SELECT id FROM memories WHERE user = ? AND content = ?`, user, content).Scan(&id); err == nil {
		return id, nil
	}
	res, err := db.Exec(`INSERT INTO memories(user, content, created_at) VALUES(?, ?, ?)`, user, content, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func forget(user string, id int64) error {
	if db == nil {
		return ErrNotAvailable
	}
	_, err := db.Exec(`DELETE FROM memories WHERE user = ? AND id = ?`, user, id)
	return err
}

func memories(user string) []Memory {
	out := []Memory{}
	if db == nil {
		return out
	}
	rows, err := db.Query(`SELECT id, content FROM memories WHERE user = ? ORDER BY id DESC LIMIT 200`, user)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var m Memory
		if rows.Scan(&m.ID, &m.Content) == nil {
			out = append(out, m)
		}
	}
	return out
}

func journal(user, action string) {
	if db == nil {
		return
	}
	db.Exec(`INSERT INTO journal(user, action, created_at) VALUES(?, ?, ?)`, user, action, time.Now().Unix())
}

func recentJournal(user string, n int) []string {
	out := []string{}
	if db == nil {
		return out
	}
	rows, err := db.Query(`SELECT action, created_at FROM journal WHERE user = ? ORDER BY id DESC LIMIT ?`, user, n)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		var t int64
		if rows.Scan(&action, &t) == nil {
			out = append(out, fmt.Sprintf("%s %s", time.Unix(t, 0).Format("2006-01-02"), action))
		}
	}
	return out
}

var ErrNotAvailable = NewError("Memory is not available", 503)
