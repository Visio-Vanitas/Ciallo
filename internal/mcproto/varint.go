package mcproto

import (
	"errors"
	"fmt"
	"io"
)

const MaxVarIntBytes = 5
const MaxPacketLength = 1<<21 - 1

var ErrVarIntTooLong = errors.New("varint is too long")

func ReadVarInt(r io.Reader) (int32, []byte, error) {
	var numRead int
	var result int32
	raw := make([]byte, 0, MaxVarIntBytes)
	buf := []byte{0}

	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, raw, err
		}
		b := buf[0]
		raw = append(raw, b)
		value := int32(b & 0x7F)
		result |= value << (7 * numRead)

		numRead++
		if numRead > MaxVarIntBytes {
			return 0, raw, ErrVarIntTooLong
		}
		if b&0x80 == 0 {
			return result, raw, nil
		}
	}
}

func WriteVarInt(w io.Writer, value int32) error {
	_, err := w.Write(EncodeVarInt(value))
	return err
}

func EncodeVarInt(value int32) []byte {
	u := uint32(value)
	out := make([]byte, 0, MaxVarIntBytes)
	for {
		if u&^uint32(0x7F) == 0 {
			out = append(out, byte(u))
			return out
		}
		out = append(out, byte(u&0x7F|0x80))
		u >>= 7
	}
}

func VarIntSize(value int32) int {
	return len(EncodeVarInt(value))
}

func CheckPacketLength(length int32, maxLen int) error {
	if length < 0 {
		return fmt.Errorf("negative packet length %d", length)
	}
	if int(length) > maxLen {
		return fmt.Errorf("packet length %d exceeds max %d", length, maxLen)
	}
	return nil
}
