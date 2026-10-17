package ipc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const MaxMessageSize = 1 << 20 // 1 MiB (native messaging host→browser limit)

// ReadFrame reads a 4-byte little-endian length-prefixed JSON message.
func ReadFrame(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(lenBuf[:])
	if n == 0 {
		return nil, fmt.Errorf("empty frame")
	}
	if n > MaxMessageSize {
		return nil, fmt.Errorf("frame too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// WriteFrame writes a 4-byte LE length-prefixed message.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxMessageSize {
		return fmt.Errorf("payload too large: %d", len(payload))
	}
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func ReadJSON(r io.Reader, v interface{}) error {
	b, err := ReadFrame(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func WriteJSON(w io.Writer, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return WriteFrame(w, b)
}
