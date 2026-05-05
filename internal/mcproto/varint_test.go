package mcproto

import (
	"bytes"
	"errors"
	"testing"
)

func TestVarIntRoundTrip(t *testing.T) {
	values := []int32{0, 1, 2, 127, 128, 255, 2147483647, -1, -2147483648}
	for _, value := range values {
		encoded := EncodeVarInt(value)
		got, raw, err := ReadVarInt(bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("ReadVarInt(%d): %v", value, err)
		}
		if got != value {
			t.Fatalf("ReadVarInt(%d) = %d", value, got)
		}
		if !bytes.Equal(raw, encoded) {
			t.Fatalf("raw bytes mismatch for %d", value)
		}
	}
}

func TestVarIntTooLong(t *testing.T) {
	_, _, err := ReadVarInt(bytes.NewReader([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x00}))
	if !errors.Is(err, ErrVarIntTooLong) {
		t.Fatalf("expected ErrVarIntTooLong, got %v", err)
	}
}

func TestPacketLengthLimit(t *testing.T) {
	packet := NewPacket(0, []byte("hello"))
	_, err := ReadPacket(bytes.NewReader(packet.Raw), 2)
	if err == nil {
		t.Fatal("expected packet length error")
	}
}

func TestReadPacketAcceptsConfiguredLargePacket(t *testing.T) {
	data := bytes.Repeat([]byte("x"), MaxPacketLength)
	packet := NewPacket(0, data)
	if VarIntSize(int32(len(packet.Payload))) <= 3 {
		t.Fatalf("test packet length varint is not large enough: payload=%d", len(packet.Payload))
	}
	got, err := ReadPacket(bytes.NewReader(packet.Raw), len(packet.Payload))
	if err != nil {
		t.Fatalf("ReadPacket large configured packet: %v", err)
	}
	if len(got.Data) != len(data) {
		t.Fatalf("data len = %d, want %d", len(got.Data), len(data))
	}
}
