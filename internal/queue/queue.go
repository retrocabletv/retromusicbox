package queue

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrRateLimit is returned by Add when the caller has exceeded
// MaxRequestsPerCallerPerHour. Callers can detect it with errors.Is so they
// can surface a specific user-facing message instead of a generic failure.
var ErrRateLimit = errors.New("rate limit exceeded")

// queueOrder defines how queued/ready items are sorted.
// Most-voted videos play first; ties broken by arrival time.
const queueOrder = `vote_count DESC, id ASC`

type Request struct {
	ID            int64   `json:"id"`
	CatalogueCode string  `json:"catalogue_code"`
	CallerID      string  `json:"caller_id,omitempty"`
	RequestedAt   string  `json:"requested_at"`
	Status        string  `json:"status"`
	PlayedAt      *string `json:"played_at,omitempty"`
	VoteCount     int     `json:"vote_count"`
}

type QueueItem struct {
	Code      string `json:"code"`
	Artist    string `json:"artist"`
	Title     string `json:"title"`
	VoteCount int    `json:"vote_count"`
}

type Service struct {
	db                          *sql.DB
	maxRequestsPerCallerPerHour int
	allowDuplicate              bool
}

func NewService(db *sql.DB, maxPerHour int, allowDuplicate bool) *Service {
	return &Service{
		db:                          db,
		maxRequestsPerCallerPerHour: maxPerHour,
		allowDuplicate:              allowDuplicate,
	}
}

func (s *Service) Add(catalogueCode, callerID string) (*Request, int, error) {
	return s.add(catalogueCode, callerID, true)
}

// AddBypassRateLimit is like Add but skips the per-caller hourly rate limit.
// Used for operator-initiated requests (e.g. the rmbctl playnow command) that
// are not driven by an end-user.
func (s *Service) AddBypassRateLimit(catalogueCode, callerID string) (*Request, int, error) {
	return s.add(catalogueCode, callerID, false)
}

func (s *Service) add(catalogueCode, callerID string, applyRateLimit bool) (*Request, int, error) {
	// Vote stacking: if this video is already queued, increment its vote count
	if !s.allowDuplicate {
		var existingID int64
		err := s.db.QueryRow(
			`SELECT id FROM requests
			 WHERE catalogue_code = ? AND status IN ('queued', 'ready', 'fetching')`,
			catalogueCode,
		).Scan(&existingID)
		if err == nil {
			// Bump vote count
			_, err = s.db.Exec(
				`UPDATE requests SET vote_count = vote_count + 1 WHERE id = ?`,
				existingID,
			)
			if err != nil {
				return nil, 0, fmt.Errorf("bump vote count: %w", err)
			}
			// Recalculate position after vote bump (ordering may have changed)
			pos := s.positionOf(existingID)
			var req Request
			s.db.QueryRow(
				`SELECT id, catalogue_code, caller_id, requested_at, status, vote_count
				 FROM requests WHERE id = ?`, existingID,
			).Scan(&req.ID, &req.CatalogueCode, &req.CallerID, &req.RequestedAt, &req.Status, &req.VoteCount)
			return &req, pos, nil
		}
		if err != sql.ErrNoRows {
			return nil, 0, err
		}
	}

	// Rate limit per caller
	if applyRateLimit && callerID != "" && callerID != "web" {
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
			return nil, 0, fmt.Errorf("%w: max %d requests per hour", ErrRateLimit, s.maxRequestsPerCallerPerHour)
		}
	}

	result, err := s.db.Exec(
		`INSERT INTO requests (catalogue_code, caller_id, vote_count) VALUES (?, ?, 1)`,
		catalogueCode, callerID,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("insert request: %w", err)
	}

	id, _ := result.LastInsertId()
	position := s.positionOf(id)

	req := &Request{
		ID:            id,
		CatalogueCode: catalogueCode,
		CallerID:      callerID,
		Status:        "queued",
		VoteCount:     1,
	}

	return req, position, nil
}

