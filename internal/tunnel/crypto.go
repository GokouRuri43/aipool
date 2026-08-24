package tunnel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const maxEncryptedRecord = 64 << 10

type secureConn struct {
	net.Conn
	writeAEAD    cipher.AEAD
	readAEAD     cipher.AEAD
	writeAAD     []byte
	readAAD      []byte
	writeCounter uint64
	readCounter  uint64
	readBuffer   []byte
	writeMu      sync.Mutex
}

func wrapSecure(conn net.Conn, encodedKey, pairID, streamID, role string) (net.Conn, error) {
	secret, err := base64.RawURLEncoding.DecodeString(encodedKey)
	if err != nil || len(secret) < 32 {
		return nil, fmt.Errorf("tunnel key must be at least 32 base64url bytes")
	}
	peer := peerRole(role)
	if peer == "" {
		return nil, fmt.Errorf("invalid tunnel role")
	}
	writeLabel := role + ">" + peer
	readLabel := peer + ">" + role
	writeAEAD, err := makeAEAD(secret, pairID, streamID, writeLabel)
	if err != nil {
		return nil, err
	}
	readAEAD, err := makeAEAD(secret, pairID, streamID, readLabel)
	if err != nil {
		return nil, err
	}
	return &secureConn{
		Conn: conn, writeAEAD: writeAEAD, readAEAD: readAEAD,
		writeAAD: []byte(pairID + "|" + streamID + "|" + writeLabel),
		readAAD:  []byte(pairID + "|" + streamID + "|" + readLabel),
	}, nil
}

func makeAEAD(secret []byte, pairID, streamID, direction string) (cipher.AEAD, error) {
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write([]byte("aipool-tunnel-v1|" + pairID + "|" + streamID + "|" + direction))
	block, err := aes.NewCipher(h.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (c *secureConn) Write(data []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	for len(data) > 0 {
		size := min(len(data), maxEncryptedRecord)
		plain := data[:size]
		nonce := counterNonce(c.writeAEAD.NonceSize(), c.writeCounter)
		sealed := c.writeAEAD.Seal(nil, nonce, plain, c.writeAAD)
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(sealed)))
		if err := writeFull(c.Conn, header[:]); err != nil {
			return written, err
		}
		if err := writeFull(c.Conn, sealed); err != nil {
			return written, err
		}
		c.writeCounter++
		written += size
		data = data[size:]
	}
	return written, nil
}

func (c *secureConn) Read(data []byte) (int, error) {
	if len(c.readBuffer) > 0 {
		n := copy(data, c.readBuffer)
		c.readBuffer = c.readBuffer[n:]
		return n, nil
	}
	var header [4]byte
	if _, err := io.ReadFull(c.Conn, header[:]); err != nil {
		return 0, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > maxEncryptedRecord+uint32(c.readAEAD.Overhead()) {
		return 0, fmt.Errorf("invalid encrypted tunnel record size")
	}
	sealed := make([]byte, size)
	if _, err := io.ReadFull(c.Conn, sealed); err != nil {
		return 0, err
	}
	nonce := counterNonce(c.readAEAD.NonceSize(), c.readCounter)
	plain, err := c.readAEAD.Open(nil, nonce, sealed, c.readAAD)
	if err != nil {
		return 0, fmt.Errorf("tunnel record authentication failed: %w", err)
	}
	c.readCounter++
	n := copy(data, plain)
	c.readBuffer = append(c.readBuffer[:0], plain[n:]...)
	return n, nil
}

func counterNonce(size int, counter uint64) []byte {
	nonce := make([]byte, size)
	binary.BigEndian.PutUint64(nonce[size-8:], counter)
	return nonce
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

func (c *secureConn) SetDeadline(t time.Time) error { return c.Conn.SetDeadline(t) }
