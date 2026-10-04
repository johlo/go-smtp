package smtp_test

import (
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johlo/go-smtp"
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
	s.CommandHook = func(c *smtp.Conn, command, arg string) error {
		if c.HookData != nil {
			t.Error("stale hook data")
		}
		c.HookData = command
		if command == "NOOP" {
			c.WriteReply(&smtp.Reply{Code: 450, Lines: []string{"command hook"}})
			return smtp.ErrResponseHandled
		}
		return nil
	}
	s.ResponseHook = func(c *smtp.Conn, r *smtp.Reply) *smtp.Reply {
		if c.Command() != "" && c.HookData != c.Command() {
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

func TestGreetingHookAndExplicitDisconnect(t *testing.T) {
	for _, reply := range []*smtp.Reply{nil, {Code: 421, Lines: []string{"unavailable"}}} {
		s := smtp.NewServer(&backend{})
		s.GreetingHook = func(c *smtp.Conn) error {
			if c.Command() != "" {
				t.Error("greeting masqueraded as a command")
			}
			return c.Disconnect(reply)
		}
		s.CommandHook = func(_ *smtp.Conn, _, _ string) error { t.Error("command hook ran for greeting"); return nil }
		c, closeConn := hookSMTPConn(t, s)
		if reply != nil {
			hookSMTPReply(t, c, "", 421)
		}
		hookSMTPClosed(t, c)
		closeConn()
	}
}

func TestHandledGreetingKeepsConnectionOpen(t *testing.T) {
	s := smtp.NewServer(&backend{})
	s.GreetingHook = func(c *smtp.Conn) error {
		c.HookData = "greeting"
		if err := c.WriteRaw("220 custom banner\r\n"); err != nil {
			return err
		}
		return smtp.ErrResponseHandled
	}
	s.CommandHook = func(c *smtp.Conn, _, _ string) error {
		if c.HookData != nil {
			t.Error("greeting data leaked to command")
		}
		return nil
	}
	c, closeConn := hookSMTPConn(t, s)
	defer closeConn()
	if got := hookSMTPReply(t, c, "", 220); got != "custom banner" {
		t.Fatal(got)
	}
	hookSMTPReply(t, c, "CONNECT", 501)
	hookSMTPReply(t, c, "NOOP", 250)
}

func TestRejectedBDATDrainsAndRecovers(t *testing.T) {
	for _, payload := range []string{"NOOP\r\n", strings.Repeat("x", 10000)} {
		s := smtp.NewServer(&backend{})
		s.CommandHook = func(_ *smtp.Conn, name, _ string) error {
			if name == "BDAT" {
				return &smtp.SMTPError{Code: 451, Message: "retry"}
			}
			return nil
		}
		c, closeConn := hookSMTPConn(t, s)
		hookSMTPReply(t, c, "", 220)
		hookSMTPReply(t, c, "EHLO localhost", 250)
		hookSMTPReply(t, c, "MAIL FROM:<sender@example.test>", 250)
		hookSMTPReply(t, c, "RCPT TO:<recipient@example.test>", 250)
		if len(payload) > s.MaxLineLength {
			// Avoid the upstream command reader's read-ahead line limit before
			// BDAT is parsed; the chunk itself must be read without that limit.
			hookSMTPReply(t, c, fmt.Sprintf("BDAT %d LAST", len(payload)), 451)
			fmt.Fprint(c.W, payload+"NOOP\r\n")
			if err := c.W.Flush(); err != nil {
				t.Fatal(err)
			}
		} else {
			fmt.Fprintf(c.W, "BDAT %d LAST\r\n%sNOOP\r\n", len(payload), payload)
			if err := c.W.Flush(); err != nil {
				t.Fatal(err)
			}
			hookSMTPReply(t, c, "", 451)
		}
		hookSMTPReply(t, c, "", 250)
		hookSMTPReply(t, c, "RSET", 250)
		hookSMTPReply(t, c, "MAIL FROM:<sender@example.test>", 250)
		hookSMTPReply(t, c, "RCPT TO:<recipient@example.test>", 250)
		hookSMTPReply(t, c, "DATA", 354)
		hookSMTPReply(t, c, "retry body\r\n.", 250)
		closeConn()
	}
}

func TestRejectedBDATWithInvalidSizeCloses(t *testing.T) {
	for _, argument := range []string{"", "invalid", "-1", "4294967296"} {
		s := smtp.NewServer(&backend{})
		s.CommandHook = func(_ *smtp.Conn, name, _ string) error {
			if name == "BDAT" {
				return &smtp.SMTPError{Code: 451, Message: "retry"}
			}
			return nil
		}
		c, closeConn := hookSMTPConn(t, s)
		hookSMTPReply(t, c, "", 220)
		hookSMTPReply(t, c, strings.TrimSpace("BDAT "+argument), 451)
		hookSMTPClosed(t, c)
		closeConn()
	}
}

func TestResponseHookHandlesDATAAndBDATErrorsWithoutDuplicateReplies(t *testing.T) {
	for _, command := range []string{"DATA", "BDAT"} {
		t.Run(command, func(t *testing.T) {
			failure := errors.New("injected backend failure")
			s := smtp.NewServer(&backend{dataErr: failure, dataErrOffset: 4})
			s.ResponseHook = func(c *smtp.Conn, reply *smtp.Reply) *smtp.Reply {
				if reply.Err != failure {
					return reply
				}
				c.WriteReply(&smtp.Reply{Code: 451, Lines: []string{"handled backend failure"}})
				return nil
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

func TestResponseHookSeesLMTPRecipientErrors(t *testing.T) {
	for _, command := range []string{"DATA", "BDAT"} {
		t.Run(command, func(t *testing.T) {
			failure := errors.New("recipient failed")
			be := &backend{implementLMTPData: true}
			be.lmtpStatus = []struct {
				addr string
				err  error
			}{{"first@example.test", failure}, {"second@example.test", nil}}
			s := smtp.NewServer(be)
			s.LMTP = true
			var seen atomic.Int32
			s.ResponseHook = func(_ *smtp.Conn, r *smtp.Reply) *smtp.Reply {
				if r.Err == failure {
					seen.Add(1)
					r.Code = 450
					r.EnhancedCode = smtp.EnhancedCode{4, 0, 0}
				}
				return r
			}
			c, closeConn := hookSMTPConn(t, s)
			defer closeConn()
			hookSMTPReply(t, c, "", 220)
			hookSMTPReply(t, c, "LHLO localhost", 250)
			hookSMTPReply(t, c, "MAIL FROM:<sender@example.test>", 250)
			hookSMTPReply(t, c, "RCPT TO:<first@example.test>", 250)
			hookSMTPReply(t, c, "RCPT TO:<second@example.test>", 250)
			if command == "DATA" {
				hookSMTPReply(t, c, "DATA", 354)
				c.PrintfLine("test\r\n.")
			} else {
				c.W.WriteString("BDAT 4 LAST\r\ntest")
				c.W.Flush()
			}
			hookSMTPReply(t, c, "", 450)
			hookSMTPReply(t, c, "", 250)
			hookSMTPReply(t, c, "NOOP", 250)
			if seen.Load() != 1 {
				t.Fatal("missing recipient error", seen.Load())
			}
		})
	}
}

func TestCommandContextClearedBeforeIdleTimeout(t *testing.T) {
	s := smtp.NewServer(&backend{})
	s.ReadTimeout = 50 * time.Millisecond
	s.CommandHook = func(c *smtp.Conn, name, _ string) error { c.HookData = name; return nil }
	s.ResponseHook = func(c *smtp.Conn, r *smtp.Reply) *smtp.Reply {
		if r.Code == 421 && (c.Command() != "" || c.HookData != nil) {
			t.Error("stale command context on idle timeout")
		}
		return r
	}
	c, closeConn := hookSMTPConn(t, s)
	defer closeConn()
	hookSMTPReply(t, c, "", 220)
	hookSMTPReply(t, c, "NOOP", 250)
	hookSMTPReply(t, c, "", 421)
	hookSMTPClosed(t, c)
}

func TestHandledBDATAbortsPartialTransactionAndCanRetry(t *testing.T) {
	s := smtp.NewServer(&backend{})
	chunks := 0
	s.CommandHook = func(c *smtp.Conn, name, _ string) error {
		if name == "BDAT" {
			chunks++
			if chunks == 2 {
				if err := c.WriteRaw("451 retry chunked transaction\r\n"); err != nil {
					return err
				}
				return fmt.Errorf("handled: %w", smtp.ErrResponseHandled)
			}
		}
		return nil
	}
	c, closeConn := hookSMTPConn(t, s)
	defer closeConn()
	hookSMTPReply(t, c, "", 220)
	hookSMTPReply(t, c, "EHLO localhost", 250)
	hookSMTPReply(t, c, "MAIL FROM:<sender@example.test>", 250)
	hookSMTPReply(t, c, "RCPT TO:<recipient@example.test>", 250)
	c.W.WriteString("BDAT 4\r\nseed")
	c.W.Flush()
	hookSMTPReply(t, c, "", 250)
	c.W.WriteString("BDAT 6 LAST\r\nNOOP\r\nNOOP\r\n")
	c.W.Flush()
	hookSMTPReply(t, c, "", 451)
	hookSMTPReply(t, c, "", 250)
	hookSMTPReply(t, c, "MAIL FROM:<sender@example.test>", 250)
	hookSMTPReply(t, c, "RCPT TO:<recipient@example.test>", 250)
	c.W.WriteString("BDAT 4 LAST\r\ntest")
	c.W.Flush()
	hookSMTPReply(t, c, "", 250)
	hookSMTPReply(t, c, "NOOP", 250)
}
