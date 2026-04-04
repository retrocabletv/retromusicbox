package playout

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/alexkinch/thebox/internal/catalogue"
	"github.com/alexkinch/thebox/internal/config"
	"github.com/alexkinch/thebox/internal/fetcher"
	"github.com/alexkinch/thebox/internal/queue"
	"github.com/alexkinch/thebox/internal/ws"
)

type State string

const (
	StateFiller     State = "filler"
	StatePlaying    State = "playing"
	StateTransition State = "transition"
)

// WebSocket message types
type PlayMessage struct {
	Type          string        `json:"type"`
	Video         VideoInfo     `json:"video"`
	Queue         []QueueEntry  `json:"queue"`
	PositionTotal int           `json:"position_total"`
}

type VideoInfo struct {
	CatalogueCode string `json:"catalogue_code"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	DurationSecs   int    `json:"duration_seconds"`
	MediaURL       string `json:"media_url"`
	ThumbnailURL   string `json:"thumbnail_url"`
}

type FillerMessage struct {
	Type        string            `json:"type"`
	Mode        string            `json:"mode"`
	Catalogue   []catalogue.Entry `json:"catalogue,omitempty"`
	PhoneNumber string            `json:"phone_number"`
}

type QueueUpdateMessage struct {
	Type          string       `json:"type"`
	Queue         []QueueEntry `json:"queue"`
	PositionTotal int          `json:"position_total"`
}

type QueueEntry struct {
	Code   string `json:"code"`
	Artist string `json:"artist"`
	Title  string `json:"title"`
}

type SkipMessage struct {
	Type string `json:"type"`
}

type Controller struct {
	mu             sync.Mutex
	state          State
	currentRequest *queue.Request
	cfg            config.PlayoutConfig
	channelCfg     config.ChannelConfig
	catalogue      *catalogue.Service
	queue          *queue.Service
	fetcher        *fetcher.Service
	hub            *ws.Hub
	stopCh         chan struct{}
	videoTimer     *time.Timer
	fillerStart    time.Time
	fillerMode     int // cycles through filler modes
}

func NewController(
	cfg config.PlayoutConfig,
	channelCfg config.ChannelConfig,
	cat *catalogue.Service,
	q *queue.Service,
	f *fetcher.Service,
	hub *ws.Hub,
) *Controller {
	c := &Controller{
		state:     StateFiller,
		cfg:       cfg,
		channelCfg: channelCfg,
		catalogue: cat,
		queue:     q,
		fetcher:   f,
		hub:       hub,
		stopCh:    make(chan struct{}),
	}

	hub.SetOnMessage(c.handleRendererMessage)

	return c
}

func (c *Controller) Start() {
	log.Println("[playout] controller starting")

	c.sendFiller("ident")

	go c.runLoop()
}

func (c *Controller) Stop() {
	close(c.stopCh)
	if c.videoTimer != nil {
		c.videoTimer.Stop()
	}
}

func (c *Controller) runLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.tick()
		}
	}
}

func (c *Controller) tick() {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.state {
	case StateFiller:
		// Check if there's something in the queue
		next, err := c.queue.GetNext()
		if err != nil {
			log.Printf("[playout] error getting next: %v", err)
			return
		}

		if next != nil && (next.Status == "ready" || c.fetcher.IsReady(c.getYoutubeID(next.CatalogueCode))) {
			c.startTransition(next)
			return
		}

		// Check if we should do random play
		if !c.fillerStart.IsZero() && time.Since(c.fillerStart) > time.Duration(c.cfg.FillerRandomDelayMinutes)*time.Minute {
			c.playRandom()
			return
		}

		// Cycle filler modes
		if !c.fillerStart.IsZero() && time.Since(c.fillerStart) > 30*time.Second {
			c.fillerMode = (c.fillerMode + 1) % 3
			modes := []string{"ident", "catalogue_scroll", "ident"}
			c.sendFiller(modes[c.fillerMode])
			c.fillerStart = time.Now()
		}

	case StatePlaying:
		// Playing is managed by timer and renderer messages
		return

	case StateTransition:
		// Transitions are time-limited, handled by timer
		return
	}
}

func (c *Controller) startTransition(req *queue.Request) {
	c.state = StateTransition

	entry, err := c.catalogue.GetByCode(req.CatalogueCode)
	if err != nil || entry == nil {
		log.Printf("[playout] catalogue lookup failed for %s: %v", req.CatalogueCode, err)
		c.queue.MarkFailed(req.ID)
		c.state = StateFiller
		return
	}

	// Send transition info to renderer
	queueItems := c.getQueueEntries()
	total, _ := c.queue.ActiveCount()

	transitionMsg := PlayMessage{
		Type: "transition",
		Video: VideoInfo{
			CatalogueCode: entry.Code,
			Title:          entry.Title,
			Artist:         entry.Artist,
			DurationSecs:   derefInt(entry.DurationSeconds),
			MediaURL:       "/media/" + entry.YoutubeID + ".mp4",
			ThumbnailURL:   "/media/thumbnails/" + entry.YoutubeID + ".jpg",
		},
		Queue:         queueItems,
		PositionTotal: total,
	}
	c.hub.Broadcast(transitionMsg)

	// After transition duration, start playing
	c.videoTimer = time.AfterFunc(time.Duration(c.cfg.TransitionSeconds)*time.Second, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.playVideo(req, entry)
	})
}

func (c *Controller) playVideo(req *queue.Request, entry *catalogue.Entry) {
	c.state = StatePlaying
	c.currentRequest = req

	c.queue.MarkPlaying(req.ID)
	c.catalogue.MarkPlayed(entry.Code)

	queueItems := c.getQueueEntries()
	total, _ := c.queue.ActiveCount()

	msg := PlayMessage{
		Type: "play",
		Video: VideoInfo{
			CatalogueCode: entry.Code,
			Title:          entry.Title,
			Artist:         entry.Artist,
			DurationSecs:   derefInt(entry.DurationSeconds),
			MediaURL:       "/media/" + entry.YoutubeID + ".mp4",
			ThumbnailURL:   "/media/thumbnails/" + entry.YoutubeID + ".jpg",
		},
		Queue:         queueItems,
		PositionTotal: total,
	}
	c.hub.Broadcast(msg)

	// Safety timer: if video doesn't end naturally, advance after duration + buffer
	duration := derefInt(entry.DurationSeconds)
	if duration <= 0 {
		duration = 300 // default 5 minutes
	}
	c.videoTimer = time.AfterFunc(time.Duration(duration+10)*time.Second, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.state == StatePlaying && c.currentRequest != nil && c.currentRequest.ID == req.ID {
			log.Printf("[playout] safety timer: advancing past %s", entry.Code)
			c.advanceQueue()
		}
	})
}

func (c *Controller) playRandom() {
	entry, err := c.catalogue.GetRandom()
	if err != nil || entry == nil {
		return // no cached videos available
	}

	log.Printf("[playout] random play: %s - %s", entry.Artist, entry.Title)

	c.state = StatePlaying

	msg := PlayMessage{
		Type: "play",
		Video: VideoInfo{
			CatalogueCode: "----",
			Title:          entry.Title,
			Artist:         entry.Artist,
			DurationSecs:   derefInt(entry.DurationSeconds),
			MediaURL:       "/media/" + entry.YoutubeID + ".mp4",
			ThumbnailURL:   "/media/thumbnails/" + entry.YoutubeID + ".jpg",
		},
		Queue:         nil,
		PositionTotal: 0,
	}
	c.hub.Broadcast(msg)

	duration := derefInt(entry.DurationSeconds)
	if duration <= 0 {
		duration = 300
	}
	c.videoTimer = time.AfterFunc(time.Duration(duration+10)*time.Second, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.state == StatePlaying && c.currentRequest == nil {
			c.state = StateFiller
			c.fillerStart = time.Now()
			c.sendFiller("ident")
		}
	})
}

func (c *Controller) handleRendererMessage(msg json.RawMessage) {
	var base struct {
		Type  string `json:"type"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg, &base); err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	switch base.Type {
	case "video_ended":
		log.Println("[playout] video ended (renderer)")
		if c.videoTimer != nil {
			c.videoTimer.Stop()
		}
		c.advanceQueue()

	case "video_error":
		log.Printf("[playout] video error: %s", base.Error)
		if c.videoTimer != nil {
			c.videoTimer.Stop()
		}
		if c.currentRequest != nil {
			c.queue.MarkFailed(c.currentRequest.ID)
		}
		c.advanceQueue()
	}
}

