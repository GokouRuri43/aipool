package tensorwire

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

const (
	magic      = "AIPT"
	version    = 1
	headerSize = 40
	maxValues  = 16 << 20
)

type Frame struct {
	SessionID uint64
	Sequence  uint64
	Position  uint32
	Values    []float32
}

// WriteFrame emits a fixed binary header, float32 payload and HMAC. This is
// intentionally independent of HTTP/JSON so the future GPU backend can reuse
// the framing while replacing float32 with negotiated FP16/INT8 payloads.
func WriteFrame(w io.Writer, key []byte, frame Frame) error {
	if len(key) < 32 {
		return fmt.Errorf("tensor wire key must be at least 32 bytes")
	}
	if len(frame.Values) == 0 || len(frame.Values) > maxValues {
		return fmt.Errorf("invalid tensor value count")
	}
	header := make([]byte, headerSize)
	copy(header[:4], magic)
	header[4] = version
	header[5] = 1 // dtype float32
	binary.BigEndian.PutUint64(header[8:16], frame.SessionID)
	binary.BigEndian.PutUint64(header[16:24], frame.Sequence)
	binary.BigEndian.PutUint32(header[24:28], frame.Position)
	binary.BigEndian.PutUint32(header[28:32], uint32(len(frame.Values)))
	binary.BigEndian.PutUint32(header[32:36], uint32(len(frame.Values)*4))
	payload := make([]byte, len(frame.Values)*4)
	for i, value := range frame.Values {
		binary.BigEndian.PutUint32(payload[i*4:], math.Float32bits(value))
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(header)
	_, _ = mac.Write(payload)
	if err := writeFull(w, header); err != nil {
		return err
	}
	if err := writeFull(w, payload); err != nil {
		return err
	}
	return writeFull(w, mac.Sum(nil))
}

func ReadFrame(r io.Reader, key []byte) (Frame, error) {
	if len(key) < 32 {
		return Frame{}, fmt.Errorf("tensor wire key must be at least 32 bytes")
	}
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return Frame{}, err
	}
	if string(header[:4]) != magic || header[4] != version || header[5] != 1 {
		return Frame{}, fmt.Errorf("unsupported tensor wire frame")
	}
	count := binary.BigEndian.Uint32(header[28:32])
	size := binary.BigEndian.Uint32(header[32:36])
	if count == 0 || count > maxValues || size != count*4 {
		return Frame{}, fmt.Errorf("invalid tensor wire payload size")
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return Frame{}, err
	}
	signature := make([]byte, sha256.Size)
	if _, err := io.ReadFull(r, signature); err != nil {
		return Frame{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(header)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return Frame{}, fmt.Errorf("tensor wire authentication failed")
	}
	frame := Frame{SessionID: binary.BigEndian.Uint64(header[8:16]), Sequence: binary.BigEndian.Uint64(header[16:24]), Position: binary.BigEndian.Uint32(header[24:28]), Values: make([]float32, count)}
	for i := range frame.Values {
		frame.Values[i] = math.Float32frombits(binary.BigEndian.Uint32(payload[i*4:]))
	}
	return frame, nil
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		data = data[n:]
	}
	return nil
}
