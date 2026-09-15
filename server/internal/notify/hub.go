package notify

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type Hint struct {
	Type          string `json:"type"`
	SyncEpoch     string `json:"sync_epoch"`
	HighWatermark string `json:"high_watermark"`
}

type Conn struct {
	WS        *websocket.Conn
	UserID    string
	DeviceID  string
	SessionID string
}

type Hub struct {
	mu    sync.Mutex
	conns map[string]map[*Conn]struct{}
}

func New() *Hub {
	return &Hub{conns: map[string]map[*Conn]struct{}{}}
}

func (h *Hub) Add(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[c.UserID] == nil {
		h.conns[c.UserID] = map[*Conn]struct{}{}
	}
	h.conns[c.UserID][c] = struct{}{}
}

func (h *Hub) Remove(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.conns[c.UserID]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.conns, c.UserID)
		}
	}
}

func (h *Hub) CloseUser(userID string) {
	h.mu.Lock()
	set := h.conns[userID]
	delete(h.conns, userID)
	h.mu.Unlock()
	for c := range set {
		_ = c.WS.Close(websocket.StatusGoingAway, "session ended")
	}
}

func (h *Hub) CloseDevice(userID, deviceID string) {
	h.mu.Lock()
	var list []*Conn
	for c := range h.conns[userID] {
		if c.DeviceID == deviceID {
			list = append(list, c)
			delete(h.conns[userID], c)
		}
	}
	h.mu.Unlock()
	for _, c := range list {
		_ = c.WS.Close(websocket.StatusGoingAway, "device revoked")
	}
}

func (h *Hub) Broadcast(userID, epoch, watermark string) {
	payload, err := json.Marshal(Hint{
		Type:          "changes_available",
		SyncEpoch:     epoch,
		HighWatermark: watermark,
	})
	if err != nil {
		return
	}
	h.mu.Lock()
	var list []*Conn
	for c := range h.conns[userID] {
		list = append(list, c)
	}
	h.mu.Unlock()
	for _, c := range list {
		c := c
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = c.WS.Write(ctx, websocket.MessageText, payload)
		}()
	}
}
