// Package ivr exposes a small service-agnostic session API so any IVR
// front-end (Jambonz, Twilio, Asterisk, a Pi with a DTMF decoder, a test
// script) can drive on-channel digit entry. A request moves through
// three explicit states:
//
//	dialling  -> caller is entering digits
//	validated -> backend confirmed the code resolves to a catalogue entry
//	             and is waiting for the caller to confirm or cancel; the
//	             on-screen overlay shows the artist + title during this
//	             window so the viewer sees what is being requested
//	success   -> caller confirmed, request is on the queue, "Thanx!"
//	fail      -> unknown code or rejected by the queue, "Try again"
//
// A typical adapter makes these calls:
//
//	POST   /api/ivr/sessions              -> create {session_id}
//	POST   /api/ivr/sessions/{id}/digit   -> {"digit": "5"} (dialling)
//	POST   /api/ivr/sessions/{id}/confirm -> commit (validated -> success)
//	POST   /api/ivr/sessions/{id}/cancel  -> back to empty dialling
//	DELETE /api/ivr/sessions/{id}         -> caller hung up
//
// For dumb DTMF forwarders, `digit` is also state-aware: in `validated`,
// "1" confirms and "2" or "*" cancel, so an adapter can just forward
// every keypress to /digit and let the backend interpret it.
//
// At most MaxConcurrent sessions (default 3) are accepted at once; the
// patent allowed multiple simultaneous callers on-screen and the frontend
// overlay is already wired up for three. Every state change broadcasts a
// `dial_update` WebSocket event so the channel shows the phone icon,
// digit stream, validated selection, and accept/reject feedback (patent
// FIG. 1 step 32, "DISPLAY SELECTION #").
package ivr

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexkinch/retromusicbox/internal/catalogue"
	"github.com/alexkinch/retromusicbox/internal/queue"
	"github.com/alexkinch/retromusicbox/internal/ws"
)

const (
	MaxConcurrent    = 3
	CodeLength       = 3
	SessionTTL       = 30 * time.Second
	ResultLingerTime = 4 * time.Second
	DefaultConfirmTTL = 15 * time.Second
)

type sessionStatus string

const (
	statusDialling  sessionStatus = "dialling"
	statusValidated sessionStatus = "validated"
	statusSuccess   sessionStatus = "success"
	statusFail      sessionStatus = "fail"
)

// Stable machine-readable reason codes returned alongside status=fail so
// front-ends (telephony TTS, web UI) can branch on a known string instead
// of pattern-matching the human-readable Reason message. Keep these
// additive — clients should treat unknown codes as a generic failure.
const (
	reasonIncompleteCode = "incomplete_code"
	reasonUnknownCode    = "unknown_code"
	reasonRateLimited    = "rate_limited"
	reasonQueueError     = "queue_error"
)

type session struct {
	ID         string        `json:"id"`
	Digits     string        `json:"digits"`
	Status     sessionStatus `json:"status"`
	CallerID   string        `json:"caller_id,omitempty"`
	CreatedAt  time.Time     `json:"-"`
	UpdatedAt  time.Time     `json:"-"`
	entry          *catalogue.Entry
	position       int
	failReason     string
	failReasonCode string
	committing     bool
}

type Handler struct {
	mu         sync.Mutex
	sessions   map[string]*session
	catalogue  *catalogue.Service
	queue      *queue.Service
	hub        *ws.Hub
	onChange   func()
	confirmTTL time.Duration
}

