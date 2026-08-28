package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/alfred-identity/web/internal/store"
	"github.com/coder/websocket"
)

func (s *Server) handleLiveWS(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessionFromRequest(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	u, err := s.store.UserByID(ctx, sess.UserID)
	cancel()
	if err != nil || u.AccessRevoked {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	accessCtx, accessCancel := context.WithTimeout(r.Context(), 5*time.Second)
	ok := s.canAccessWeb(accessCtx, u)
	accessCancel()
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	s.liveMu.Lock()
	s.live[c] = u
	s.liveMu.Unlock()
	defer func() {
		s.liveMu.Lock()
		delete(s.live, c)
		s.liveMu.Unlock()
		_ = c.Close(websocket.StatusNormalClosure, "")
	}()

	s.pushStateTo(r.Context(), c, u)

	// Keep connection open; server pushes on Hub broadcasts. Read to detect close.
	for {
		_, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		var tip struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &tip) != nil {
			continue
		}
		switch tip.Type {
		case "ping":
			wctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			_ = c.Write(wctx, websocket.MessageText, []byte(`{"type":"pong"}`))
			cancel()
		case "pong":
		}
	}
}

func (s *Server) pushStateTo(ctx context.Context, c *websocket.Conn, u store.User) {
	stCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := s.buildState(stCtx, u)
	if err != nil {
		return
	}
	st["type"] = "state"
	msg, err := json.Marshal(st)
	if err != nil {
		return
	}
	wctx, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	_ = c.Write(wctx, websocket.MessageText, msg)
}

func (s *Server) broadcastLiveState() {
	s.liveMu.Lock()
	conns := make(map[*websocket.Conn]store.User, len(s.live))
	for c, u := range s.live {
		conns[c] = u
	}
	s.liveMu.Unlock()
	if len(conns) == 0 {
		return
	}

	// Group tabs by user so we build filtered state once per user, not once per
	// socket. A shared deadline across all builds (previous behavior after
	// per-user filtering) could starve later writes and drop live sockets.
	byUser := map[int64][]*websocket.Conn{}
	users := map[int64]store.User{}
	for c, u := range conns {
		byUser[u.ID] = append(byUser[u.ID], c)
		users[u.ID] = u
	}

	for uid, sockets := range byUser {
		u := users[uid]
		stCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		st, err := s.buildState(stCtx, u)
		cancel()
		if err != nil {
			continue
		}
		st["type"] = "state"
		msg, err := json.Marshal(st)
		if err != nil {
			continue
		}
		for _, c := range sockets {
			wctx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
			if err := c.Write(wctx, websocket.MessageText, msg); err != nil {
				s.liveMu.Lock()
				delete(s.live, c)
				s.liveMu.Unlock()
				_ = c.Close(websocket.StatusGoingAway, "write failed")
			}
			c2()
		}
	}
}
