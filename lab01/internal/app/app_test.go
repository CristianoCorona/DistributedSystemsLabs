package app

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/broker"
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/filestore"
)

// newTestApp returns an App whose file store lives in a temporary directory.
func newTestApp(t *testing.T) *App {
	t.Helper()
	files, err := filestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(files, broker.New(64))
}

// roundTrip parses raw as an HTTP request, passes it to Route and returns the bytes
// Route wrote to the connection.
//
// net.Pipe gives two connected in-memory net.Conn ends, so Route can be tested without
// opening a socket. The pipe is synchronous and unbuffered: every Write blocks until the
// other end reads it. If the test read only after Route returned, Route would block on its
// first Write and the test would deadlock. The response is therefore read in a separate
// goroutine, while Route writes.
func roundTrip(t *testing.T, a *App, raw string) string {
	t.Helper()

	// The request (and its body) is parsed from the string, as Server.handleConn would
	// parse it from the socket.
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("parsing test request: %v", err)
	}

	serverEnd, clientEnd := net.Pipe()
	defer clientEnd.Close()

	got := make(chan string)
	go func() {
		data, _ := io.ReadAll(clientEnd) // returns when the server end is closed
		got <- string(data)
	}()

	a.Route(serverEnd, req, func() {})
	serverEnd.Close() // like Server.handleConn: one request, then the connection is closed
	return <-got
}

// statusLine returns the first line of a response, without "\r\n".
func statusLine(resp string) string {
	line, _, _ := strings.Cut(resp, "\r\n")
	return line
}

// TestRouteStatusCodes checks that Route answers each kind of request with the status code the
// lab asks for: 400 for invalid methods and bad extensions, 501 for valid methods that are
// not implemented, 404 for missing files.
func TestRouteStatusCodes(t *testing.T) {
	a := newTestApp(t)

	cases := []struct {
		name string
		raw  string
		want string
	}{
		// Not one of the nine methods of knownMethods: an invalid request, so 400, not 501.
		{"unknown method", "FOO /hello.txt HTTP/1.1\r\nHost: x\r\n\r\n", "HTTP/1.0 400 Bad Request"},
		// Methods are case-sensitive: "get" is not GET, so it is unknown too.
		{"lowercase method", "get /hello.txt HTTP/1.1\r\nHost: x\r\n\r\n", "HTTP/1.0 400 Bad Request"},
		// Valid methods that this lab does not implement.
		{"DELETE", "DELETE /hello.txt HTTP/1.1\r\nHost: x\r\n\r\n", "HTTP/1.0 501 Not Implemented"},
		{"PUT", "PUT /hello.txt HTTP/1.1\r\nHost: x\r\nContent-Length: 1\r\n\r\nx", "HTTP/1.0 501 Not Implemented"},
		{"GET missing", "GET /missing.html HTTP/1.1\r\nHost: x\r\n\r\n", "HTTP/1.0 404 Not Found"},
		// The extension is checked before the file is looked up: 400 even though it is missing.
		{"GET bad extension", "GET /document.pdf HTTP/1.1\r\nHost: x\r\n\r\n", "HTTP/1.0 400 Bad Request"},
		{"POST bad extension", "POST /document.pdf HTTP/1.1\r\nHost: x\r\nContent-Length: 1\r\n\r\nx", "HTTP/1.0 400 Bad Request"},
		// The server never creates directories; the failure to create the temporary file is
		// an "other error condition", answered 400.
		{"POST missing directory", "POST /nodir/a.txt HTTP/1.1\r\nHost: x\r\nContent-Length: 1\r\n\r\nx", "HTTP/1.0 400 Bad Request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := statusLine(roundTrip(t, a, c.raw)); got != c.want {
				t.Errorf("status line = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRoutePostThenGet checks the complete responses of a POST followed by a GET of the same
// file, through Route: the file stored over HTTP comes back over HTTP with the right
// status, headers and body.
func TestRoutePostThenGet(t *testing.T) {
	a := newTestApp(t)

	post := roundTrip(t, a, "POST /page.html HTTP/1.1\r\nHost: x\r\nContent-Length: 14\r\n\r\n<h1>Hello</h1>")
	wantPost := "HTTP/1.0 201 Created\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 25\r\n" +
		"Connection: close\r\n" +
		"\r\n" +
		"File stored successfully."
	if post != wantPost {
		t.Fatalf("POST response\n%q\nwant\n%q", post, wantPost)
	}

	// The GET must return the stored body with the Content-Type of its extension.
	get := roundTrip(t, a, "GET /page.html HTTP/1.1\r\nHost: x\r\n\r\n")
	wantGet := "HTTP/1.0 200 OK\r\n" +
		"Content-Type: text/html\r\n" +
		"Content-Length: 14\r\n" +
		"Connection: close\r\n" +
		"\r\n" +
		"<h1>Hello</h1>"
	if get != wantGet {
		t.Errorf("GET response\n%q\nwant\n%q", get, wantGet)
	}
}