func NewHandler(cat *catalogue.Service, q *queue.Service, hub *ws.Hub, onChange func(), confirmTTL time.Duration) *Handler {
	if confirmTTL <= 0 {
		confirmTTL = DefaultConfirmTTL
	}
	h := &Handler{
		sessions:   make(map[string]*session),
		catalogue:  cat,
		queue:      q,
		hub:        hub,
		onChange:   onChange,
		confirmTTL: confirmTTL,
	}
	go h.reaper()
	return h
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ivr/sessions", h.handleCreate)
	mux.HandleFunc("POST /api/ivr/sessions/{id}/digit", h.handleDigit)
	mux.HandleFunc("POST /api/ivr/sessions/{id}/submit", h.handleSubmit)
	mux.HandleFunc("POST /api/ivr/sessions/{id}/confirm", h.handleConfirm)
	mux.HandleFunc("POST /api/ivr/sessions/{id}/cancel", h.handleCancel)
	mux.HandleFunc("DELETE /api/ivr/sessions/{id}", h.handleDelete)
	mux.HandleFunc("GET /api/ivr/sessions/{id}", h.handleGet)
}

// --- handlers ---------------------------------------------------------------

type createRequest struct {
	CallerID string `json:"caller_id,omitempty"`
}

type createResponse struct {
	SessionID string `json:"session_id"`
	ExpiresIn int    `json:"expires_in_seconds"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	h.mu.Lock()
	if h.activeCount() >= MaxConcurrent {
		h.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "all lines are busy")
		return
	}
	id := newSessionID()
	now := time.Now()
	s := &session{
		ID:        id,
		Digits:    "",
		Status:    statusDialling,
		CallerID:  req.CallerID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	h.sessions[id] = s
	h.mu.Unlock()

	h.broadcastDialUpdate()

	writeJSON(w, http.StatusCreated, createResponse{
		SessionID: id,
		ExpiresIn: int(SessionTTL / time.Second),
	})
}

type digitRequest struct {
	Digit string `json:"digit"`
}

type sessionResponse struct {
	ID     string        `json:"id"`
	Digits string        `json:"digits"`
	Status sessionStatus `json:"status"`
	// Populated on status=success
	Code     string `json:"code,omitempty"`
	Title    string `json:"title,omitempty"`
	Artist   string `json:"artist,omitempty"`
	Position int    `json:"position,omitempty"`
	// Populated on status=fail. Reason is human-readable and may change;
	// ReasonCode is a stable machine-readable identifier for clients to
	// branch on (see reason* constants).
	Reason     string `json:"reason,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
}

// handleDigit is state-aware. In `dialling` it accumulates digits and
// auto-submits at CodeLength; in `validated` it maps "1" to confirm and
// "2"/"*" to cancel so a dumb DTMF forwarder can drive the whole flow
// by POSTing every keypress here.
func (h *Handler) handleDigit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req digitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	d := strings.TrimSpace(req.Digit)
	if len(d) == 0 {
		writeError(w, http.StatusBadRequest, "digit is required")
		return
	}

	h.mu.Lock()
	s, ok := h.sessions[id]
	if !ok {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	currentStatus := s.Status
	h.mu.Unlock()

	switch currentStatus {
	case statusDialling:
		h.handleDiallingDigit(w, id, d)
	case statusValidated:
		h.handleValidatedDigit(w, id, d)
	default:
		writeJSON(w, http.StatusOK, h.snapshot(id))
	}
}

