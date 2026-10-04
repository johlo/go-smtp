# go-smtp

## Mailarky fork

This public fork of [emersion/go-smtp](https://github.com/emersion/go-smtp)
is based on **v0.25.0**. The `mailarky` branch adds server hooks used by
[Mailarky](https://github.com/johlo/mailarky) for protocol fault injection.
The upstream client, server, tests and MIT license are retained.

- `Server.CommandHook` runs before command handling, including `CONNECT` after
  implicit TLS negotiation. Return true when the hook has handled the reply.
  Handled CONNECT/BDAT commands close the connection; BDAT may have pipelined data.
- `Server.ResponseHook` can replace a reply or return nil to suppress it.
- `Server.ErrorHook` handles backend errors on the command goroutine, including
  SMTP DATA and BDAT errors, avoiding concurrent response writes from a worker.
- `Conn.Command()`, `Conn.HookData`, `Reply` and `Conn.WriteReply` support those
  hooks. `WriteReply` bypasses `ResponseHook` to prevent recursive interception.

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
