package ivr

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/alexkinch/thebox/internal/catalogue"
	"github.com/alexkinch/thebox/internal/config"
	"github.com/alexkinch/thebox/internal/queue"
)

type Handler struct {
	cfg       config.IVRConfig
	catalogue *catalogue.Service
	queue     *queue.Service
	onChange  func()
}

func NewHandler(cfg config.IVRConfig, cat *catalogue.Service, q *queue.Service, onChange func()) *Handler {
	return &Handler{
		cfg:       cfg,
		catalogue: cat,
		queue:     q,
		onChange:  onChange,
	}
}

// Jambonz webhook JSON structures

type JambonzCallPayload struct {
	CallSid  string `json:"call_sid"`
	From     string `json:"from"`
	To       string `json:"to"`
	CallerID string `json:"caller_id"`
}

type JambonzDTMFPayload struct {
	CallSid string `json:"call_sid"`
	Digits  string `json:"digits"`
	From    string `json:"from"`
}

type JambonzVerb map[string]interface{}

// HandleCall is the initial call webhook handler.
func (h *Handler) HandleCall(w http.ResponseWriter, r *http.Request) {
	var payload JambonzCallPayload
	json.NewDecoder(r.Body).Decode(&payload)
	log.Printf("[ivr] incoming call from %s", payload.From)

	resp := []JambonzVerb{
		{
			"verb": "play",
			"url":  "/" + h.cfg.WelcomeJingle,
		},
		{
			"verb":       "say",
			"text":       "Welcome to The Box. Enter your three digit catalogue number followed by hash.",
			"synthesizer": map[string]string{"vendor": "google", "language": "en-GB"},
		},
		{
			"verb":        "gather",
			"input":       []string{"dtmf"},
			"numDigits":   3,
			"finishOnKey": "#",
			"timeout":     h.cfg.GatherTimeoutSeconds,
			"actionHook":  h.cfg.WebhookBasePath + "/dtmf",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleDTMF processes the gathered digits.
func (h *Handler) HandleDTMF(w http.ResponseWriter, r *http.Request) {
	var payload JambonzDTMFPayload
	json.NewDecoder(r.Body).Decode(&payload)
	log.Printf("[ivr] DTMF received: %s from %s", payload.Digits, payload.From)

	code := payload.Digits
	entry, err := h.catalogue.GetByCode(code)
	if err != nil || entry == nil {
		h.respondInvalid(w, payload.From, 1)
		return
	}

	req, position, err := h.queue.Add(code, payload.From)
	if err != nil {
		log.Printf("[ivr] queue add error: %v", err)
		resp := []JambonzVerb{
			{
				"verb":       "say",
				"text":       fmt.Sprintf("Sorry, %s. Please try again later.", err.Error()),
				"synthesizer": map[string]string{"vendor": "google", "language": "en-GB"},
			},
			{"verb": "hangup"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	_ = req // used for logging if needed

	resp := []JambonzVerb{
		{
			"verb": "say",
			"text": fmt.Sprintf(
				"You have requested %s by %s. Your video is number %d in the queue. Thank you for calling The Box.",
				entry.Title, entry.Artist, position,
			),
			"synthesizer": map[string]string{"vendor": "google", "language": "en-GB"},
		},
		{"verb": "hangup"},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)

	if h.onChange != nil {
		h.onChange()
	}
}

func (h *Handler) respondInvalid(w http.ResponseWriter, from string, attempt int) {
	if attempt >= h.cfg.MaxAttempts {
		resp := []JambonzVerb{
			{
				"verb":       "say",
				"text":       "Sorry, that number was not recognised. Goodbye.",
				"synthesizer": map[string]string{"vendor": "google", "language": "en-GB"},
			},
			{"verb": "hangup"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	resp := []JambonzVerb{
		{
			"verb":       "say",
			"text":       "Sorry, that number was not recognised. Please try again.",
			"synthesizer": map[string]string{"vendor": "google", "language": "en-GB"},
		},
		{
			"verb":        "gather",
			"input":       []string{"dtmf"},
			"numDigits":   3,
			"finishOnKey": "#",
			"timeout":     h.cfg.GatherTimeoutSeconds,
			"actionHook":  h.cfg.WebhookBasePath + "/dtmf",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleStatus processes call status updates.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
