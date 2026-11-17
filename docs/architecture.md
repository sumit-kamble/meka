> Phase 1
> Architecture: Conceptual design

## Initial Design

1. [Scope and Non-goals](#1-scope-and-non-goals)
2. [Layered Model](#2-layered-model)
3. [Package Layout](#3-package-layout)
4. [Core Types](#4-core-types-and-ownership)
5. [Connection Lifecycle](#5-connection-lifecycle)
6. [Parsing strategy](#6-parsing-strategy)
7. [Framing and security invariants](#7-framing-and-security-invariants)
8. [Connection Persistance](#8-connection-persistence)
9. [Councurrency Model](#9-concurrency-model)
10. [Error Policy](#10-error-policy)
11. [Test Strategy](#11-test-strategy)
12. [Design Decision](#12-design-decision-records-to-write-later)
13. [References](#13-references)

## 1. Scope and non-goals

[🔝 Go to top](#initial-design)

### In scope

- Plain TCP listener using Go's `net` package.
- HTTP/1.1 request parsing and response writing.
- Multiple sequential requests on a persistent connection.
- Concurrent connections using goroutines.
- Explicit timeouts and resource limits.
- Correct `Content-Length` and chunked request-body framing.
- Deterministic error handling and connection closure on malformed framing.

### Initially out of scope

- HTTP/2 and HTTP/3.
- TLS termination (add after plain HTTP is correct).
- Proxy behavior, caching, compression, WebSockets, CONNECT tunnels.
- HTTP pipelining response concurrency (must not corrupt or reorder responses).
- Full production feature parity with Go's `net/http`.

## 2. Layered model

[🔝 Go to top](#initial-design)

```text
Browser / HTTP client
        |
        | HTTP/1.1 bytes
        v
+---------------------------+
| TCP transport             |
| net.Listener / net.Conn   |
+---------------------------+
        |
        v
+---------------------------+
| Connection manager        |
| accept, limits, deadlines |
+---------------------------+
        |
        v
+---------------------------+
| Buffered byte reader      |
| preserve unread bytes     |
+---------------------------+
        |
        v
+---------------------------+
| HTTP/1.1 parser           |
| request line, headers,    |
| framing, body, validation |
+---------------------------+
        |
        v
+---------------------------+
| Request / Response model  |
+---------------------------+
        |
        v
+---------------------------+
| Router + handler          |
+---------------------------+
        |
        v
+---------------------------+
| Response serializer      |
| status, headers, framing  |
+---------------------------+
        |
        v
      net.Conn.Write
```

## 3. Package layout

[🔝 Go to top](#initial-design)

```text
http-server/
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── transport/
│   │   ├── listener.go
│   │   └── connection.go
│   ├── http1/
│   │   ├── parser.go
│   │   ├── framing.go
│   │   ├── request.go
│   │   ├── response.go
│   │   └── writer.go
│   ├── server/
│   │   ├── server.go
│   │   └── config.go
│   └── router/
│       └── router.go
├── tests/
│   ├── parser/
│   └── integration/
├── docs/
│   ├── http-fundamentals.md
│   └── architecture.md
├── go.mod
└── README.md
```

## 4. Core types and ownership

[🔝 Go to top](#initial-design)

Conceptual types:

```go
type Request struct {
    Method  string
    Target  string
    Version string
    Headers map[string][]string
    Body    []byte
}

type Response struct {
    StatusCode int
    Headers    map[string][]string
    Body       []byte
}

type Handler interface {
    ServeHTTP(ResponseWriter, *Request)
}
```

For a later streaming implementation, replace `Body []byte` with a bounded reader/stream abstraction. Do not buffer arbitrarily large request bodies in memory.

Ownership rules:

- One connection handler owns request parsing for its connection.
- One connection handler serializes writes for that connection.
- A request body reader must consume exactly the framed body and no bytes belonging to the next request.
- A handler must not retain mutable connection parser buffers after the request lifecycle unless ownership is transferred safely.

## 5. Connection lifecycle

[🔝 Go to top](#initial-design)

```text
Server.Start
   |
   v
net.Listen
   |
   v
Accept loop
   |
   +---- accept error? -> classify temporary/fatal/shutdown
   |
   v
admission control (max active connections)
   |
   v
go handleConn(conn)
   |
   v
set initial read deadline
   |
   v
read + parse request
   |
   +---- malformed/oversized -> write error if safe, close
   |
   v
dispatch handler
   |
   v
serialize response with valid framing
   |
   +---- Connection: close / HTTP close semantics -> close
   |
   +---- keep-alive -> next request on same buffered reader
```

A connection handler must not start unbounded work for every request. Bound active connections and, if handlers perform expensive work, bound handler concurrency separately.

## 6. Parsing strategy

[🔝 Go to top](#initial-design)

Use a buffered reader per connection (for example, `bufio.Reader`) so bytes read ahead remain available for the next parse operation.

Parsing stages:

1. Read request line with a maximum line length.
2. Validate method, request-target, and HTTP version syntax.
3. Read header lines until the empty CRLF line, enforcing total header bytes and header count limits.
4. Normalize field names for lookup while preserving values and duplicate-field semantics.
5. Determine body framing strictly using RFC 9112.
6. Reject ambiguous or invalid framing.
7. Read exactly the framed body, with a configured maximum.
8. Return a request object and leave any subsequent request bytes buffered.

For the first milestone, support origin-form request targets used by ordinary browser requests. Explicitly reject unsupported request-target forms rather than parsing them incorrectly.

## 7. Framing and security invariants

[🔝 Go to top](#initial-design)

These invariants should become tests:

- Never use TCP read boundaries as HTTP message boundaries.
- `Content-Length` is a byte count.
- A request with no applicable body framing has a zero-length body.
- If `Transfer-Encoding` is present, apply RFC 9112 precedence and validation rules.
- Reject `Transfer-Encoding` together with `Content-Length` in this initial implementation and close the connection.
- Reject invalid, conflicting, overflowing, or excessive lengths.
- For chunked bodies, validate every chunk size, delimiter, terminating chunk, and trailer boundary.
- Do not parse the next request until the current request body has been completely consumed.
- After an unrecoverable parse/framing error, close the connection; do not attempt speculative resynchronization.
- Never allow unbounded request-line, header, body, connection, or handler resource consumption.

These are security requirements, not optional performance optimizations. Parser disagreement is a common root cause of HTTP request-smuggling vulnerabilities.

## 8. Connection persistence

[🔝 Go to top](#initial-design)

HTTP/1.1 persistence is the default, subject to connection options and protocol rules. The server should:

- inspect `Connection` tokens case-insensitively;
- honor `Connection: close`;
- close after framing errors;
- use an idle/read timeout so clients cannot hold connections forever;
- keep the same buffered reader for the lifetime of the connection;
- emit response framing that lets the client identify the end of each response.

## 9. Concurrency model

[🔝 Go to top](#initial-design)

Initial model: one goroutine per accepted connection.

Advantages:

- simple connection-local state;
- easy sequential request processing;
- blocking-looking code while Go's runtime integrates network I/O with its poller.

Risks:

- unbounded goroutine and file-descriptor growth;
- slow clients consuming connection slots;
- expensive handlers saturating CPU or downstream dependencies;
- concurrent response writes corrupting message order.

Mitigations:

- maximum active connections;
- read/header/body/write/idle deadlines;
- maximum request/header/body sizes;
- bounded handler worker pool or semaphore where needed;
- graceful shutdown and connection tracking;
- metrics for active connections, errors, request sizes, and latency.

Do not add a custom event loop or raw syscalls in the first version. First measure the standard `net` implementation and understand its behavior.

## 10. Error policy

[🔝 Go to top](#initial-design)

| Condition                        | Initial behavior                                 |
| -------------------------------- | ------------------------------------------------ |
| Malformed request line/header    | 400, then close                                  |
| Unsupported HTTP version         | 505, then close                                  |
| Header too large                 | 431, then close                                  |
| Body too large                   | 413, then close                                  |
| Unsupported transfer coding      | 501 or 400 according to precise condition; close |
| Invalid/ambiguous framing        | 400, then close                                  |
| Unknown route                    | 404; connection may persist                      |
| Unsupported method for route     | 405 with `Allow`                                 |
| Handler failure                  | 500 if a valid response can still be sent        |
| Read/write timeout or peer close | close; avoid writing if transport is unusable    |

Do not promise a response if the connection has already failed. Error handling must not desynchronize the parser.

## 11. Test strategy

[🔝 Go to top](#initial-design)

### Unit tests

- request-line parsing and invalid syntax;
- case-insensitive header names;
- duplicate headers;
- CRLF handling;
- Content-Length exact byte count;
- missing/invalid/overflowing Content-Length;
- chunk sizes, extensions, trailers, zero chunk;
- Transfer-Encoding + Content-Length rejection;
- header/body limits;
- response serialization and byte-accurate Content-Length.

### Adversarial stream tests

Feed the parser using readers that:

- return one byte at a time;
- split every CRLF across reads;
- return headers and body in separate reads;
- return two requests in one read;
- return partial data and then EOF;
- return `n > 0` with an error.

### Integration tests

- multiple concurrent clients;
- sequential requests over one keep-alive connection;
- `Connection: close`;
- slow/incomplete headers and body deadlines;
- graceful shutdown;
- load tests with bounded connection counts.

Use Go's `net/http` client as an interoperability test client, not as the implementation of server.

## 12. Design decision records to write later

[🔝 Go to top](#initial-design)

Record the rationale for:

- buffered parsing versus custom byte buffer;
- body buffering versus streaming;
- supported request-target forms;
- transfer-coding support;
- connection and request concurrency limits;
- error-and-close policy;
- why the implementation intentionally differs from full `net/http` behavior.

## 13. References

[🔝 Go to top](#initial-design)

- [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html)
- [RFC 9112](https://www.rfc-editor.org/rfc/rfc9112.html)
- [Go net package](https://pkg.go.dev/net)
- [Go runtime netpoll source](https://go.dev/src/runtime/netpoll.go)
