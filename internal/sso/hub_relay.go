package sso

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/alfred-identity/web/internal/eqlogin"
	"github.com/alfred-identity/web/internal/store"
)

const (
	maxRelayPayload      = 8192
	defaultLoginUpstream = "login.eqemulator.net:5998"
)

type udpRelay struct {
	mu       sync.Mutex
	conn     *net.UDPConn
	upstream *net.UDPAddr
	crcBytes byte
	crcKey   uint32
	cancel   context.CancelFunc
}

func (r *udpRelay) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	if r.conn != nil {
		_ = r.conn.Close()
		r.conn = nil
	}
}

func (c *wsClient) closeRelay() {
	if c == nil {
		return
	}
	c.relayMu.Lock()
	rel := c.relay
	c.relay = nil
	c.relayMu.Unlock()
	rel.close()
}

func (h *Hub) loginUpstreamAddr() string {
	if h != nil && h.LoginUpstream != "" {
		return h.LoginUpstream
	}
	return defaultLoginUpstream
}

func (h *Hub) relayFor(client *wsClient) (*udpRelay, error) {
	client.relayMu.Lock()
	if client.relay != nil && client.relay.conn != nil {
		rel := client.relay
		client.relayMu.Unlock()
		return rel, nil
	}
	client.relayMu.Unlock()

	up, err := net.ResolveUDPAddr("udp", h.loginUpstreamAddr())
	if err != nil {
		return nil, fmt.Errorf("resolve login upstream: %w", err)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("bind login relay: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rel := &udpRelay{conn: conn, upstream: up, cancel: cancel}

	client.relayMu.Lock()
	if client.relay != nil && client.relay.conn != nil {
		existing := client.relay
		client.relayMu.Unlock()
		cancel()
		_ = conn.Close()
		return existing, nil
	}
	client.relay = rel
	client.relayMu.Unlock()

	go h.relayReadLoop(ctx, client, rel)
	if h.Log != nil {
		h.Log.Info("login relay UDP started",
			"display_name", client.user.DisplayName,
			"user_id", client.user.ID,
			"discord_id", client.user.DiscordID,
			"upstream", up.String(),
			"local", conn.LocalAddr().String(),
		)
	}
	return rel, nil
}

func (h *Hub) relayReadLoop(ctx context.Context, client *wsClient, rel *udpRelay) {
	buf := make([]byte, 65535)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = rel.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := rel.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		pkt := append([]byte{}, buf[:n]...)
		rel.noteDownlink(pkt)
		_ = writeJSON(ctx, client.conn, client, map[string]any{
			"type":    "login_relay_down",
			"payload": base64.StdEncoding.EncodeToString(pkt),
		})
	}
}

func (r *udpRelay) noteDownlink(pkt []byte) {
	if resp, ok := eqlogin.ParseSessionResponse(pkt); ok {
		r.mu.Lock()
		r.crcBytes = resp.CRCBytes
		r.crcKey = resp.EncodeKey
		r.mu.Unlock()
	}
}

func (r *udpRelay) stripBody(pkt []byte) []byte {
	r.mu.Lock()
	crcBytes := r.crcBytes
	r.mu.Unlock()
	return eqlogin.StripForParse(pkt, crcBytes)
}

func (r *udpRelay) wrapSpliced(body []byte) []byte {
	r.mu.Lock()
	crcBytes := r.crcBytes
	crcKey := r.crcKey
	r.mu.Unlock()
	return eqlogin.RestoreCRC(body, crcKey, crcBytes)
}

func (h *Hub) handleLoginRelayUp(ctx context.Context, client *wsClient, user store.User, data []byte) {
	var msg struct {
		Type    string `json:"type"`
		Payload string `json:"payload"`
		Splice  bool   `json:"splice"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		_ = writeJSON(ctx, client.conn, client, map[string]any{
			"type": "login_relay_error", "message": "bad_request",
		})
		return
	}
	pkt, err := base64.StdEncoding.DecodeString(msg.Payload)
	if err != nil || len(pkt) == 0 || len(pkt) > maxRelayPayload {
		_ = writeJSON(ctx, client.conn, client, map[string]any{
			"type": "login_relay_error", "message": "bad_payload",
		})
		return
	}

	rel, err := h.relayFor(client)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("login relay start", "err", err, "user_id", user.ID)
		}
		_ = writeJSON(ctx, client.conn, client, map[string]any{
			"type": "login_relay_error", "message": "upstream_unavailable",
		})
		return
	}

	out := pkt
	if msg.Splice {
		body := rel.stripBody(pkt)
		spliced, dropReason, err := h.spliceLoginPacket(ctx, user, body)
		if err != nil {
			if h.Log != nil {
				h.Log.Error("eq login failed",
					"err", err,
					"display_name", user.DisplayName,
					"user_id", user.ID,
					"discord_id", user.DiscordID,
				)
			}
			_ = writeJSON(ctx, client.conn, client, map[string]any{
				"type": "login_relay_error", "message": "internal",
			})
			return
		}
		if dropReason != "" {
			if h.Log != nil {
				h.Log.Warn("eq login denied",
					"reason", dropReason,
					"display_name", user.DisplayName,
					"user_id", user.ID,
					"discord_id", user.DiscordID,
				)
			}
			return
		}
		if spliced != nil {
			out = rel.wrapSpliced(spliced)
		}
	}

	if _, err := rel.conn.WriteToUDP(out, rel.upstream); err != nil {
		if h.Log != nil {
			h.Log.Warn("login relay send failed", "err", err, "user_id", user.ID)
		}
	}
}

// spliceLoginPacket replaces alias credentials with vault credentials.
// dropReason != "" means the packet must not be forwarded (ACL / busy / rate limit).
// spliced==nil and dropReason=="" means the body was not a login packet; caller
// should forward the original datagram.
func (h *Hub) spliceLoginPacket(ctx context.Context, user store.User, body []byte) (spliced []byte, dropReason string, err error) {
	lp, ok := eqlogin.ParseLoginPacket(body)
	if !ok {
		return nil, "", nil
	}
	lim := h.limiterFor(user.ID)
	if !lim.Allow() {
		return nil, "rate_limited", nil
	}
	cands, err := h.Store.ResolveLoginCandidates(ctx, user, lp.Username)
	if err != nil || len(cands) == 0 {
		return nil, "not_found", nil
	}
	chosen := pickLoginCandidate(cands, h.Presence)
	if chosen == 0 {
		return nil, "all_busy", nil
	}
	realUser, password, err := h.Store.DecryptCredentials(ctx, chosen)
	if err != nil {
		return nil, "", err
	}
	out, err := lp.RewriteCredentials(realUser, password)
	password = ""
	if err != nil {
		return nil, "", err
	}
	if h.Presence != nil {
		h.Presence.ClearUserExcept(user.ID, chosen)
	}
	h.Store.AuditAccount(ctx, user.ID, chosen, "login_relay", lp.Username)
	h.broadcastFullState()
	if h.Log != nil {
		h.Log.Info("eq login",
			"display_name", user.DisplayName,
			"user_id", user.ID,
			"discord_id", user.DiscordID,
			"typed", lp.Username,
			"eq_username", realUser,
			"account_id", chosen,
		)
	}
	return out, "", nil
}
