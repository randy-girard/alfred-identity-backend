package eqlogin

import "encoding/binary"

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
