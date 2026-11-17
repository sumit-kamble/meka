> Phase 1 Go HTTP/1.1 server built from first principles.

## HTTP Fundamental

1. [HTTP server](#1-http-server)
2. [TCP, Sockets, Ports & Connections](#2-tcpip-sockets-ports-and-connections)
3. [TCP Byte Stream](#3-tcp-is-a-byte-stream-partial-reads-and-writes)
4. [HTTP Request/Response structure](#4-http-request-and-response-structure)
5. [Methods Status Codes & Headers](#5-methods-status-codes-and-headers)
6. [Message Framing](#6-http11-message-framing)
7. [Connection Lifecyle](#7-keep-alive-and-connection-lifecycle)
8. [Go Concurrency](#8-go-concurrency-and-the-network-poller)
9. [End to End flow](#9-end-to-end-browser-request-to-response)
10. [Knowledge Checks](#10-knowledge-checks)
11. [References](#11-references)

## 1. HTTP Server

[🔝 Go to top](#http-fundamental)

An HTTP server is an application that interprets HTTP messages carried over a transport connection.

For the project:

- IP routes packets between hosts.
- TCP provides a reliable, ordered byte stream between two endpoints.
- HTTP/1.1 defines how those bytes represent requests and responses.
- The server parses a request, applies application behavior, serializes a response, and writes bytes back.

**TCP does not preserve HTTP message boundaries.**

- A single HTTP request can arrive across multiple reads.
- Multiple requests can arrive in one read.
- Parser must use HTTP framing rules, not assumptions about read boundaries.

## 2. TCP/IP, sockets, ports, and connections

[🔝 Go to top](#http-fundamental)

### IP

IP provides addressing and packet forwarding. An IP address identifies a network interface/host destination at the network layer.
IP itself does not guarantee delivery, ordering or retransmission.

### TCP

TCP provides a reliable, ordered, full-duplex byte stream between two endpoints. It handles sequencing, acknowledgements, retransmission, flow control, and congestion control.

A TCP connection is commonly identified by the 4-tuple:

`(source IP, source port, destination IP, destination port)`

The client usually gets an ephemeral source port; the server listens on a known destination port such as 8080 or 80. A port is not a process ID and does not itself identify an application protocol.

TCP is connection-oriented. A typical connection establishment uses the three-way handshake:

1. Client sends SYN.
2. Server replies SYN-ACK.
3. Client sends ACK.

After establishment, both sides can send bytes independently. TCP segmentation, retransmission, and buffering are below HTTP.

### Socket

A socket is an operating-system communication endpoint exposed to a process through an API/file descriptor or handle. A listening TCP socket accepts incoming connections; each accepted connection has its own connected socket. In Go, `net.Listen("tcp", addr)` returns a `net.Listener`, and `Accept()` returns a `net.Conn`.

Useful Go APIs:

- `net.Listen`, `net.Listener.Accept`
- `net.Conn.Read`, `net.Conn.Write`, `net.Conn.Close`
- `net.Conn.SetReadDeadline`, `SetWriteDeadline`, `SetDeadline`

## 3. TCP is a byte stream: partial reads and writes

[🔝 Go to top](#http-fundamental)

Do not assume one `Read` returns a complete request. `Read(p)` returns the number of bytes currently available (up to `len(p)`) and may return fewer bytes than requested. It can also return `n > 0` together with an error; process the bytes before handling the error.

Likewise, a general `io.Writer` may write fewer bytes than requested. Check both `n` and `err`, or use a helper such as `io.WriteString` / `io.Copy` where appropriate while still handling errors.

Example: the client sends these bytes:

```text
GET /hello HTTP/1.1\r\nHost: localhost\r\n\r\n
```

The server might observe them as:

- Read 1: `GET /he`
- Read 2: `llo HTTP/1.1\r\nHost: local`
- Read 3: `host\r\n\r\n`

Or one read might contain two pipelined requests. Neither case changes the HTTP message boundaries.

**Engineering consequence:** use buffered parsing and preserve bytes read beyond the current message. Never discard bytes just because they followed the first request in a read buffer.

## 4. HTTP request and response structure

[🔝 Go to top](#http-fundamental)

HTTP is an application-level request/response protocol. HTTP semantics are defined independently from a particular wire version in RFC 9110. HTTP/1.1's textual message syntax is defined by RFC 9112.

A typical HTTP/1.1 request:

```http
GET /hello?name=Sam HTTP/1.1
Host: localhost:8080
Accept: text/plain
Connection: keep-alive

```

Its parts:

1. Request line: method, request target, HTTP version.
2. Zero or more header fields.
3. Empty line (`CRLF`) ending the header section.
4. Optional message body.

A typical response:

```http
HTTP/1.1 200 OK
Content-Type: text/plain; charset=utf-8
Content-Length: 5

Hello
```

Its parts:

1. Status line: HTTP version, status code, optional reason phrase.
2. Response header fields.
3. Empty line.
4. Optional response body, subject to response framing rules.

HTTP messages are sequences of octets. Header syntax is not a license to treat arbitrary body bytes as text; bodies may be binary.

## 5. Methods, status codes, and headers

[🔝 Go to top](#http-fundamental)

Methods communicate the intended operation. Common methods:

- `GET`: retrieve a representation; defined as safe and idempotent.
- `HEAD`: like GET but no response content.
- `POST`: ask the target resource to process the enclosed representation according to resource-specific semantics.
- `PUT`: create or replace the target resource state; idempotent by definition.
- `DELETE`: request removal of the association between target resource and its current functionality; idempotent by definition.
- `OPTIONS`: request communication options.
- `PATCH`: apply partial modifications (semantics defined by its patch format/specification).

Do not equate idempotence with “returns the same response.” It means repeating the request has the same intended effect on server state.

Status code classes:

- `1xx`: informational
- `2xx`: successful
- `3xx`: redirection
- `4xx`: client error
- `5xx`: server error

Common codes: 200 OK, 201 Created, 204 No Content, 301/302 redirects, 304 Not Modified, 400 Bad Request, 401 Unauthorized, 403 Forbidden, 404 Not Found, 405 Method Not Allowed, 413 Content Too Large, 431 Request Header Fields Too Large, 500 Internal Server Error, 501 Not Implemented, 503 Service Unavailable.

Headers are field-name/value metadata and control information. Field names are case-insensitive. A server must not assume headers arrive in a particular order. Multiple field lines and list-valued fields require care; do not blindly overwrite duplicates. Some fields cannot safely be combined.

## 6. HTTP/1.1 message framing

[🔝 Go to top](#http-fundamental)

Framing answers: where does this message end, and where does the next message begin?

RFC 9112 defines body-length determination. Important cases:

1. Responses to `HEAD`, and certain status codes (1xx, 204, 304), have no message body regardless of framing fields.
2. A successful `CONNECT` response switches to tunnel mode after the header section.
3. `Transfer-Encoding` takes precedence over `Content-Length`; a message containing both is suspicious and can indicate request-smuggling risk. A recipient ought to treat it as an error and close the connection rather than allowing parser disagreement.
4. If the final transfer coding is `chunked`, the body ends at the terminating zero-size chunk and trailer section.
5. If a valid `Content-Length` applies, exactly that many octets make up the body.
6. Some responses without explicit length are delimited by connection close. This is not suitable for persistent reuse and can make truncation hard to distinguish.
7. Requests without `Transfer-Encoding` or `Content-Length` have a zero-length body.

### Content-Length

`Content-Length` is a decimal count of body octets, not characters. For UTF-8 text, character count and byte count can differ. Read exactly the declared number of bytes. Enforce a configured maximum before allocating or buffering a body.

### Transfer-Encoding: chunked

Chunked transfer coding allows a sender to stream a body without knowing its final size in advance. The body consists of chunks:

```http
4\r\n
Wiki\r\n
5\r\n
pedia\r\n
0\r\n
\r\n
```

Each chunk begins with a hexadecimal size and CRLF, followed by that many data octets and CRLF. A zero-size chunk ends the chunk sequence; optional trailers follow, then a final empty line. Chunk framing is not part of the representation data delivered to the application.

Do not confuse `Transfer-Encoding` (message transfer coding) with `Content-Encoding` (representation coding such as gzip).

### Security: request smuggling

If a proxy and an origin server disagree about message boundaries, an attacker may cause bytes to be interpreted as different requests by different hops. Strict, consistent parsing is essential. For the first server version, reject ambiguous framing (especially `Transfer-Encoding` plus `Content-Length`), malformed lengths, invalid chunk syntax, and excessive headers/bodies. Close the connection after framing errors.

## 7. Keep-alive and connection lifecycle

[🔝 Go to top](#http-fundamental)

HTTP/1.1 connections are persistent by default unless the client or server indicates `Connection: close`. Persistence allows multiple request/response exchanges over one TCP connection, avoiding a new handshake for every request.

A simplified lifecycle:

1. `Listen` creates a listening socket.
2. `Accept` returns a connected socket.
3. Read and parse one request.
4. Handle it and write a correctly framed response.
5. If persistence is allowed, parse the next request on the same connection.
6. Close on `Connection: close`, protocol error, timeout, server shutdown, or resource policy.

HTTP keep-alive is an HTTP connection-management concept. TCP keepalive probes are an operating-system TCP feature; they are not the same thing.

HTTP/1.1 also permits pipelining, where a client sends multiple requests without waiting for earlier responses. Responses must remain in request order. A first implementation can deliberately avoid pipelining support while ensuring it does not lose already-buffered subsequent request bytes.

Use read, write, header, body, and idle deadlines. Also limit concurrent connections and request sizes. An unbounded goroutine-per-connection design can still exhaust memory, file descriptors, or downstream resources.

## 8. Go concurrency and the network poller

[🔝 Go to top](#http-fundamental)

A simple Go server commonly has:

- one goroutine accepting connections;
- one goroutine per accepted connection;
- a per-connection loop reading requests and writing responses.

Goroutines are lightweight execution units scheduled by the Go runtime. They are not equivalent to one operating-system thread each.

Go's `net` package integrates network I/O with the runtime poller. When a goroutine waits for network readiness, the runtime can park it rather than requiring a dedicated OS thread to block for that socket. When the socket becomes ready, the runtime makes the goroutine runnable. The exact OS mechanism varies by platform (for example, epoll on Linux and kqueue on BSD-like systems).

Important distinctions:

- The runtime poller does not parse HTTP.
- It does not make application-level resource limits unnecessary.
- A goroutine-per-connection model is easy to reason about but needs admission control and timeouts.
- Multiple goroutines may call methods on a `net.Conn` simultaneously, but your HTTP parser and response ordering still need deliberate ownership/synchronization.
- Avoid concurrent writes to one connection unless writes are serialized.

Read Go's `net.Conn` contract and the runtime's `netpoll` implementation for implementation-level understanding; treat runtime internals as implementation details, not stable APIs.

## 9. End-to-end: browser request to response

[🔝 Go to top](#http-fundamental)

For `http://example.test:8080/hello` (plain HTTP; HTTPS adds TLS):

1. **URL interpretation:** Browser extracts scheme, host, port, and path.
2. **Name resolution:** Browser/OS resolves the hostname to an IP address, potentially using DNS and caches.
3. **Routing:** The OS selects a route and network interface for the destination.
4. **TCP establishment:** Client sends SYN; server replies SYN-ACK; client sends ACK. The server's listening socket accepts the new connection.
5. **HTTP serialization:** Browser constructs an HTTP/1.1 request line and headers, followed by CRLF CRLF and any body.
6. **TCP transmission:** Browser writes bytes. TCP segments and transports them reliably; segment boundaries do not correspond to HTTP boundaries.
7. **Server accept/read:** Go's listener accepts the connection. A connection handler reads available bytes. Reads may be partial or may include bytes for multiple requests.
8. **HTTP parsing/framing:** Server parses the start line and headers, validates syntax, determines body framing, and reads exactly the body bytes.
9. **Application handling:** Server routes the request, validates input, runs handler logic, and creates a response.
10. **Response serialization:** Server emits status line, headers, CRLF CRLF, and a body whose framing is unambiguous.
11. **TCP response delivery:** Bytes are written to the connection and transported back. Client reads a byte stream and parses the HTTP response using its framing rules.
12. **Reuse or close:** If persistent connection rules permit, browser can send another request on the same connection; otherwise one side closes it.

For HTTPS, TLS is negotiated after TCP establishment and before HTTP application data is exchanged (for conventional HTTPS over TCP). HTTP/2 may be negotiated through ALPN. HTTP/3 uses QUIC over UDP and is outside this project's first implementation scope.

## 10. Knowledge checks

[🔝 Go to top](#http-fundamental)

You should be able to answer these without notes:

1. Why can one HTTP request arrive across several `Read` calls?
2. Why can one `Read` contain more than one request?
3. What identifies a TCP connection?
4. What is the difference between a listening socket and an accepted connection?
5. Why is `Content-Length` measured in octets?
6. What is the difference between `Transfer-Encoding` and `Content-Encoding`?
7. What happens when a request has neither `Content-Length` nor `Transfer-Encoding`?
8. Why is `Transfer-Encoding` plus `Content-Length` dangerous?
9. Why must a persistent server preserve unread buffered bytes?
10. What does Go's network poller do, and what does it not do?

## 11. References

[🔝 Go to top](#http-fundamental)

- [RFC 9110 — HTTP Semantics](https://www.rfc-editor.org/rfc/rfc9110.html): terminology, methods, status codes, fields, semantics.
- [RFC 9112 — HTTP/1.1](https://www.rfc-editor.org/rfc/rfc9112.html): message syntax, parsing, framing, connection management, security.
- [Go `net` package](https://pkg.go.dev/net): `Listener`, `Conn`, TCP APIs and deadlines.
- [Go `net` source](https://go.dev/src/net/net.go): standard-library networking implementation.
- [Go runtime network poller source](https://go.dev/src/runtime/netpoll.go): runtime poller internals.
