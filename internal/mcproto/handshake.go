package mcproto

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"unicode/utf8"
)

const HandshakePacketID int32 = 0x00

type NextState int32

const (
	NextStateStatus NextState = 1
	NextStateLogin  NextState = 2
)

type Handshake struct {
	ProtocolVersion int32
	ServerAddress   string
	ServerPort      uint16
	NextState       NextState
}

func ParseHandshake(p Packet) (Handshake, error) {
	if p.ID != HandshakePacketID {
		return Handshake{}, fmt.Errorf("expected handshake packet id 0x00, got 0x%x", p.ID)
	}

	r := bytes.NewReader(p.Data)
	protocolVersion, _, err := ReadVarInt(r)
	if err != nil {
		return Handshake{}, fmt.Errorf("protocol version: %w", err)
	}
	serverAddress, err := ReadString(r, 255)
	if err != nil {
		return Handshake{}, fmt.Errorf("server address: %w", err)
	}
	if r.Len() < 2 {
		return Handshake{}, fmt.Errorf("server port: short read")
	}
	portBytes := make([]byte, 2)
	if _, err := r.Read(portBytes); err != nil {
		return Handshake{}, fmt.Errorf("server port: %w", err)
	}
	serverPort := binary.BigEndian.Uint16(portBytes)
	nextState, _, err := ReadVarInt(r)
	if err != nil {
		return Handshake{}, fmt.Errorf("next state: %w", err)
	}

	return Handshake{
		ProtocolVersion: protocolVersion,
		ServerAddress:   serverAddress,
		ServerPort:      serverPort,
		NextState:       NextState(nextState),
	}, nil
}

func ReadString(r *bytes.Reader, maxChars int) (string, error) {
	length, _, err := ReadVarInt(r)
	if err != nil {
		return "", err
	}
	if length < 0 {
		return "", fmt.Errorf("negative string length %d", length)
	}
	if int(length) > r.Len() {
		return "", fmt.Errorf("string length %d exceeds remaining %d", length, r.Len())
	}
	if int(length) > maxChars*4 {
		return "", fmt.Errorf("string byte length %d exceeds max %d", length, maxChars*4)
	}
	raw := make([]byte, int(length))
	if _, err := r.Read(raw); err != nil {
		return "", err
	}
	if !utf8.Valid(raw) {
		return "", fmt.Errorf("string is not valid utf-8")
	}
	value := string(raw)
	if utf16CodeUnits(value) > maxChars {
		return "", fmt.Errorf("string character length exceeds max %d", maxChars)
	}
	return value, nil
}

func EncodeString(value string) []byte {
	raw := []byte(value)
	out := EncodeVarInt(int32(len(raw)))
	return append(out, raw...)
}

func NormalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if i := strings.IndexByte(host, 0); i >= 0 {
		host = host[:i]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

func utf16CodeUnits(value string) int {
	units := 0
	for _, r := range value {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return units
}

func BuildHandshake(protocolVersion int32, host string, port uint16, nextState NextState) Packet {
	data := make([]byte, 0, 16+len(host))
	data = append(data, EncodeVarInt(protocolVersion)...)
	data = append(data, EncodeString(host)...)
	portBuf := []byte{0, 0}
	binary.BigEndian.PutUint16(portBuf, port)
	data = append(data, portBuf...)
	data = append(data, EncodeVarInt(int32(nextState))...)
	return NewPacket(HandshakePacketID, data)
}
