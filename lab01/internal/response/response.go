// Package response writes HTTP/1.0 responses by hand.
//
// The lab allows net/http only for parsing requests, so responses are not built with
// http.Response: the status line and the headers are written with fmt.Fprintf.
package response

import (
	"fmt"
	"io"
	"log"
	"net/http"
)

// Write writes a complete HTTP/1.0 response to w.
// We avoid making use of the http.Response to respect the lab's requirements of only using it for parsing the request.
func Write(w io.Writer, status int, contentType string, body []byte) error {
	// 1. Status line, e.g. "HTTP/1.0 404 Not Found"
	// We use the if with a short statement to check for errors in the fmt.Fprintf calls. If an error occurs, we return it immediately.
	if _, err := fmt.Fprintf(w, "HTTP/1.0 %d %s\r\n", status, http.StatusText(status)); err != nil {
		return err
	}

	// 2. Headers: Content-Type, Content-Length, Connection: close, followed by the empty line.
	// Note that len(body) already counts the bytes in the body because of its type.
	if _, err := fmt.Fprintf(w, "Content-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", contentType, len(body)); err != nil {
		return err
	}

	// 3. Body: raw bytes (e.g. JSON, JPEG, etc.) of the response.
	_, err := w.Write(body)

	return err // if no error occurred, we correctly return nil.
}

// Error writes a plain-text response whose body is the status text.
func Error(w io.Writer, status int) {
	err := Write(w, status, "text/plain", []byte(http.StatusText(status)))

	if err != nil {
		log.Println("Error writing error response:", err)
		// We cannot do much more here, because the connection is already broken. We just log the error and return.
	}
}
