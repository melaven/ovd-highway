package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512
	sendBufSize    = 64
	controlBufSize = 256
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkOrigin,
}

type Hub struct {
	log        *slog.Logger
	broadcast  chan []byte
	register   chan *wsClient
	unregister chan *wsClient
	clients    map[*wsClient]struct{}
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		log:        log.With("component", "ws_hub"),
		broadcast:  make(chan []byte, controlBufSize),
		register:   make(chan *wsClient, controlBufSize),
		unregister: make(chan *wsClient, controlBufSize),
		clients:    make(map[*wsClient]struct{}),
	}
}

// Run processes client lifecycle and broadcast events until ctx is done.
// All map mutations happen here, so the Hub is safe for concurrent use.
func (h *Hub) Run(ctx context.Context) {
	defer h.closeAll()

	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.register:
			h.clients[c] = struct{}{}
			h.log.Info("ws client connected", "total", len(h.clients))
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
				h.log.Info("ws client disconnected", "total", len(h.clients))
			}
		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Slow consumer: drop it instead of blocking the pipeline.
					h.log.Warn("ws client send buffer full, disconnecting", "total", len(h.clients))
					delete(h.clients, c)
					close(c.send)
				}
			}
		}
	}
}

// Broadcast marshals v to JSON and queues it for all connected clients.
// It never blocks: if the queue is full the event is dropped.
func (h *Hub) Broadcast(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.log.Error("ws broadcast marshal failed", "err", err)
		return
	}
	select {
	case h.broadcast <- data:
	default:
		h.log.Warn("ws broadcast queue full, event dropped")
	}
}

func (h *Hub) closeAll() {
	for c := range h.clients {
		delete(h.clients, c)
		close(c.send)
	}
}

// ServeWS upgrades the HTTP connection and pumps messages for one client.
func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		hub.log.Warn("ws upgrade failed", "err", err)
		return
	}

	c := &wsClient{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, sendBufSize),
	}

	hub.register <- c
	go c.writePump()
	go c.readPump()
}

type wsClient struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

func (c *wsClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// checkOrigin allows non-browser clients (empty Origin) and localhost origins
// for local dev. Additional origins can be allowlisted via WS_ALLOWED_ORIGINS
// (comma-separated, exact match including scheme and port).
func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	if allowed := os.Getenv("WS_ALLOWED_ORIGINS"); allowed != "" {
		for _, o := range strings.Split(allowed, ",") {
			if strings.TrimSpace(o) == origin {
				return true
			}
		}
	}

	for _, prefix := range []string{
		"http://localhost:",
		"https://localhost:",
		"http://127.0.0.1:",
		"https://127.0.0.1:",
		"http://[::1]:",
		"https://[::1]:",
	} {
		if strings.HasPrefix(origin, prefix) {
			return true
		}
	}
	return false
}
