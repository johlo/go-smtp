package smtp_test

import (
	"errors"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-smtp"
)

func hookSMTPConn(t *testing.T, s *smtp.Server) (*textproto.Conn, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	return textproto.NewConn(c), func() { c.Close(); s.Close(); <-done }
}
func hookSMTPReply(t *testing.T, c *textproto.Conn, command string, code int) string {
	t.Helper()
	if command != "" {
		if err := c.PrintfLine("%s", command); err != nil {
			t.Fatal(err)
		}
	}
	_, line, err := c.ReadResponse(code)
	if err != nil {
		t.Fatal(command, err)
	}
	return line
}
func hookSMTPClosed(t *testing.T, c *textproto.Conn) {
	t.Helper()
	line, err := c.ReadLine()
	if err == nil {
		t.Fatalf("expected closed connection, got %q", line)
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("socket did not close")
	}
}

func TestCommandAndResponseHooks(t *testing.T) {
	s := smtp.NewServer(&backend{})
	s.CommandHook = func(c *smtp.Conn, command, arg string) bool {
		c.HookData = command
		if command == "NOOP" {
			c.WriteReply(&smtp.Reply{Code: 450, Lines: []string{"command hook"}})
			return true
		}
		return false
	}
	s.ResponseHook = func(c *smtp.Conn, r *smtp.Reply) *smtp.Reply {
		if c.HookData != c.Command() {
			t.Errorf("hook context mismatch: %v / %s", c.HookData, c.Command())
		}
		if c.Command() == "NOOP" {
			t.Error("WriteReply invoked ResponseHook")
		}
		if c.Command() == "RSET" {
			r.Lines = []string{"response hook"}
		}
		return r
	}
	c, closeConn := hookSMTPConn(t, s)
	defer closeConn()
	hookSMTPReply(t, c, "", 220)
	hookSMTPReply(t, c, "EHLO localhost", 250)
	if got := hookSMTPReply(t, c, "NOOP", 450); !strings.Contains(got, "command hook") {
		t.Fatal(got)
	}
	if got := hookSMTPReply(t, c, "RSET", 250); !strings.Contains(got, "response hook") {
		t.Fatal(got)
	}
}

func TestGreetingHookAndHandledBDATClose(t *testing.T) {
	for _, command := range []string{"CONNECT", "BDAT"} {
		t.Run(command, func(t *testing.T) {
			s := smtp.NewServer(&backend{})
			s.CommandHook = func(c *smtp.Conn, name, arg string) bool {
				if name != command {
					return false
				}
				c.WriteReply(&smtp.Reply{Code: 421, Lines: []string{"unavailable"}})
				return true
			}
			c, closeConn := hookSMTPConn(t, s)
			defer closeConn()
			if command == "BDAT" {
				hookSMTPReply(t, c, "", 220)
				hookSMTPReply(t, c, "EHLO localhost", 250)
				hookSMTPReply(t, c, "MAIL FROM:<sender@example.test>", 250)
				hookSMTPReply(t, c, "RCPT TO:<recipient@example.test>", 250)
				c.PrintfLine("BDAT 6 LAST\r\nNOOP\r\nNOOP")
			}
			hookSMTPReply(t, c, "", 421)
			hookSMTPClosed(t, c)
		})
	}
}

func TestErrorHookHandlesDATAAndBDATWithoutDuplicateReplies(t *testing.T) {
	for _, command := range []string{"DATA", "BDAT"} {
		t.Run(command, func(t *testing.T) {
			failure := errors.New("injected backend failure")
			s := smtp.NewServer(&backend{dataErr: failure, dataErrOffset: 4})
			s.ErrorHook = func(c *smtp.Conn, err error) bool {
				if err != failure {
					return false
				}
				c.WriteReply(&smtp.Reply{Code: 451, Lines: []string{"handled backend failure"}})
				return true
			}
			c, closeConn := hookSMTPConn(t, s)
			defer closeConn()
			hookSMTPReply(t, c, "", 220)
			hookSMTPReply(t, c, "EHLO localhost", 250)
			hookSMTPReply(t, c, "MAIL FROM:<sender@example.test>", 250)
			hookSMTPReply(t, c, "RCPT TO:<recipient@example.test>", 250)
			if command == "DATA" {
				hookSMTPReply(t, c, "DATA", 354)
				c.PrintfLine("test\r\n.")
			} else {
				c.PrintfLine("BDAT 4 LAST")
				c.W.WriteString("test")
				c.W.Flush()
			}
			if got := hookSMTPReply(t, c, "", 451); !strings.Contains(got, "handled backend failure") {
				t.Fatal(got)
			}
			hookSMTPReply(t, c, "NOOP", 250)
		})
	}
}
