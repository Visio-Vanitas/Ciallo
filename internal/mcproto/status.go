package mcproto

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

const (
	StatusRequestPacketID  int32 = 0x00
	StatusResponsePacketID int32 = 0x00
	StatusPingPacketID     int32 = 0x01
	StatusPongPacketID     int32 = 0x01
)

func BuildStatusResponse(json string) Packet {
	return NewPacket(StatusResponsePacketID, EncodeString(json))
}

func BuildPong(payload []byte) (Packet, error) {
	if len(payload) != 8 {
		return Packet{}, fmt.Errorf("ping payload must be 8 bytes, got %d", len(payload))
	}
	data := make([]byte, 8)
	copy(data, payload)
	return NewPacket(StatusPongPacketID, data), nil
}

func BuildPing(value int64) Packet {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, uint64(value))
	return NewPacket(StatusPingPacketID, data)
}

func ParseStatusJSON(p Packet) (string, error) {
	if p.ID != StatusResponsePacketID {
		return "", fmt.Errorf("expected status response packet id 0x00, got 0x%x", p.ID)
	}
	r := bytes.NewReader(p.Data)
	return ReadString(r, 32767)
}

func ExtractMOTD(statusJSON string) (json.RawMessage, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(statusJSON), &root); err != nil {
		return nil, false
	}
	description, ok := root["description"]
	if !ok || len(description) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), description...), true
}

func BuildFallbackStatus(protocolVersion int32, motd json.RawMessage) (Packet, error) {
	if len(motd) == 0 {
		motd = json.RawMessage(`{"text":"Server status temporarily unavailable"}`)
	}
	root := map[string]any{
		"version": map[string]any{
			"name":     "ciallo fallback",
			"protocol": protocolVersion,
		},
		"players": map[string]any{
			"max":    0,
			"online": 0,
		},
		"description": json.RawMessage(append([]byte(nil), motd...)),
	}
	data, err := json.Marshal(root)
	if err != nil {
		return Packet{}, err
	}
	return BuildStatusResponse(string(data)), nil
}
