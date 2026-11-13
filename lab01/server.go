package main

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

type Server struct {
	addr    string                                 // address to listen on, built by the caller (ip:port)
	sem     chan struct{}                          // buffered channel to limit the number of goroutines. struct{} is an empty struct, which takes up no memory.
	handler func(conn net.Conn, req *http.Request) // how a valid request is handled, to mimic a polymorphic approach.
	timeout time.Duration                          // timeout for the connections, in seconds
}

/*
** NewServer creates a new Server instance with the specified address, maximum number of connections and request handler function.
 */
func NewServer(addr string, maxConns int, h func(net.Conn, *http.Request), timeout time.Duration) *Server {
	return &Server{
		addr:    addr,
		sem:     make(chan struct{}, maxConns), //maxCons defines the maximum number of active connections.
		handler: h,
		timeout: timeout,
	}
}

/*
** Serve starts the server and listens for incoming connections.
** It accepts connections and handles them concurrently, while limiting the number of concurrent connections using a semaphore.
 */
func (s *Server) Serve() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		// Error in starting the server; return the error to the caller.
		return err
	}
	defer listener.Close() // Ensure the listener is closed when the function exits

	for {
		s.sem <- struct{}{} // Acquire a slot in the semaphore. If the channel is full, this will block until a slot is available.
		conn, err := listener.Accept()
		if err != nil {
			<-s.sem // Release the slot in the semaphore since we failed to accept a connection.
			if errors.Is(err, net.ErrClosed) {
				// Listener has been closed; exit the loop and terminate the server.
				return err
			}
			// Error in accepting the connection; log it but don't terminate the server.
			log.Println("Error accepting connection:", err)
			continue
		}

		go func() {
			defer func() { <-s.sem }() // Release the slot in the semaphore when done. We define a closure because defer wants a function call, not a channel operation.
			s.handleConn(conn)
		}()
	}

}

// handleConn reads one HTTP request from conn and passes it to the handler.
// Exactly one request per connection: the connection is always closed on return.

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close() // Ensure the connection is closed when the function exits

	// TODO: Implement connection handling logic here
	// 1. Set the timeout for the connection.
	conn.SetDeadline(time.Now().Add(s.timeout))

	// 2. Parse the HTTP request from the connection.
	reader := bufio.NewReader(conn) // http.ReadRequest requires a bufio.Reader, so we wrap the connection in one. This buffer reads the input data in chunks, which is more efficient than reading byte by byte.
	req, err := http.ReadRequest(reader)

	if err != nil {
		if errors.Is(err, io.EOF) {
			// Connection closed by the client without sending a request; log it and return.
			log.Println("Connection closed by client without sending a request")
			return
		}
		// General error in the request (malformed); error 400 Bad Request is sent back to the client.
		log.Println("Bad request from", conn.RemoteAddr(), ":", err)
		writeError(conn, http.StatusBadRequest)
		return
	}
	s.handler(conn, req) // Pass the valid request to the handler function for processing.

}
