// Package testutil supplies synthetic local fixtures for regression tests.
package testutil

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func Database(t *testing.T) *sql.DB {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	Exec(t, db, `
		PRAGMA journal_mode=WAL;
		CREATE TABLE handle (ROWID INTEGER PRIMARY KEY, id TEXT, service TEXT);
		CREATE TABLE chat (ROWID INTEGER PRIMARY KEY, guid TEXT, chat_identifier TEXT, display_name TEXT, service_name TEXT);
		CREATE TABLE chat_handle_join (chat_id INTEGER, handle_id INTEGER);
		CREATE TABLE message (ROWID INTEGER PRIMARY KEY, text TEXT, attributedBody BLOB, date INTEGER, is_from_me INTEGER, is_read INTEGER, service TEXT, handle_id INTEGER);
		CREATE TABLE chat_message_join (chat_id INTEGER, message_id INTEGER);
		CREATE TABLE attachment (ROWID INTEGER PRIMARY KEY, filename TEXT, mime_type TEXT, uti TEXT, total_bytes INTEGER);
		CREATE TABLE message_attachment_join (message_id INTEGER, attachment_id INTEGER);
	`)
	return db
}

func Exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func Output(t *testing.T, f func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	original := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = original }()
	type captured struct {
		data []byte
		err  error
	}
	result := make(chan captured, 1)
	go func() {
		data, err := io.ReadAll(reader)
		reader.Close()
		result <- captured{data, err}
	}()
	f()
	writer.Close()
	out := <-result
	if out.err != nil {
		t.Fatal(out.err)
	}
	return string(out.data)
}

func Input(t *testing.T, text string, f func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	original := os.Stdin
	os.Stdin = file
	defer func() { os.Stdin = original }()
	f()
}

func AppleScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "scripts")
	script := `#!/bin/sh
printf '%s\n' "$2" >> "$TEST_OSASCRIPT_LOG"
if [ "$TEST_OSASCRIPT_FAIL" = "1" ]; then
    printf '%s\n' 'Synthetic automation error' >&2
    exit 1
fi
printf '%s\n' 'false'
`
	if err := os.WriteFile(filepath.Join(dir, "osascript"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_OSASCRIPT_LOG", logPath)
	t.Setenv("TEST_OSASCRIPT_FAIL", "0")
	return logPath
}
