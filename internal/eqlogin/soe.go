package eqlogin

import (
	"bytes"
	"encoding/binary"
)

// Transport opcodes (big-endian).
const (
	OpSessionRequest  = 0x0001
	OpSessionResponse = 0x0002
	OpCombined        = 0x0003
	OpDisconnect      = 0x0005
	OpKeepAlive       = 0x0006
	OpPacket          = 0x0009
	OpFragment        = 0x000D
	OpAck             = 0x0015
)

// EQ login application opcodes (EQEmu opcodes.conf, Titanium/SoF).
const (
	AppChatMessage        = 0x0016
	AppLoginAccepted      = 0x0017
	AppServerListResponse = 0x0018
)

type SessionResponse struct {
	ConnectCode   uint32
	EncodeKey     uint32
	CRCBytes      byte
	EncodePass1   byte
	EncodePass2   byte
	MaxPacketSize uint32
}

func TransportOpcode(data []byte) uint16 {
	if len(data) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(data[0:2])
}

func ParseSessionResponse(data []byte) (SessionResponse, bool) {
	if len(data) < 17 {
		return SessionResponse{}, false
	}
	if TransportOpcode(data) != OpSessionResponse {
		return SessionResponse{}, false
	}
	return SessionResponse{
		ConnectCode:   binary.BigEndian.Uint32(data[2:6]),
		EncodeKey:     binary.BigEndian.Uint32(data[6:10]),
		CRCBytes:      data[10],
		EncodePass1:   data[11],
		EncodePass2:   data[12],
		MaxPacketSize: binary.LittleEndian.Uint32(data[13:17]),
	}, true
}

func PacketUsesCRC(data []byte) bool {
	op := TransportOpcode(data)
	return op != OpSessionRequest && op != OpSessionResponse
}

// LooksLikeLoginFailure reports EQ login-server replies that mean the
// credentials were rejected (chat error, disconnect, or "invalid username").
func LooksLikeLoginFailure(pkt []byte) bool {
	if len(pkt) < 2 {
		return false
	}
	if TransportOpcode(pkt) == OpDisconnect {
		return true
	}
	lower := bytes.ToLower(pkt)
	if bytes.Contains(lower, []byte("invalid username")) || bytes.Contains(lower, []byte("invalid password")) {
		return true
	}
	return walkAppOpcodes(pkt, func(app uint16) bool {
		return app == AppChatMessage
	})
}

// LooksLikeLoginSuccess reports a server-list (or fragment of one), which
// means vault credentials already worked.
func LooksLikeLoginSuccess(pkt []byte) bool {
	if TransportOpcode(pkt) == OpFragment {
		return true
	}
	return walkAppOpcodes(pkt, func(app uint16) bool {
		return app == AppServerListResponse
	})
}

func walkAppOpcodes(pkt []byte, fn func(uint16) bool) bool {
	switch TransportOpcode(pkt) {
	case OpPacket:
		if len(pkt) >= 6 {
			return fn(binary.LittleEndian.Uint16(pkt[4:6]))
		}
	case OpCombined:
		i := 2
		for i < len(pkt) {
			n := int(pkt[i])
			i++
			if n <= 0 || i+n > len(pkt) {
				break
			}
			sub := pkt[i : i+n]
			i += n
			if walkAppOpcodes(sub, fn) {
				return true
			}
		}
	}
	return false
}
