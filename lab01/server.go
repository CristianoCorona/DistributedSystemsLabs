package main

import (
	"errors"
	"log"
	"net"
	"net/http"
)

// TODO: implement a timeout for the connections to avoid stalling at maxCons

type Server struct {
	addr    string                                 // address to listen on, built by the caller (ip:port)
	sem     chan struct{}                          // buffered channel to limit the number of goroutines. struct{} is an empty struct, which takes up no memory.
	handler func(conn net.Conn, req *http.Request) // how a valid request is handled, to mimic a polymorphic approach.
}

/*
** NewServer creates a new Server instance with the specified address, maximum number of connections and request handler function.
 */
func NewServer(addr string, maxConns int, h func(net.Conn, *http.Request)) *Server {
	return &Server{
		addr:    addr,
		sem:     make(chan struct{}, maxConns), //maxCons defines the maximum number of active connections.
		handler: h,
	}
}

/*
** Serve starts the server and listens for incoming connections.
** It accepts connections and handles them concurrently, while limiting the number of concurrent connections using a semaphore.
** TODO: It also timeouts connections after the defined timeout of the server instance.
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

/*
** handleConn handles an individual connection.
** It reads the request from the connection, and if it's valid, it invokes the handler function provided during server creation.
 */
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close() // Ensure the connection is closed when the function exits

	// TODO: Implement connection handling logic here
}
