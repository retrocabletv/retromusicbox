package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

func Open(dbPath string) (*sql.DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS catalogue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT UNIQUE NOT NULL,
			youtube_id TEXT NOT NULL,
			title TEXT NOT NULL,
			artist TEXT NOT NULL,
			duration_seconds INTEGER,
			thumbnail_path TEXT,
			video_path TEXT,
			last_played_at DATETIME,
			play_count INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			catalogue_code TEXT NOT NULL,
			caller_id TEXT,
			requested_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			status TEXT DEFAULT 'queued',
			played_at DATETIME,
			FOREIGN KEY (catalogue_code) REFERENCES catalogue(code)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_status ON requests(status)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_caller ON requests(caller_id, requested_at)`,
		`CREATE INDEX IF NOT EXISTS idx_catalogue_code ON catalogue(code)`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return fmt.Errorf("execute migration: %w", err)
		}
	}

	return nil
}
