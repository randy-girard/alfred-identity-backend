package eqlogin

import "encoding/binary"

// SOECRC32 matches p99-login-proxy / EQEmu wire format.
func SOECRC32(data []byte, key uint32) uint32 {
	table := crcTable()
	crc := uint32(0xFFFFFFFF)
	keyBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(keyBytes, key)
	for _, b := range append(keyBytes, data...) {
		idx := (crc ^ uint32(b)) & 0xFF
		crc = table[idx] ^ (crc >> 8)
	}
	return ^crc
}

func AppendCRC(packet []byte, key uint32, crcBytes byte) []byte {
	if crcBytes == 0 {
		out := make([]byte, len(packet))
		copy(out, packet)
		return out
	}
	crc := SOECRC32(packet, key)
	out := append([]byte{}, packet...)
	if crcBytes == 2 {
		out = append(out, byte(crc>>8), byte(crc))
	} else {
		out = append(out, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	}
	return out
}

func StripCRC(packet []byte, crcBytes byte) []byte {
	if crcBytes == 0 || len(packet) < int(crcBytes) {
		return packet
	}
	return packet[:len(packet)-int(crcBytes)]
}

func crcTable() *[256]uint32 {
	var table [256]uint32
	for i := range table {
		crc := uint32(i)
		for j := 0; j < 8; j++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
		table[i] = crc
	}
	return &table
}
