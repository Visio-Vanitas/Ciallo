package mcproto

import (
	"bytes"
	"testing"
)

func TestParseLoginStart(t *testing.T) {
	packet := BuildLoginStart("Steve", []byte{0x01, 0x02})
	got, err := ParseLoginStart(packet, 765)
	if err != nil {
		t.Fatalf("ParseLoginStart: %v", err)
	}
	if got.Username != "Steve" {
		t.Fatalf("username = %q", got.Username)
	}
	read, err := ReadPacket(bytes.NewReader(packet.Raw), 64*1024)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if !bytes.Equal(read.Raw, packet.Raw) {
		t.Fatal("raw packet not preserved")
	}
}

func TestParseLoginStartRejectsWrongPacketID(t *testing.T) {
	_, err := ParseLoginStart(NewPacket(0x01, EncodeString("Steve")), 765)
	if err == nil {
		t.Fatal("expected packet id error")
	}
}

func TestParseLoginStartRejectsLongUsername(t *testing.T) {
	_, err := ParseLoginStart(BuildLoginStart("abcdefghijklmnopq", nil), 765)
	if err == nil {
		t.Fatal("expected username length error")
	}
}
