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
	mu             sync.Mutex
	conn           *net.UDPConn
	upstream       *net.UDPAddr
	crcBytes       byte
	crcKey         uint32
	cancel         context.CancelFunc
	typedFallback  []byte
	typedRetryUsed bool
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
		if eqlogin.LooksLikeLoginFailure(pkt) {
			if retry := rel.takeTypedRetry(); retry != nil {
				if h.Log != nil {
					h.Log.Info("eq login retrying typed credentials",
						"display_name", client.user.DisplayName,
						"user_id", client.user.ID,
						"discord_id", client.user.DiscordID,
					)
				}
				if _, err := rel.conn.WriteToUDP(retry, rel.upstream); err != nil && h.Log != nil {
					h.Log.Warn("eq login typed retry send failed", "err", err, "user_id", client.user.ID)
				}
				continue
			}
		}
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
	if eqlogin.LooksLikeLoginSuccess(pkt) {
		r.clearTypedFallback()
	}
}

func (r *udpRelay) setTypedFallback(pkt []byte) {
	if r == nil || len(pkt) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.typedFallback = append([]byte{}, pkt...)
	r.typedRetryUsed = false
}

func (r *udpRelay) takeTypedRetry() []byte {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.typedRetryUsed || len(r.typedFallback) == 0 {
		return nil
	}
	r.typedRetryUsed = true
	out := r.typedFallback
	r.typedFallback = nil
	return out
}

func (r *udpRelay) clearTypedFallback() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.typedFallback = nil
	r.typedRetryUsed = true
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
		spliced, dropReason, retryTyped, err := h.spliceLoginPacket(ctx, user, body)
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
			if retryTyped {
				rel.setTypedFallback(pkt)
			}
		}
	}

	if _, err := rel.conn.WriteToUDP(out, rel.upstream); err != nil {
		if h.Log != nil {
			h.Log.Warn("login relay send failed", "err", err, "user_id", user.ID)
		}
	}
}

func (h *Hub) handleLoginSplice(ctx context.Context, client *wsClient, user store.User, data []byte) {
	var msg struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Payload   string `json:"payload"`
	}
	reply := func(payload []byte, errMsg string) {
		out := map[string]any{
			"type":       "login_splice_result",
			"request_id": msg.RequestID,
		}
		if errMsg != "" {
			out["error"] = errMsg
		}
		if len(payload) > 0 {
			out["payload"] = base64.StdEncoding.EncodeToString(payload)
		}
		_ = writeJSON(ctx, client.conn, client, out)
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		reply(nil, "bad_request")
		return
	}
	pkt, err := base64.StdEncoding.DecodeString(msg.Payload)
	if err != nil || len(pkt) == 0 || len(pkt) > maxRelayPayload {
		reply(nil, "bad_payload")
		return
	}
	spliced, dropReason, _, err := h.spliceLoginPacket(ctx, user, pkt)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("eq login splice failed",
				"err", err,
				"display_name", user.DisplayName,
				"user_id", user.ID,
				"discord_id", user.DiscordID,
			)
		}
		reply(nil, "internal")
		return
	}
	if dropReason != "" {
		if h.Log != nil {
			h.Log.Warn("eq login splice denied",
				"reason", dropReason,
				"display_name", user.DisplayName,
				"user_id", user.ID,
				"discord_id", user.DiscordID,
			)
		}
		reply(nil, dropReason)
		return
	}
	out := pkt
	if spliced != nil {
		out = spliced
	}
	reply(out, "")
}

// spliceLoginPacket replaces alias credentials with vault credentials.
// dropReason != "" means the packet must not be forwarded (ACL / busy / rate limit).
// spliced==nil and dropReason=="" means forward the original datagram (not a
// login packet, or no vault match — try the typed username/password).
// retryTyped is true when vault credentials were sent and differ from what
// the client typed, so a login-server failure should retry the original packet.
func (h *Hub) spliceLoginPacket(ctx context.Context, user store.User, body []byte) (spliced []byte, dropReason string, retryTyped bool, err error) {
	lp, ok := eqlogin.ParseLoginPacket(body)
	if !ok {
		return nil, "", false, nil
	}
	lim := h.limiterFor(user.ID)
	if !lim.Allow() {
		return nil, "rate_limited", false, nil
	}
	cands, err := h.Store.ResolveLoginCandidates(ctx, user, lp.Username)
	if err != nil || len(cands) == 0 {
		if h.Log != nil {
			h.Log.Info("eq login using typed credentials",
				"reason", "not_found",
				"display_name", user.DisplayName,
				"user_id", user.ID,
				"discord_id", user.DiscordID,
				"typed", lp.Username,
			)
		}
		return nil, "", false, nil
	}
	chosen := pickLoginCandidate(cands, h.Presence)
	if chosen == 0 {
		return nil, "all_busy", false, nil
	}
	realUser, password, err := h.Store.DecryptCredentials(ctx, chosen)
	if err != nil {
		return nil, "", false, err
	}
	retryTyped = lp.Password != password
	out, err := lp.RewriteCredentials(realUser, password)
	password = ""
	if err != nil {
		return nil, "", false, err
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
			"typed_fallback", retryTyped,
		)
	}
	return out, "", retryTyped, nil
}
