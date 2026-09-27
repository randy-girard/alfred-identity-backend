package sso

import (
	"context"
	"encoding/base64"
	"net"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/crypto"
	"github.com/alfred-identity/web/internal/eqlogin"
	"github.com/alfred-identity/web/internal/presence"
	"github.com/alfred-identity/web/internal/store"
)

func combinedLogin(t *testing.T, user, pass string) []byte {
	t.Helper()
	base := []byte{
		0x00, 0x03,
		0x04, 0x00, 0x15, 0x00, 0x00,
		0x20, 0x00, 0x09, 0x00, 0x01,
		0x02, 0x00,
		0x03, 0x00, 0x00, 0x00,
		0x00,
		0x02,
		0x00, 0x00, 0x00, 0x00,
	}
	base = append(base, crypto.GoldenCipherBytes()...)
	lp, ok := eqlogin.ParseLoginPacket(base)
	if !ok {
		t.Fatal("parse golden")
	}
	out, err := lp.RewriteCredentials(user, pass)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSpliceLoginPacketIgnoresNonLogin(t *testing.T) {
	h := &Hub{}
	spliced, dropReason, retryTyped, err := h.spliceLoginPacket(context.Background(), store.User{}, []byte{0x00, eqlogin.OpKeepAlive})
	if err != nil || dropReason != "" || spliced != nil || retryTyped {
		t.Fatalf("spliced=%v drop=%q retry=%v err=%v", spliced, dropReason, retryTyped, err)
	}
}

func TestHubLoginRelaySplicesToUpstream(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	u, err := st.UpsertUser(ctxBG, "relay-"+randHex(4), "Relay User", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	realUser := "realbox_" + randHex(4)
	acctID, err := st.AddEQAccount(ctxBG, realUser, "vaultpass", "")
	if err != nil {
		t.Fatal(err)
	}
	alias := "tank_" + randHex(3)
	if err := st.AddAlias(ctxBG, alias, acctID); err != nil {
		t.Fatal(err)
	}

	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)
		_ = ln.SetReadDeadline(time.Now().Add(8 * time.Second))
		n, _, err := ln.ReadFromUDP(buf)
		if err != nil {
			return
		}
		got <- append([]byte{}, buf[:n]...)
	}()

	h := &Hub{
		Store:           st,
		Presence:        presence.New(time.Minute),
		ProtocolVersion: DefaultProtocolVersion,
		LoginUpstream:   ln.LocalAddr().String(),
	}
	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	authHub(t, ctx, conn, raw)

	pkt := combinedLogin(t, alias, "junk-password")
	writeWS(t, ctx, conn, map[string]any{
		"type":    "login_relay_up",
		"payload": base64.StdEncoding.EncodeToString(pkt),
		"splice":  true,
	})

	var wire []byte
	select {
	case wire = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for spliced UDP")
	}
	lp, ok := eqlogin.ParseLoginPacket(wire)
	if !ok {
		t.Fatalf("upstream packet was not a login: %x", wire)
	}
	if lp.Username != realUser || lp.Password != "vaultpass" {
		t.Fatalf("upstream creds user=%q pass=%q want %q/vaultpass", lp.Username, lp.Password, realUser)
	}
}

func TestUdpRelayTypedRetryOnce(t *testing.T) {
	r := &udpRelay{}
	if r.takeTypedRetry() != nil {
		t.Fatal("empty fallback")
	}
	r.setTypedFallback([]byte{1, 2, 3})
	got := r.takeTypedRetry()
	if string(got) != "\x01\x02\x03" {
		t.Fatalf("%v", got)
	}
	if r.takeTypedRetry() != nil {
		t.Fatal("retry must be single-shot")
	}
	r.setTypedFallback([]byte{9})
	r.clearTypedFallback()
	if r.takeTypedRetry() != nil {
		t.Fatal("cleared fallback")
	}
}

func TestHubLoginRelayUnknownForwardsTyped(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	u, err := st.UpsertUser(ctxBG, "relay-nf-"+randHex(4), "Relay User", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)
		_ = ln.SetReadDeadline(time.Now().Add(8 * time.Second))
		n, _, err := ln.ReadFromUDP(buf)
		if err != nil {
			return
		}
		got <- append([]byte{}, buf[:n]...)
	}()

	h := &Hub{
		Store:           st,
		Presence:        presence.New(time.Minute),
		ProtocolVersion: DefaultProtocolVersion,
		LoginUpstream:   ln.LocalAddr().String(),
	}
	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	authHub(t, ctx, conn, raw)

	pkt := combinedLogin(t, "not-in-vault", "real-eq-pass")
	writeWS(t, ctx, conn, map[string]any{
		"type":    "login_relay_up",
		"payload": base64.StdEncoding.EncodeToString(pkt),
		"splice":  true,
	})

	var wire []byte
	select {
	case wire = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for typed UDP")
	}
	lp, ok := eqlogin.ParseLoginPacket(wire)
	if !ok {
		t.Fatalf("upstream packet was not a login: %x", wire)
	}
	if lp.Username != "not-in-vault" || lp.Password != "real-eq-pass" {
		t.Fatalf("expected typed creds, got user=%q pass=%q", lp.Username, lp.Password)
	}
}

