package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

const (
	listenAddress  = ":8080"
	maxRequestHead = 8 << 10
	readTimeout    = 5 * time.Second
	writeTimeout   = 5 * time.Second
)

var errRequestHeadTooLarge = errors.New("request head too large")

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		log.Fatalf("listen on %s: %v", listenAddress, err)
	}
	defer listener.Close()

	log.Printf("listening on http://localhost%s", listenAddress)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalf("accept connection: %v", err)
		}

		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	log.Printf("%s connected", remote)

	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		log.Printf("%s set read deadline: %v", remote, err)
		return
	}

	reader := bufio.NewReaderSize(conn, maxRequestHead)
	head, err := readRequestHead(reader, maxRequestHead)
	if err != nil {
		if errors.Is(err, errRequestHeadTooLarge) {
			respond(conn, remote, "431 Request Header Fields Too Large", "Request Header Fields Too Large\n")
			return
		}

		log.Printf("%s read request: %v", remote, err)
		return
	}

	log.Printf("%s raw request: %q", remote, head)

	status, body, extraHeaders := route(head)
	respond(conn, remote, status, body, extraHeaders...)
}

// readRequestHead reads through the blank line that terminates the header
// section. It bounds both the buffered reader and the accumulated head.
func readRequestHead(reader *bufio.Reader, limit int) ([]byte, error) {
	head := make([]byte, 0, 1024)

	for {
		line, err := reader.ReadSlice('\n')
		head = append(head, line...)

		if len(head) > limit || errors.Is(err, bufio.ErrBufferFull) {
			return nil, errRequestHeadTooLarge
		}
		if err != nil {
			return nil, err
		}
		if bytes.HasSuffix(head, []byte("\r\n\r\n")) {
			return head, nil
		}
	}
}

// route only parses the request line for this milestone. Header parsing is
// intentionally deferred.
func route(head []byte) (status, body string, extraHeaders []string) {
	requestLine, _, _ := strings.Cut(string(head), "\r\n")
	parts := strings.Split(requestLine, " ")
	if len(parts) != 3 {
		return "400 Bad Request", "Bad Request\n", nil
	}

	method, target, version := parts[0], parts[1], parts[2]
	if version != "HTTP/1.1" {
		return "505 HTTP Version Not Supported", "HTTP Version Not Supported\n", nil
	}

	path, _, _ := strings.Cut(target, "?")
	if path != "/hello" {
		return "404 Not Found", "Not Found\n", nil
	}
	if method != "GET" {
		return "405 Method Not Allowed", "Method Not Allowed\n", []string{"Allow: GET"}
	}

	return "200 OK", "Hello World", nil
}

func respond(conn net.Conn, remote, status, body string, extraHeaders ...string) {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		log.Printf("%s set write deadline: %v", remote, err)
		return
	}

	if err := writeResponse(conn, status, body, extraHeaders...); err != nil {
		log.Printf("%s write response: %v", remote, err)
		return
	}

	log.Printf("%s responded %s (%d body bytes)", remote, status, len(body))
}

// writeResponse manually serializes a complete HTTP/1.1 response. The
// connection is always closed after this response.
func writeResponse(w io.Writer, status, body string, extraHeaders ...string) error {
	var response strings.Builder

	fmt.Fprintf(&response, "HTTP/1.1 %s\r\n", status)
	response.WriteString("Content-Type: text/plain\r\n")
	fmt.Fprintf(&response, "Content-Length: %d\r\n", len(body))
	response.WriteString("Connection: close\r\n")
	for _, header := range extraHeaders {
		response.WriteString(header)
		response.WriteString("\r\n")
	}
	response.WriteString("\r\n")
	response.WriteString(body)

	_, err := io.Copy(w, strings.NewReader(response.String()))
	return err
}
