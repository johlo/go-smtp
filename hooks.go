package smtp

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// ErrResponseHandled tells a hook caller that the response has already been
// handled. It suppresses the default response without closing the connection.
var ErrResponseHandled = errors.New("smtp: response handled by hook")

// Disconnect sends an optional reply without invoking ResponseHook, closes the
// connection, and returns ErrResponseHandled for use from a hook.
func (c *Conn) Disconnect(reply *Reply) error {
	if reply != nil {
		c.WriteReply(reply)
	}
	c.Close()
	return ErrResponseHandled
}

// WriteRaw writes and flushes a response verbatim through the protocol writer.
// It honors WriteTimeout and bypasses ResponseHook. Call it only from a hook on
// the connection's command goroutine, never from a DATA/BDAT backend worker.
func (c *Conn) WriteRaw(response string) error {
	if c.server.WriteTimeout != 0 {
		if err := c.conn.SetWriteDeadline(time.Now().Add(c.server.WriteTimeout)); err != nil {
			return err
		}
	}
	if _, err := c.text.W.WriteString(response); err != nil {
		return err
	}
	return c.text.W.Flush()
}

// A rejected chunk still belongs to BDAT, even when its bytes look like SMTP
// commands. Drain it before reading the next command and abort the transaction.
func (c *Conn) discardRejectedChunk(arg string) {
	defer func() {
		if c.bdatPipe != nil {
			c.bdatPipe.CloseWithError(ErrDataReset)
			<-c.dataResult
		}
		c.reset()
	}()
	args := strings.Fields(arg)
	if len(args) == 0 {
		c.Close()
		return
	}
	size, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil {
		c.Close()
		return
	}
	c.lineLimitReader.LineLimit = 0
	if c.server.ReadTimeout != 0 {
		c.conn.SetReadDeadline(time.Now().Add(c.server.ReadTimeout))
	}
	if _, err := io.CopyN(io.Discard, c.text.R, int64(size)); err != nil {
		c.Close()
	}
}