func TestHubLoginRelayRetriesTypedOnLoginFailure(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	u, err := st.UpsertUser(ctxBG, "relay-fb-"+randHex(4), "Relay User", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	realUser := "realbox_" + randHex(4)
	acctID, err := st.AddEQAccount(ctxBG, realUser, "vaultpass", "")
	if err != nil {
		t.Fatal(err)
	}
	alias := "tank_" + randHex(3)
	if err := st.AddAlias(ctxBG, alias, acctID); err != nil {
		t.Fatal(err)
	}

	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan []byte, 2)
	go func() {
		buf := make([]byte, 65535)
		_ = ln.SetReadDeadline(time.Now().Add(8 * time.Second))
		n, src, err := ln.ReadFromUDP(buf)
		if err != nil {
			return
		}
		got <- append([]byte{}, buf[:n]...)
		fail := []byte{0x00, eqlogin.OpPacket, 0x00, 0x00, byte(eqlogin.AppChatMessage), 0x00}
		fail = append(fail, []byte("Invalid username or password")...)
		_, _ = ln.WriteToUDP(fail, src)
		_ = ln.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _, err = ln.ReadFromUDP(buf)
		if err != nil {
			return
		}
		got <- append([]byte{}, buf[:n]...)
	}()

	h := &Hub{
		Store:           st,
		Presence:        presence.New(time.Minute),
		ProtocolVersion: DefaultProtocolVersion,
		LoginUpstream:   ln.LocalAddr().String(),
	}
	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	authHub(t, ctx, conn, raw)

	pkt := combinedLogin(t, alias, "real-eq-pass")
	writeWS(t, ctx, conn, map[string]any{
		"type":    "login_relay_up",
		"payload": base64.StdEncoding.EncodeToString(pkt),
		"splice":  true,
	})

	var first []byte
	select {
	case first = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for vault UDP")
	}
	lp, ok := eqlogin.ParseLoginPacket(first)
	if !ok || lp.Username != realUser || lp.Password != "vaultpass" {
		t.Fatalf("first packet vault creds user=%q pass=%q", lp.Username, lp.Password)
	}

	var second []byte
	select {
	case second = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for typed retry UDP")
	}
	lp2, ok := eqlogin.ParseLoginPacket(second)
	if !ok || lp2.Username != alias || lp2.Password != "real-eq-pass" {
		t.Fatalf("retry typed user=%q pass=%q want %q/real-eq-pass", lp2.Username, lp2.Password, alias)
	}
}

func TestHubLoginSpliceReturnsVaultPacket(t *testing.T) {
	st := openHubTestStore(t)
	ctxBG := context.Background()
	u, err := st.UpsertUser(ctxBG, "splice-"+randHex(4), "Splice User", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := st.CreateToken(ctxBG, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	realUser := "realbox_" + randHex(4)
	acctID, err := st.AddEQAccount(ctxBG, realUser, "vaultpass", "")
	if err != nil {
		t.Fatal(err)
	}
	alias := "tank_" + randHex(3)
	if err := st.AddAlias(ctxBG, alias, acctID); err != nil {
		t.Fatal(err)
	}

	h := &Hub{
		Store:           st,
		Presence:        presence.New(time.Minute),
		ProtocolVersion: DefaultProtocolVersion,
	}
	conn, ctx, cancel := dialHub(t, h)
	defer cancel()
	authHub(t, ctx, conn, raw)

	pkt := combinedLogin(t, alias, "junk-password")
	writeWS(t, ctx, conn, map[string]any{
		"type":       "login_splice",
		"request_id": "spl-1",
		"payload":    base64.StdEncoding.EncodeToString(pkt),
	})
	msg := readWSUntil(t, ctx, conn, "login_splice_result")
	if errStr, _ := msg["error"].(string); errStr != "" {
		t.Fatalf("splice error=%s", errStr)
	}
	payload, _ := msg["payload"].(string)
	wire, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	lp, ok := eqlogin.ParseLoginPacket(wire)
	if !ok {
		t.Fatalf("result was not a login: %x", wire)
	}
	if lp.Username != realUser || lp.Password != "vaultpass" {
		t.Fatalf("spliced creds user=%q pass=%q want %q/vaultpass", lp.Username, lp.Password, realUser)
	}
}
