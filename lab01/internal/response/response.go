// Package response writes HTTP/1.0 responses by hand.
//
// The lab allows net/http only for parsing requests, so responses are not built with
// http.Response: the status line and the headers are written with fmt.Fprintf.
package response

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
)

// Stream writes a complete HTTP/1.0 response to w, copying the body from body.
// size must be the exact number of bytes body will produce: it becomes the
// Content-Length header, which is written before the body is read.
// We avoid making use of the http.Response to respect the lab's requirements of only using it for parsing the request.
func Stream(w io.Writer, status int, contentType string, size int64, body io.Reader) error {
	// 1. Status line, e.g. "HTTP/1.0 404 Not Found"
	// We use the if with a short statement to check for errors in the fmt.Fprintf calls. If an error occurs, we return it immediately.
	if _, err := fmt.Fprintf(w, "HTTP/1.0 %d %s\r\n", status, http.StatusText(status)); err != nil {
		return err
	}

	// 2. Headers: Content-Type, Content-Length, Connection: close, followed by the empty line.
	if _, err := fmt.Fprintf(w, "Content-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", contentType, size); err != nil {
		return err
	}

	// 3. Body: raw bytes (e.g. JSON, JPEG, etc.) of the response, copied in chunks so that
	// a large file never has to fit in memory. When w is a TCP connection and body is a
	// file, io.Copy lets the kernel move the data directly (sendfile on Linux).
	n, err := io.Copy(w, body)
	if err != nil {
		return err
	}
	// The headers are already sent, so a size mismatch cannot be reported to the client
	// anymore; returning it lets the caller log a response the client will see as broken.
	if n != size {
		return fmt.Errorf("body has %d bytes, Content-Length declared %d", n, size)
	}
	return nil // no error occurred
}

// Write writes a complete HTTP/1.0 response to w, with body held in memory.
// It is Stream with a body whose size is already known: len(body) counts its bytes.
func Write(w io.Writer, status int, contentType string, body []byte) error {
	return Stream(w, status, contentType, int64(len(body)), bytes.NewReader(body))
}

// Error writes a plain-text response whose body is the status text.
func Error(w io.Writer, status int) {
	err := Write(w, status, "text/plain", []byte(http.StatusText(status)))

	if err != nil {
		log.Println("Error writing error response:", err)
		// We cannot do much more here, because the connection is already broken. We just log the error and return.
	}
}
