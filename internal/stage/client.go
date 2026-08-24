package stage

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/local/aipool/internal/tensorwire"
)

type TCPClient struct {
	Address string
	Key     []byte
	Timeout time.Duration
	mu      sync.Mutex
	conn    net.Conn
}

func (c *TCPClient) Process(ctx context.Context, frame tensorwire.Frame) (tensorwire.Frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	if c.conn == nil {
		conn, err := (&net.Dialer{Timeout: c.Timeout, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", c.Address)
		if err != nil {
			return tensorwire.Frame{}, err
		}
		c.conn = conn
	}
	deadline := time.Now().Add(c.Timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = c.conn.SetDeadline(deadline)
	if err := tensorwire.WriteFrame(c.conn, c.Key, frame); err != nil {
		c.closeLocked()
		return tensorwire.Frame{}, err
	}
	output, err := tensorwire.ReadFrame(c.conn, c.Key)
	if err != nil {
		c.closeLocked()
		return tensorwire.Frame{}, err
	}
	if output.SessionID != frame.SessionID || output.Sequence != frame.Sequence || output.Position != frame.Position {
		c.closeLocked()
		return tensorwire.Frame{}, fmt.Errorf("stage returned a mismatched frame")
	}
	_ = c.conn.SetDeadline(time.Time{})
	return output, nil
}

func (c *TCPClient) Close() error { c.mu.Lock(); defer c.mu.Unlock(); return c.closeLocked() }
func (c *TCPClient) closeLocked() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}
