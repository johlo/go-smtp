# go-smtp

## Mailarky fork

This public fork of [emersion/go-smtp](https://github.com/emersion/go-smtp)
is based on **v0.25.0**. The `smtp-protocol-hooks` branch adds server hooks used by
[Mailarky](https://github.com/johlo/mailarky) for protocol fault injection.
The upstream client, server, tests and MIT license are retained.

- `Server.GreetingHook` runs after implicit TLS negotiation, before the banner.
  `Conn.Command()` is empty here. Returning `ErrResponseHandled` suppresses the
  normal banner and keeps the connection open; `Conn.Disconnect(reply)` sends
  an optional reply, closes, and returns that sentinel. Other errors reject
  the connection with an error reply.
- `Server.CommandHook` runs before a parsed command. Return nil to continue,
  an error for a rejection, or `ErrResponseHandled` after sending a custom reply.
  Rejected BDAT chunks are drained and the transaction is reset so the client
  can retry. An invalid chunk size or a failed drain closes the connection.
  Commands rejected by the line parser do not reach this hook.
- `Server.ResponseHook` may edit or replace a reply, or return nil to suppress it.
  `Reply.Err` carries the original backend error, including SMTP DATA/BDAT and
  each LMTP recipient's result. The hook runs on the connection goroutine.
- `Conn.Command()` and `Conn.HookData` last only for the current command and
  are cleared before reading another command. Greeting metadata is cleared
  before the first command.
- `Conn.WriteReply` and `Conn.WriteRaw` bypass ResponseHook. Raw writes use the
  protocol writer and honor WriteTimeout; call them from hooks, not concurrent
  backend workers. `Conn.Disconnect(nil)` closes without a reply.

Hooks are optional; configure them before serving connections. Normal behavior
is preserved when they are nil. No sandbox-specific fault registry is included.

The module path is `github.com/johlo/go-smtp`. Consumers import this fork
directly and pin a revision with `go get`; no `replace` directive is needed.
Run `go test -race ./...` and `go vet ./...` when updating.

[![Go Reference](https://pkg.go.dev/badge/github.com/johlo/go-smtp.svg)](https://pkg.go.dev/github.com/johlo/go-smtp)

An ESMTP client and server library written in Go.

## Features

* ESMTP client & server implementing [RFC 5321]
* Support for additional SMTP extensions such as [AUTH] and [PIPELINING]
* UTF-8 support for subject and message
* [LMTP] support

## Relationship with net/smtp

The Go standard library provides a SMTP client implementation in `net/smtp`.
However `net/smtp` is frozen: it's not getting any new features. go-smtp
provides a server implementation and a number of client improvements.

## Licence

MIT

[RFC 5321]: https://tools.ietf.org/html/rfc5321
[AUTH]: https://tools.ietf.org/html/rfc4954
[PIPELINING]: https://tools.ietf.org/html/rfc2920
[LMTP]: https://tools.ietf.org/html/rfc2033
