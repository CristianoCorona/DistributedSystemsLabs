// Package server is the transport layer: it accepts TCP connections, bounds how many are
// handled at the same time and parses one HTTP request per connection. It knows nothing
// about files: every well-formed request is passed to a Handler.
package server

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/response"
)

// Handler is called for every well-formed request. It writes the response to conn.
// It is a plain function type, so the application layer does not need to import this
// package: any method with the same signature, such as (*app.App).Route, can be passed.
type Handler func(conn net.Conn, req *http.Request)

// Server is a minimal HTTP server built directly on TCP sockets.
// It serves one request per connection and bounds the number of
// connections handled concurrently.
type Server struct {
	addr    string        // address to listen on, in "host:port" form
	sem     chan struct{} // counting semaphore, one slot per active connection
	handler Handler       // called for every well-formed request
	timeout time.Duration // per-connection deadline, in seconds
}

// New returns a Server that listens on addr, handles at most maxConns
// connections at the same time and passes valid requests to h.
// A connection that does not complete within timeout seconds is closed.
func New(addr string, maxConns int, h Handler, timeout time.Duration) *Server {
	return &Server{
		addr:    addr,
		sem:     make(chan struct{}, maxConns),
		handler: h,
		timeout: timeout,
	}
}

// Serve listens on s.addr and handles each connection in its own goroutine.
// A semaphore slot is acquired before Accept, so once the limit is reached
// new connections wait in the kernel backlog.
// Serve returns only if the listener cannot be created or has been closed.
func (s *Server) Serve() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		// Error when trying to listen on the specified address. This could be due to the port being in use or insufficient permissions.
		return err
	}
	defer listener.Close()
	log.Println("Listening on", s.addr)
	for {
		s.sem <- struct{}{} // blocks while maxConns connections are active

		conn, err := listener.Accept()
		if err != nil {
			<-s.sem // the slot will not be used: give it back
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			log.Println("Error accepting connection:", err) // log and continue accepting new connections
			continue
		}

		go func() {
			// defer needs a function call, not a channel operation,
			// hence the closure.
			defer func() { <-s.sem }()
			s.handleConn(conn)
		}()
	}
}

// handleConn reads one HTTP request from conn and passes it to the handler.
// Exactly one request per connection: the connection is always closed on return.
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	// The deadline covers reads and writes, so a client that is too slow
	// to send its request or to read the response cannot hold a slot forever.
	conn.SetDeadline(time.Now().Add(time.Duration(s.timeout) * time.Second))

	// http.ReadRequest needs a *bufio.Reader, which reads from the socket in
	// chunks. Only the request line and headers are parsed here: the body
	// stays in req.Body and must be read from there, not from conn.
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		switch {
		case errors.Is(err, io.EOF):
			log.Println("Connection closed by client without sending a request")
		case errors.Is(err, os.ErrDeadlineExceeded):
			log.Println("Timeout waiting for request from", conn.RemoteAddr())
			conn.SetWriteDeadline(time.Now().Add(time.Second)) // set a short deadline to write the error response
			response.Error(conn, http.StatusRequestTimeout)    //notify the client
		default:
			log.Println("Bad request from", conn.RemoteAddr(), ":", err)
			response.Error(conn, http.StatusBadRequest)
		}
		return
	}

	s.handler(conn, req)
}