func (h *Handler) handleDiallingDigit(w http.ResponseWriter, id, d string) {
	if d == "#" {
		h.submit(w, id)
		return
	}
	if d == "*" {
		h.clearDigits(w, id)
		return
	}
	if len(d) != 1 || d[0] < '0' || d[0] > '9' {
		writeError(w, http.StatusBadRequest, "digit must be one of 0-9, #, *")
		return
	}

	h.mu.Lock()
	s, ok := h.sessions[id]
	if !ok {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if s.Status != statusDialling {
		resp := h.snapshotLocked(s)
		h.mu.Unlock()
		writeJSON(w, http.StatusConflict, resp)
		return
	}
	if len(s.Digits) >= CodeLength {
		resp := h.snapshotLocked(s)
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	s.Digits += d
	s.UpdatedAt = time.Now()
	full := len(s.Digits) == CodeLength
	resp := h.snapshotLocked(s)
	h.mu.Unlock()

	h.broadcastDialUpdate()

	if full {
		h.submit(w, id)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleValidatedDigit(w http.ResponseWriter, id, d string) {
	switch d {
	case "1":
		h.confirm(w, id)
	case "2", "*":
		h.cancelAndReset(w, id)
	default:
		// Ignore other digits while waiting for confirm/cancel.
		writeJSON(w, http.StatusOK, h.snapshot(id))
	}
}

func (h *Handler) clearDigits(w http.ResponseWriter, id string) {
	h.mu.Lock()
	s, ok := h.sessions[id]
	if !ok {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	s.Digits = ""
	s.Status = statusDialling
	s.entry = nil
	s.position = 0
	s.failReason = ""
	s.UpdatedAt = time.Now()
	resp := h.snapshotLocked(s)
	h.mu.Unlock()
	h.broadcastDialUpdate()
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	h.submit(w, r.PathValue("id"))
}

func (h *Handler) handleConfirm(w http.ResponseWriter, r *http.Request) {
	h.confirm(w, r.PathValue("id"))
}

func (h *Handler) handleCancel(w http.ResponseWriter, r *http.Request) {
	h.cancelAndReset(w, r.PathValue("id"))
}

// submit validates the entered digits against the catalogue and transitions
// to `validated` on success or `fail` on an unknown code. It does NOT commit
// the request to the queue — that happens in confirm() after the caller has
// acknowledged the playback of "you chose X, press 1 to confirm".
func (h *Handler) submit(w http.ResponseWriter, id string) {
	h.mu.Lock()
	s, ok := h.sessions[id]
	if !ok {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if s.Status != statusDialling {
		resp := h.snapshotLocked(s)
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	code := s.Digits
	h.mu.Unlock()

	if len(code) != CodeLength {
		h.finalise(id, statusFail, "incomplete code", reasonIncompleteCode, nil, 0)
		writeJSON(w, http.StatusOK, h.snapshot(id))
		return
	}

	entry, err := h.catalogue.GetByCode(code)
	if err != nil || entry == nil {
		h.finalise(id, statusFail, "unknown code", reasonUnknownCode, nil, 0)
		writeJSON(w, http.StatusOK, h.snapshot(id))
		return
	}

	h.finalise(id, statusValidated, "", "", entry, 0)
	writeJSON(w, http.StatusOK, h.snapshot(id))
}

// confirm commits a previously validated session to the queue and flips
// the status to `success`. Called either via POST /confirm directly or
// via POST /digit with "1" while in the validated state.
func (h *Handler) confirm(w http.ResponseWriter, id string) {
	h.mu.Lock()
	s, ok := h.sessions[id]
	if !ok {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if s.Status != statusValidated || s.committing || s.entry == nil {
		resp := h.snapshotLocked(s)
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	s.committing = true
	entry := s.entry
	callerID := s.CallerID
	if callerID == "" {
		callerID = "ivr:" + id
	}
	h.mu.Unlock()

	_, position, err := h.queue.Add(entry.Code, callerID)
	if err != nil {
		h.mu.Lock()
		if s, ok := h.sessions[id]; ok {
			s.committing = false
		}
		h.mu.Unlock()
		code := reasonQueueError
		if errors.Is(err, queue.ErrRateLimit) {
			code = reasonRateLimited
		}
		h.finalise(id, statusFail, err.Error(), code, entry, 0)
		writeJSON(w, http.StatusOK, h.snapshot(id))
		return
	}

	h.finalise(id, statusSuccess, "", "", entry, position)
	if h.onChange != nil {
		h.onChange()
	}
	writeJSON(w, http.StatusOK, h.snapshot(id))
}

// cancelAndReset rolls a `validated` (or `dialling`) session back to an
// empty dialling state so the caller can try again without hanging up.
// Called via POST /cancel, via POST /digit with "2" or "*" while in
// validated, or via POST /digit with "*" while in dialling.
func (h *Handler) cancelAndReset(w http.ResponseWriter, id string) {
	h.clearDigits(w, id)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.mu.Lock()
	_, ok := h.sessions[id]
	if ok {
		delete(h.sessions, id)
	}
	h.mu.Unlock()
	if ok {
		h.broadcastDialUpdate()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap := h.snapshot(id)
	if snap.ID == "" {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// --- internals --------------------------------------------------------------

func (h *Handler) finalise(id string, status sessionStatus, reason, reasonCode string, entry *catalogue.Entry, position int) {
	h.mu.Lock()
	s, ok := h.sessions[id]
	if !ok {
		h.mu.Unlock()
		return
	}
	s.Status = status
	s.UpdatedAt = time.Now()
	s.failReason = reason
	s.failReasonCode = reasonCode
	if entry != nil {
		s.entry = entry
	}
	if position > 0 {
		s.position = position
	}
	h.mu.Unlock()

	h.broadcastDialUpdate()
}

func (h *Handler) snapshot(id string) sessionResponse {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[id]
	if !ok {
		return sessionResponse{}
	}
	return h.snapshotLocked(s)
}

func (h *Handler) snapshotLocked(s *session) sessionResponse {
	resp := sessionResponse{
		ID:         s.ID,
		Digits:     s.Digits,
		Status:     s.Status,
		Reason:     s.failReason,
		ReasonCode: s.failReasonCode,
	}
	if s.entry != nil {
		resp.Code = s.entry.Code
		resp.Title = s.entry.Title
		resp.Artist = s.entry.Artist
	}
	resp.Position = s.position
	return resp
}

// activeCount counts sessions that are occupying a concurrent-caller slot.
// Dialling and validated both count — a caller waiting at the "press 1 to
// confirm" prompt is still on the line — but success/fail do not, so the
// next caller can connect immediately after "Thanx!"/"Try again" shows.
func (h *Handler) activeCount() int {
	n := 0
	for _, s := range h.sessions {
		if s.Status == statusDialling || s.Status == statusValidated {
			n++
		}
	}
	return n
}

// reaper evicts idle sessions:
//   - dialling: SessionTTL (no digit in 30s and we drop it)
//   - validated: confirmTTL (caller never pressed 1 or 2)
//   - success/fail: ResultLingerTime (overlay linger)
func (h *Handler) reaper() {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for range t.C {
		h.sweep()
	}
}

func (h *Handler) sweep() {
	now := time.Now()
	changed := false
	h.mu.Lock()
	for id, s := range h.sessions {
		var ttl time.Duration
		switch s.Status {
		case statusDialling:
			ttl = SessionTTL
		case statusValidated:
			ttl = h.confirmTTL
		default:
			ttl = ResultLingerTime
		}
		if now.Sub(s.UpdatedAt) > ttl {
			delete(h.sessions, id)
			changed = true
		}
	}
	h.mu.Unlock()
	if changed {
		h.broadcastDialUpdate()
	}
}

type dialUpdate struct {
	Type    string         `json:"type"`
	Callers []dialSnapshot `json:"callers"`
}

type dialSnapshot struct {
	ID     string        `json:"id"`
	Digits string        `json:"digits"`
	Status sessionStatus `json:"status"`
	// Populated once a session reaches `validated` and kept through
	// `success`/`fail` so the on-screen overlay can render "→ 123 ARTIST
	// — TITLE" during the confirmation window and through to "Thanx!".
	Code   string `json:"code,omitempty"`
	Artist string `json:"artist,omitempty"`
	Title  string `json:"title,omitempty"`
}

func (h *Handler) broadcastDialUpdate() {
	if h.hub == nil {
		return
	}
	h.mu.Lock()
	callers := make([]dialSnapshot, 0, len(h.sessions))
	for _, s := range h.sessions {
		snap := dialSnapshot{ID: s.ID, Digits: s.Digits, Status: s.Status}
		if s.entry != nil {
			snap.Code = s.entry.Code
			snap.Artist = s.entry.Artist
			snap.Title = s.entry.Title
		}
		callers = append(callers, snap)
	}
	h.mu.Unlock()
	h.hub.BroadcastEvent(dialUpdate{Type: "dial_update", Callers: callers})
}

func newSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, code int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
