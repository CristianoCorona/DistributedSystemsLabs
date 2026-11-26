package response

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// The tests compare exact bytes: a client parses the status line and the headers
// literally, so a missing "\r" or a wrong Content-Length is a real bug, not a detail.

// TestWrite checks the exact bytes of a complete response with an in-memory body: status
// line, the three headers, the empty line and the body.
func TestWrite(t *testing.T) {
	var buf bytes.Buffer

	if err := Write(&buf, http.StatusOK, "text/html", []byte("<h1>Hello</h1>")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "HTTP/1.0 200 OK\r\n" +
		"Content-Type: text/html\r\n" +
		"Content-Length: 14\r\n" +
		"Connection: close\r\n" +
		"\r\n" +
		"<h1>Hello</h1>"
	if got := buf.String(); got != want {
		t.Errorf("Write wrote\n%q\nwant\n%q", got, want)
	}
}

// TestError checks that every error status the server sends itself produces a well-formed
// text/plain response whose body is the status text, as the lab requires.
func TestError(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "HTTP/1.0 400 Bad Request\r\nContent-Type: text/plain\r\nContent-Length: 11\r\nConnection: close\r\n\r\nBad Request"},
		{http.StatusNotFound, "HTTP/1.0 404 Not Found\r\nContent-Type: text/plain\r\nContent-Length: 9\r\nConnection: close\r\n\r\nNot Found"},
		// 408 is sent by the server package when the headers do not arrive in time.
		{http.StatusRequestTimeout, "HTTP/1.0 408 Request Timeout\r\nContent-Type: text/plain\r\nContent-Length: 15\r\nConnection: close\r\n\r\nRequest Timeout"},
		{http.StatusNotImplemented, "HTTP/1.0 501 Not Implemented\r\nContent-Type: text/plain\r\nContent-Length: 15\r\nConnection: close\r\n\r\nNot Implemented"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		Error(&buf, c.status)
		if got := buf.String(); got != c.want {
			t.Errorf("Error(%d) wrote\n%q\nwant\n%q", c.status, got, c.want)
		}
	}
}

// TestStream checks the exact bytes of a response whose body is copied from a reader with a
// given size, which is how files are sent to the client.
func TestStream(t *testing.T) {
	var buf bytes.Buffer

	if err := Stream(&buf, http.StatusOK, "text/plain", 5, strings.NewReader("hello")); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := "HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello"
	if got := buf.String(); got != want {
		t.Errorf("Stream wrote\n%q\nwant\n%q", got, want)
	}
}

// TestEmpty checks that a 204 response has only the status line and Connection: close.
// It matters because RFC 9110 forbids Content-Length in a 204, which is the long-poll
// timeout answer.
func TestEmpty(t *testing.T) {
	var buf bytes.Buffer

	// A 204 response has no body, so it must not carry Content-Type or Content-Length.
	if err := Empty(&buf, http.StatusNoContent); err != nil {
		t.Fatalf("Empty: %v", err)
	}
	want := "HTTP/1.0 204 No Content\r\nConnection: close\r\n\r\n"
	if got := buf.String(); got != want {
		t.Errorf("Empty wrote\n%q\nwant\n%q", got, want)
	}
}
