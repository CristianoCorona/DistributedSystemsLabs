package response

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// The tests compare exact bytes: a client parses the status line and the headers
// literally, so a missing "\r" or a wrong Content-Length is a real bug, not a detail.

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

func TestWriteBinaryBody(t *testing.T) {
	var buf bytes.Buffer

	// Content-Length counts bytes, not characters: a body with a NUL and a non-ASCII
	// byte must be sent unchanged and counted as 3 bytes.
	body := []byte{0x00, 0xff, 'a'}
	if err := Write(&buf, http.StatusCreated, "image/gif", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "HTTP/1.0 201 Created\r\n" +
		"Content-Type: image/gif\r\n" +
		"Content-Length: 3\r\n" +
		"Connection: close\r\n" +
		"\r\n" +
		"\x00\xffa"
	if got := buf.String(); got != want {
		t.Errorf("Write wrote\n%q\nwant\n%q", got, want)
	}
}

func TestError(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "HTTP/1.0 400 Bad Request\r\nContent-Type: text/plain\r\nContent-Length: 11\r\nConnection: close\r\n\r\nBad Request"},
		{http.StatusNotFound, "HTTP/1.0 404 Not Found\r\nContent-Type: text/plain\r\nContent-Length: 9\r\nConnection: close\r\n\r\nNot Found"},
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

func TestStreamSizeMismatch(t *testing.T) {
	var buf bytes.Buffer

	// The headers promise 10 bytes but the body has 5: the client would wait for the
	// missing bytes, so Stream must report the mismatch to its caller.
	if err := Stream(&buf, http.StatusOK, "text/plain", 10, strings.NewReader("hello")); err == nil {
		t.Error("Stream with a short body returned nil, want an error")
	}
}
