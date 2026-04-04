package queue

import (
	"database/sql"
	"fmt"
	"time"
)

type Request struct {
	ID            int64  `json:"id"`
	CatalogueCode string `json:"catalogue_code"`
	CallerID      string `json:"caller_id,omitempty"`
	RequestedAt   string `json:"requested_at"`
	Status        string `json:"status"`
	PlayedAt      *string `json:"played_at,omitempty"`
}

type QueueItem struct {
	Code   string `json:"code"`
	Artist string `json:"artist"`
	Title  string `json:"title"`
}

type Service struct {
	db                      *sql.DB
	maxRequestsPerCallerPerHour int
	allowDuplicate          bool
}

func NewService(db *sql.DB, maxPerHour int, allowDuplicate bool) *Service {
	return &Service{
		db:                      db,
		maxRequestsPerCallerPerHour: maxPerHour,
		allowDuplicate:          allowDuplicate,
	}
}

func (s *Service) Add(catalogueCode, callerID string) (*Request, int, error) {
	// Check for duplicate in active queue
	if !s.allowDuplicate {
		var existingID int64
		var pos int
		err := s.db.QueryRow(
			`SELECT r.id, (SELECT COUNT(*) FROM requests r2
			 WHERE r2.status IN ('queued','ready','playing') AND r2.id <= r.id) as pos
			 FROM requests r
			 WHERE r.catalogue_code = ? AND r.status IN ('queued', 'ready', 'playing')`,
			catalogueCode,
		).Scan(&existingID, &pos)
		if err == nil {
			return &Request{ID: existingID, CatalogueCode: catalogueCode, Status: "queued"}, pos, nil
		}
		if err != sql.ErrNoRows {
			return nil, 0, err
		}
	}

	// Rate limit per caller
	if callerID != "" && callerID != "web" {
		var count int
		oneHourAgo := time.Now().Add(-1 * time.Hour).Format(time.DateTime)
		err := s.db.QueryRow(
			`SELECT COUNT(*) FROM requests WHERE caller_id = ? AND requested_at > ?`,
			callerID, oneHourAgo,
		).Scan(&count)
		if err != nil {
			return nil, 0, err
		}
		if count >= s.maxRequestsPerCallerPerHour {
			return nil, 0, fmt.Errorf("rate limit exceeded: max %d requests per hour", s.maxRequestsPerCallerPerHour)
		}
	}

	result, err := s.db.Exec(
		`INSERT INTO requests (catalogue_code, caller_id) VALUES (?, ?)`,
		catalogueCode, callerID,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("insert request: %w", err)
	}

	id, _ := result.LastInsertId()

	// Get position
	var position int
	s.db.QueryRow(
		`SELECT COUNT(*) FROM requests WHERE status IN ('queued', 'ready', 'playing') AND id <= ?`, id,
	).Scan(&position)

	req := &Request{
		ID:            id,
		CatalogueCode: catalogueCode,
		CallerID:      callerID,
		Status:        "queued",
	}

	return req, position, nil
}

func (s *Service) GetActive() ([]Request, error) {
	rows, err := s.db.Query(
		`SELECT id, catalogue_code, caller_id, requested_at, status, played_at
		 FROM requests WHERE status IN ('queued', 'ready', 'fetching', 'playing')
		 ORDER BY id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []Request
	for rows.Next() {
		var r Request
		var callerID, playedAt sql.NullString
		if err := rows.Scan(&r.ID, &r.CatalogueCode, &callerID, &r.RequestedAt, &r.Status, &playedAt); err != nil {
			return nil, err
		}
		if callerID.Valid {
			r.CallerID = callerID.String
		}
		if playedAt.Valid {
			r.PlayedAt = &playedAt.String
		}
		requests = append(requests, r)
	}
	return requests, rows.Err()
}

func (s *Service) GetActiveWithDetails() ([]QueueItem, error) {
	rows, err := s.db.Query(
		`SELECT r.catalogue_code, c.artist, c.title
		 FROM requests r JOIN catalogue c ON r.catalogue_code = c.code
		 WHERE r.status IN ('queued', 'ready')
		 ORDER BY r.id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []QueueItem
	for rows.Next() {
		var item QueueItem
		if err := rows.Scan(&item.Code, &item.Artist, &item.Title); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetNext() (*Request, error) {
	var r Request
	var callerID sql.NullString
	err := s.db.QueryRow(
		`SELECT id, catalogue_code, caller_id, requested_at, status
		 FROM requests WHERE status IN ('queued', 'ready')
		 ORDER BY id ASC LIMIT 1`,
	).Scan(&r.ID, &r.CatalogueCode, &callerID, &r.RequestedAt, &r.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if callerID.Valid {
		r.CallerID = callerID.String
	}
	return &r, nil
}

func (s *Service) GetTopN(n int) ([]Request, error) {
	rows, err := s.db.Query(
		`SELECT id, catalogue_code, caller_id, requested_at, status, played_at
		 FROM requests WHERE status IN ('queued', 'ready')
		 ORDER BY id ASC LIMIT ?`, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []Request
	for rows.Next() {
		var r Request
		var callerID, playedAt sql.NullString
		if err := rows.Scan(&r.ID, &r.CatalogueCode, &callerID, &r.RequestedAt, &r.Status, &playedAt); err != nil {
			return nil, err
		}
		if callerID.Valid {
			r.CallerID = callerID.String
		}
		if playedAt.Valid {
			r.PlayedAt = &playedAt.String
		}
		requests = append(requests, r)
	}
	return requests, rows.Err()
}

func (s *Service) UpdateStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE requests SET status = ? WHERE id = ?`, status, id)
	return err
}

func (s *Service) MarkPlaying(id int64) error {
	_, err := s.db.Exec(`UPDATE requests SET status = 'playing' WHERE id = ?`, id)
	return err
}

func (s *Service) MarkPlayed(id int64) error {
	_, err := s.db.Exec(
		`UPDATE requests SET status = 'played', played_at = CURRENT_TIMESTAMP WHERE id = ?`, id,
	)
	return err
}

func (s *Service) MarkFailed(id int64) error {
	_, err := s.db.Exec(`UPDATE requests SET status = 'failed' WHERE id = ?`, id)
	return err
}

func (s *Service) Delete(id int64) error {
	result, err := s.db.Exec(`DELETE FROM requests WHERE id = ? AND status IN ('queued', 'ready')`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("request %d not found or not deletable", id)
	}
	return nil
}

func (s *Service) SkipCurrent() error {
	_, err := s.db.Exec(`UPDATE requests SET status = 'played', played_at = CURRENT_TIMESTAMP WHERE status = 'playing'`)
	return err
}

func (s *Service) ActiveCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM requests WHERE status IN ('queued', 'ready', 'playing')`).Scan(&count)
	return count, err
}

func (s *Service) SetReady(catalogueCode string) error {
	_, err := s.db.Exec(
		`UPDATE requests SET status = 'ready' WHERE catalogue_code = ? AND status IN ('queued', 'fetching')`,
		catalogueCode,
	)
	return err
}
