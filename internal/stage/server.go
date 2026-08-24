package stage

import (
	"bufio"
	"fmt"
	"net"
	"time"

	"github.com/local/aipool/internal/tensorwire"
)

type Server struct {
	Runtime *Runtime
	Key     []byte
}

func (s *Server) Serve(listener net.Listener) error {
	if s.Runtime == nil || len(s.Key) < 32 {
		return fmt.Errorf("runtime and 32-byte stage key are required")
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	reader := bufio.NewReader(conn)
	for {
		frame, err := tensorwire.ReadFrame(reader, s.Key)
		if err != nil {
			return
		}
		output, err := s.Runtime.Process(frame)
		if err != nil {
			return
		}
		if tensorwire.WriteFrame(conn, s.Key, output) != nil {
			return
		}
	}
}