func (c *Controller) advanceQueue() {
	if c.currentRequest != nil {
		c.queue.MarkPlayed(c.currentRequest.ID)
		c.currentRequest = nil
	}

	next, err := c.queue.GetNext()
	if err != nil {
		log.Printf("[playout] error getting next: %v", err)
		c.goToFiller()
		return
	}

	if next != nil && (next.Status == "ready" || c.fetcher.IsReady(c.getYoutubeID(next.CatalogueCode))) {
		c.startTransition(next)
	} else {
		c.goToFiller()
	}
}

func (c *Controller) goToFiller() {
	c.state = StateFiller
	c.fillerStart = time.Now()
	c.fillerMode = 0
	c.sendFiller("ident")
}

func (c *Controller) sendFiller(mode string) {
	if c.fillerStart.IsZero() {
		c.fillerStart = time.Now()
	}

	msg := FillerMessage{
		Type:        "filler",
		Mode:        mode,
		PhoneNumber: c.channelCfg.PhoneNumberDisplay,
	}

	if mode == "catalogue_scroll" {
		entries, _ := c.catalogue.GetAll()
		msg.Catalogue = entries
	}

	c.hub.Broadcast(msg)
}

func (c *Controller) Skip() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.videoTimer != nil {
		c.videoTimer.Stop()
	}

	c.hub.Broadcast(SkipMessage{Type: "skip"})

	if c.currentRequest != nil {
		c.queue.MarkPlayed(c.currentRequest.ID)
		c.currentRequest = nil
	}

	c.advanceQueue()
}

// NotifyQueueChange sends a queue update to all connected renderers.
func (c *Controller) NotifyQueueChange() {
	c.mu.Lock()
	defer c.mu.Unlock()

	items := c.getQueueEntries()
	total, _ := c.queue.ActiveCount()

	msg := QueueUpdateMessage{
		Type:          "queue_update",
		Queue:         items,
		PositionTotal: total,
	}
	c.hub.Broadcast(msg)
}

func (c *Controller) getQueueEntries() []QueueEntry {
	items, err := c.queue.GetActiveWithDetails()
	if err != nil {
		return nil
	}
	entries := make([]QueueEntry, len(items))
	for i, item := range items {
		entries[i] = QueueEntry{
			Code:   item.Code,
			Artist: item.Artist,
			Title:  item.Title,
		}
	}
	return entries
}

func (c *Controller) getYoutubeID(code string) string {
	entry, err := c.catalogue.GetByCode(code)
	if err != nil || entry == nil {
		return ""
	}
	return entry.YoutubeID
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
