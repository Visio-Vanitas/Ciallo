package mcproto

import (
	"bytes"
	"fmt"
	"io"
)

type Packet struct {
	ID      int32
	Data    []byte
	Payload []byte
	Raw     []byte
}

func ReadPacket(r io.Reader, maxLen int) (Packet, error) {
	length, lengthRaw, err := ReadVarInt(r)
	if err != nil {
		return Packet{}, err
	}
	if err := CheckPacketLength(length, maxLen); err != nil {
		return Packet{}, err
	}

	payload := make([]byte, int(length))
	if _, err := io.ReadFull(r, payload); err != nil {
		return Packet{}, err
	}

	id, idRaw, err := ReadVarInt(bytes.NewReader(payload))
	if err != nil {
		return Packet{}, fmt.Errorf("read packet id: %w", err)
	}
	dataOffset := len(idRaw)
	if dataOffset > len(payload) {
		return Packet{}, io.ErrUnexpectedEOF
	}

	raw := make([]byte, 0, len(lengthRaw)+len(payload))
	raw = append(raw, lengthRaw...)
	raw = append(raw, payload...)

	return Packet{
		ID:      id,
		Data:    append([]byte(nil), payload[dataOffset:]...),
		Payload: append([]byte(nil), payload...),
		Raw:     raw,
	}, nil
}

func WritePacket(w io.Writer, p Packet) error {
	if len(p.Raw) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	payload := p.Payload
	if len(payload) == 0 {
		payload = append(EncodeVarInt(p.ID), p.Data...)
	}
	if err := WriteVarInt(w, int32(len(payload))); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func NewPacket(id int32, data []byte) Packet {
	payload := append(EncodeVarInt(id), data...)
	raw := append(EncodeVarInt(int32(len(payload))), payload...)
	return Packet{
		ID:      id,
		Data:    append([]byte(nil), data...),
		Payload: payload,
		Raw:     raw,
	}
}
