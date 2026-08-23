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
		if _, _, err := c.Read(r.Context()); err != nil {
			return
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for c, u := range conns {
		st, err := s.buildState(ctx, u)
		if err != nil {
			continue
		}
		st["type"] = "state"
		msg, err := json.Marshal(st)
		if err != nil {
			continue
		}
		wctx, c2 := context.WithTimeout(ctx, 3*time.Second)
		if err := c.Write(wctx, websocket.MessageText, msg); err != nil {
			s.liveMu.Lock()
			delete(s.live, c)
			s.liveMu.Unlock()
			_ = c.Close(websocket.StatusGoingAway, "write failed")
		}
		c2()
	}
}
