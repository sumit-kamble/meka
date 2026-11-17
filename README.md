# http-server

A from-scratch HTTP/1.1 server built in Go without using `net/http`.

## Phase 1: minimal TCP server

The current server:

- listens on port `8080` with `net.Listen`;
- accepts connections and handles each in a goroutine;
- reads a raw request header block until `\r\n\r\n`;
- logs the raw request bytes;
- manually serializes an HTTP/1.1 response;
- sends `Connection: close` and closes the socket.

It handles one request per connection and does not parse request bodies or
headers yet.

## Run

```bash
go run ./cmd/server
```

The server logs a line when it starts and then logs every accepted connection
and raw request. CRLF bytes are shown as escaped sequences in the log.

## Endpoint

```http
GET /hello HTTP/1.1
Host: localhost:8080
```

Response for `GET /hello`:

```http
HTTP/1.1 200 OK
Content-Type: text/plain
Content-Length: 11
Connection: close

Hello World
```

The body is exactly 11 bytes with no trailing newline.

Other behavior:

| Request | Response |
| --- | --- |
| Unknown path | `404 Not Found` |
| Non-`GET` method on `/hello` | `405 Method Not Allowed` |
| Malformed request line | `400 Bad Request` |
| Unsupported HTTP version | `505 HTTP Version Not Supported` |
| Header block over 8 KiB | `431 Request Header Fields Too Large` |

## Test with curl

In a second terminal:

```bash
curl -v http://localhost:8080/hello
```

Check the exact status and body size:

```bash
curl -sS -o /dev/null -w 'status=%{http_code} bytes=%{size_download}\n' \
  http://localhost:8080/hello
```

Expected:

```text
status=200 bytes=11
```

Inspect the response headers:

```bash
curl -sS -D - -o /dev/null http://localhost:8080/hello
```

Test the error paths:

```bash
curl -i http://localhost:8080/missing
curl -i -X POST http://localhost:8080/hello
```

On Windows PowerShell, use `curl.exe` instead of the `curl` alias.

## Current limitations

- No keep-alive or pipelined requests
- No request-body or `Content-Length` handling
- No header parsing or validation
- No configurable address, connection limits, or graceful shutdown
- No TLS, HTTP/2, or automated tests

The detailed design for later phases is in [docs/architecture.md](docs/architecture.md)
and [docs/http-fundamental.md](docs/http-fundamental.md).
