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

// Config holds the limits of a Server.
type Config struct {
	// MaxConns is the maximum number of connections handled at the same time. The lab asks for 10, but we keep it configurable.
	MaxConns int
	// HeaderTimeout bounds the time a client has to send the request line and the headers.
	// It is short: a well-behaved client sends them right after connecting, so a client
	// that stays silent is most likely idle or malicious and must not hold a slot (especially considered we can only handle a limited number
	//  of concurrent connections).
	HeaderTimeout time.Duration
	// RequestTimeout bounds the rest of the exchange: reading the body and writing the
	// response. It is longer than HeaderTimeout, because a large upload or download over a
	// slow link legitimately takes time.
	RequestTimeout time.Duration
}

// Server is a minimal HTTP server built directly on TCP sockets.
// It serves one request per connection and bounds the number of
// connections handled concurrently.
type Server struct {
	addr    string        // address to listen on, in "host:port" form
	sem     chan struct{} // counting semaphore, one slot per active connection
	handler Handler       // called for every well-formed request
	cfg     Config        // timeouts (the connection limit lives in the capacity of sem)
}

// New returns a Server that listens on addr, handles at most cfg.MaxConns
// connections at the same time and passes valid requests to h.
// A connection is closed if the client does not send its headers within
// cfg.HeaderTimeout, or if the rest of the exchange takes longer than cfg.RequestTimeout.
func New(addr string, cfg Config, h Handler) *Server {
	return &Server{
		addr:    addr,
		sem:     make(chan struct{}, cfg.MaxConns),
		handler: h,
		cfg:     cfg,
	}
}

// Serve listens on s.addr and handles each connection in its own goroutine.
// A semaphore slot is acquired before Accept, so once the limit is reached
// new connections wait.
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
		s.sem <- struct{}{} // blocks while MaxConns connections are active. This is BEFORE the goroutine spawns, so we effectively have at most MaxConns goroutines running at the same time.

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
			// defer needs a function call, not a channel operation, hence the closure.
			defer func() { <-s.sem }()
			s.handleConn(conn)
		}()
	}
}

// handleConn reads one HTTP request from conn and passes it to the handler.
// Exactly one request per connection: the connection is always closed on return (REST).
//
// The deadline has two phases. Both use SetDeadline, which covers reads and writes:
// a read deadline alone would let a client that never reads the response hold a slot
// forever, because the write would block once the socket buffers are full.
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	// Phase 1: the request line and the headers must arrive within HeaderTimeout.
	conn.SetDeadline(time.Now().Add(s.cfg.HeaderTimeout))

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
			conn.SetWriteDeadline(time.Now().Add(time.Second)) // the old deadline has expired: give the error response a short new one
			response.Error(conn, http.StatusRequestTimeout)    //notify the client
		default:
			log.Println("Bad request from", conn.RemoteAddr(), ":", err)
			response.Error(conn, http.StatusBadRequest)
		}
		return
	}

	// Phase 2: the headers are in, so the client is active. A new, longer deadline
	// (measured from now, not from the connection start) covers reading the body in the
	// handler and writing the response.
	conn.SetDeadline(time.Now().Add(s.cfg.RequestTimeout))

	s.handler(conn, req)
}