// Prioritise bumps a request's vote_count above every other active item so it
// becomes next in queue order. The request must still be active (queued,
// ready, or fetching) — already-played or failed entries are left alone.
func (s *Service) Prioritise(id int64) error {
	result, err := s.db.Exec(
		`UPDATE requests
		 SET vote_count = COALESCE((
		     SELECT MAX(vote_count) FROM requests
		     WHERE status IN ('queued', 'ready', 'fetching') AND id != ?
		 ), 0) + 1
		 WHERE id = ? AND status IN ('queued', 'ready', 'fetching')`,
		id, id,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("request %d not active", id)
	}
	return nil
}

// positionOf returns the 1-based position of a request in the active queue.
func (s *Service) positionOf(id int64) int {
	var position int
	// Count how many active items would appear before this one in queue order
	s.db.QueryRow(
		`SELECT COUNT(*) + 1 FROM requests
		 WHERE status IN ('queued', 'ready', 'playing')
		   AND id != ?
		   AND (
		     vote_count > (SELECT vote_count FROM requests WHERE id = ?)
		     OR (vote_count = (SELECT vote_count FROM requests WHERE id = ?) AND id < ?)
		   )`,
		id, id, id, id,
	).Scan(&position)
	return position
}

func (s *Service) GetActive() ([]Request, error) {
	rows, err := s.db.Query(
		fmt.Sprintf(
			`SELECT id, catalogue_code, caller_id, requested_at, status, played_at, vote_count
			 FROM requests WHERE status IN ('queued', 'ready', 'fetching', 'playing')
			 ORDER BY %s`, queueOrder),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []Request
	for rows.Next() {
		var r Request
		var callerID, playedAt sql.NullString
		if err := rows.Scan(&r.ID, &r.CatalogueCode, &callerID, &r.RequestedAt, &r.Status, &playedAt, &r.VoteCount); err != nil {
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
		fmt.Sprintf(
			`SELECT r.catalogue_code, c.artist, c.title, r.vote_count
			 FROM requests r JOIN catalogue c ON r.catalogue_code = c.code
			 WHERE r.status IN ('queued', 'ready')
			 ORDER BY %s`, queueOrder),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []QueueItem
	for rows.Next() {
		var item QueueItem
		if err := rows.Scan(&item.Code, &item.Artist, &item.Title, &item.VoteCount); err != nil {
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
		fmt.Sprintf(
			`SELECT id, catalogue_code, caller_id, requested_at, status, vote_count
			 FROM requests WHERE status IN ('queued', 'ready')
			 ORDER BY %s LIMIT 1`, queueOrder),
	).Scan(&r.ID, &r.CatalogueCode, &callerID, &r.RequestedAt, &r.Status, &r.VoteCount)
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
		fmt.Sprintf(
			`SELECT id, catalogue_code, caller_id, requested_at, status, played_at, vote_count
			 FROM requests WHERE status IN ('queued', 'ready')
			 ORDER BY %s LIMIT ?`, queueOrder), n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []Request
	for rows.Next() {
		var r Request
		var callerID, playedAt sql.NullString
		if err := rows.Scan(&r.ID, &r.CatalogueCode, &callerID, &r.RequestedAt, &r.Status, &playedAt, &r.VoteCount); err != nil {
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

// ResetStale marks any playing/fetching requests as queued. Called on startup
// to recover from unclean shutdowns.
func (s *Service) ResetStale() error {
	_, err := s.db.Exec(`UPDATE requests SET status = 'queued' WHERE status IN ('playing', 'fetching')`)
	return err
}

func (s *Service) SetReady(catalogueCode string) error {
	_, err := s.db.Exec(
		`UPDATE requests SET status = 'ready' WHERE catalogue_code = ? AND status IN ('queued', 'fetching')`,
		catalogueCode,
	)
	return err
}
