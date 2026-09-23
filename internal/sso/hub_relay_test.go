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
	spliced, drop, err := h.spliceLoginPacket(context.Background(), store.User{}, []byte{0x00, eqlogin.OpKeepAlive})
	if err != nil || drop || spliced != nil {
		t.Fatalf("spliced=%v drop=%v err=%v", spliced, drop, err)
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
