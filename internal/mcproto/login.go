package mcproto

import (
	"bytes"
	"fmt"
)

const (
	LoginStartPacketID      int32 = 0x00
	LoginDisconnectPacketID int32 = 0x00
)

type LoginStart struct {
	Username string
}

func ParseLoginStart(p Packet, protocolVersion int32) (LoginStart, error) {
	if p.ID != LoginStartPacketID {
		return LoginStart{}, fmt.Errorf("expected login start packet id 0x00, got 0x%x", p.ID)
	}
	r := bytes.NewReader(p.Data)
	username, err := ReadString(r, 16)
	if err != nil {
		return LoginStart{}, err
	}
	return LoginStart{Username: username}, nil
}

func BuildLoginStart(username string, trailing []byte) Packet {
	data := EncodeString(username)
	data = append(data, trailing...)
	return NewPacket(LoginStartPacketID, data)
}

func BuildLoginDisconnect(reasonJSON string) Packet {
	return NewPacket(LoginDisconnectPacketID, EncodeString(reasonJSON))
}
