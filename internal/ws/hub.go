package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

type Hub struct {
	mu          sync.RWMutex
	clients     map[*Client]bool
	currentState []byte // last broadcast message, sent to new clients on connect
	onMessage   func(msg json.RawMessage)
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[*Client]bool),
	}
}

func (h *Hub) SetOnMessage(fn func(json.RawMessage)) {
	h.onMessage = fn
}

func (h *Hub) SetCurrentState(state []byte) {
	h.mu.Lock()
	h.currentState = state
	h.mu.Unlock()
}

func (h *Hub) Broadcast(msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ws] marshal error: %v", err)
		return
	}

	h.mu.Lock()
	h.currentState = data
	h.mu.Unlock()

	h.broadcast(data)
}

// BroadcastEvent sends a message to all clients without updating the replay
// state. Use this for supplementary updates (queue changes, dial updates)
// that don't represent the full channel state.
func (h *Hub) BroadcastEvent(msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ws] marshal error: %v", err)
		return
	}

	h.broadcast(data)
}

func (h *Hub) broadcast(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		select {
		case client.send <- data:
		default:
			close(client.send)
			delete(h.clients, client)
		}
	}
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, 256),
	}

	h.mu.Lock()
	h.clients[client] = true
	state := h.currentState
	h.mu.Unlock()

	log.Printf("[ws] client connected (total: %d)", len(h.clients))

	// Send current state to new client
	if state != nil {
		client.send <- state
	}

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.hub.mu.Lock()
		delete(c.hub.clients, c)
		c.hub.mu.Unlock()
		c.conn.Close()
		log.Printf("[ws] client disconnected")
	}()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if c.hub.onMessage != nil {
			c.hub.onMessage(json.RawMessage(message))
		}
	}
}

func (c *Client) writePump() {
	defer c.conn.Close()

	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
