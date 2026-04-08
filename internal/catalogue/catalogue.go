package catalogue

import (
	"database/sql"
	"fmt"
	"strings"
)

type Entry struct {
	ID              int64   `json:"id"`
	Code            string  `json:"code"`
	YoutubeID       string  `json:"youtube_id"`
	Title           string  `json:"title"`
	Artist          string  `json:"artist"`
	DurationSeconds *int    `json:"duration_seconds,omitempty"`
	ThumbnailPath   *string `json:"thumbnail_path,omitempty"`
	VideoPath       *string `json:"video_path,omitempty"`
	LastPlayedAt    *string `json:"last_played_at,omitempty"`
	PlayCount       int     `json:"play_count"`
	CreatedAt       string  `json:"created_at"`
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Add(youtubeID, title, artist string, durationSeconds int, thumbnailPath string) (*Entry, error) {
	code, err := s.nextCode()
	if err != nil {
		return nil, err
	}

	var thumbPtr *string
	if thumbnailPath != "" {
		thumbPtr = &thumbnailPath
	}

	result, err := s.db.Exec(
		`INSERT INTO catalogue (code, youtube_id, title, artist, duration_seconds, thumbnail_path)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		code, youtubeID, title, artist, durationSeconds, thumbPtr,
	)
	if err != nil {
		return nil, fmt.Errorf("insert catalogue entry: %w", err)
	}

	id, _ := result.LastInsertId()
	return &Entry{
		ID:              id,
		Code:            code,
		YoutubeID:       youtubeID,
		Title:           title,
		Artist:          artist,
		DurationSeconds: &durationSeconds,
		ThumbnailPath:   thumbPtr,
		PlayCount:       0,
	}, nil
}

func (s *Service) nextCode() (string, error) {
	var maxCode sql.NullString
	err := s.db.QueryRow(`SELECT MAX(code) FROM catalogue`).Scan(&maxCode)
	if err != nil {
		return "", err
	}

	if !maxCode.Valid || maxCode.String == "" {
		return "001", nil
	}

	var num int
	fmt.Sscanf(maxCode.String, "%d", &num)
	num++
	if num > 999 {
		return "", fmt.Errorf("catalogue full: maximum 999 entries")
	}
	return fmt.Sprintf("%03d", num), nil
}

func (s *Service) GetByCode(code string) (*Entry, error) {
	return s.scanOne(s.db.QueryRow(
		`SELECT id, code, youtube_id, title, artist, duration_seconds, thumbnail_path, video_path, last_played_at, play_count, created_at
		 FROM catalogue WHERE code = ?`, code,
	))
}

func (s *Service) GetByYoutubeID(youtubeID string) (*Entry, error) {
	return s.scanOne(s.db.QueryRow(
		`SELECT id, code, youtube_id, title, artist, duration_seconds, thumbnail_path, video_path, last_played_at, play_count, created_at
		 FROM catalogue WHERE youtube_id = ?`, youtubeID,
	))
}

func (s *Service) List(limit, offset int) ([]Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, code, youtube_id, title, artist, duration_seconds, thumbnail_path, video_path, last_played_at, play_count, created_at
		 FROM catalogue ORDER BY code ASC LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanMany(rows)
}

func (s *Service) Search(query string) ([]Entry, error) {
	q := "%" + strings.ToLower(query) + "%"
	rows, err := s.db.Query(
		`SELECT id, code, youtube_id, title, artist, duration_seconds, thumbnail_path, video_path, last_played_at, play_count, created_at
		 FROM catalogue WHERE LOWER(title) LIKE ? OR LOWER(artist) LIKE ? ORDER BY code ASC`, q, q,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanMany(rows)
}

func (s *Service) Delete(code string) error {
	result, err := s.db.Exec(`DELETE FROM catalogue WHERE code = ?`, code)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("catalogue entry %s not found", code)
	}
	return nil
}

func (s *Service) Update(code, title, artist string) error {
	result, err := s.db.Exec(`UPDATE catalogue SET title = ?, artist = ? WHERE code = ?`, title, artist, code)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("catalogue entry %s not found", code)
	}
	return nil
}

func (s *Service) UpdateCode(oldCode, newCode string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Temporarily disable FK checks so we can update both tables without ordering issues
	if _, err := tx.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	if _, err := tx.Exec(`UPDATE catalogue SET code = ? WHERE code = ?`, newCode, oldCode); err != nil {
		return fmt.Errorf("update catalogue: %w", err)
	}
	if _, err := tx.Exec(`UPDATE requests SET catalogue_code = ? WHERE catalogue_code = ?`, newCode, oldCode); err != nil {
		return fmt.Errorf("update requests: %w", err)
	}
	if _, err := tx.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("re-enable foreign keys: %w", err)
	}
	return tx.Commit()
}

func (s *Service) SetVideoPath(youtubeID, videoPath string) error {
	_, err := s.db.Exec(`UPDATE catalogue SET video_path = ? WHERE youtube_id = ?`, videoPath, youtubeID)
	return err
}

func (s *Service) MarkPlayed(code string) error {
	_, err := s.db.Exec(
		`UPDATE catalogue SET last_played_at = CURRENT_TIMESTAMP, play_count = play_count + 1 WHERE code = ?`, code,
	)
	return err
}

func (s *Service) GetRandom() (*Entry, error) {
	return s.scanOne(s.db.QueryRow(
		`SELECT id, code, youtube_id, title, artist, duration_seconds, thumbnail_path, video_path, last_played_at, play_count, created_at
		 FROM catalogue WHERE video_path IS NOT NULL ORDER BY RANDOM() LIMIT 1`,
	))
}

func (s *Service) GetAll() ([]Entry, error) {
	rows, err := s.db.Query(
		`SELECT id, code, youtube_id, title, artist, duration_seconds, thumbnail_path, video_path, last_played_at, play_count, created_at
		 FROM catalogue ORDER BY code ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanMany(rows)
}

func (s *Service) scanOne(row *sql.Row) (*Entry, error) {
	e := &Entry{}
	err := row.Scan(&e.ID, &e.Code, &e.YoutubeID, &e.Title, &e.Artist,
		&e.DurationSeconds, &e.ThumbnailPath, &e.VideoPath, &e.LastPlayedAt, &e.PlayCount, &e.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Service) scanMany(rows *sql.Rows) ([]Entry, error) {
	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Code, &e.YoutubeID, &e.Title, &e.Artist,
			&e.DurationSeconds, &e.ThumbnailPath, &e.VideoPath, &e.LastPlayedAt, &e.PlayCount, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
