package smtp

import (
	"net"
	"testing"
	"time"
)

func TestWriteRawHonorsTimeout(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	s := NewServer(nil)
	s.WriteTimeout = 30 * time.Millisecond
	c := newConn(serverConn, s)
	err := c.WriteRaw("299 blocked response\r\n")
	if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatalf("expected write timeout, got %v", err)
	}
}
