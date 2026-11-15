package main

import (
	"log"
	"net"
	"net/http"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/filestore"
)

// app is the main application struct. It holds the file store.
type app struct {
	files *filestore.Store
}

// knownMethods lists the HTTP methods defined by RFC 9110 and RFC 5789.
// Requests using any other method are rejected as malformed.
var knownMethods = map[string]bool{
	http.MethodGet:     true, // GET, known and implemented
	http.MethodPost:    true, // POST, known and implemented
	http.MethodPut:     true, // PUT, known but not implemented (501)
	http.MethodHead:    true, // HEAD, known but not implemented (501)
	http.MethodDelete:  true, // DELETE, known but not implemented (501)
	http.MethodPatch:   true, // PATCH, known but not implemented (501)
	http.MethodConnect: true, // CONNECT, known but not implemented (501)
	http.MethodOptions: true, // OPTIONS, known but not implemented (501)
	http.MethodTrace:   true, // TRACE, known but not implemented (501)
}

// route is the server's request handler: it picks the action for req
// based on its method and path.
func (a *app) route(conn net.Conn, req *http.Request) {
	// 0. Log the request received.
	log.Printf("Received request: %s %s", req.Method, req.URL.Path)
	// 1. unknown method -> 400
	if !knownMethods[req.Method] {
		writeError(conn, http.StatusBadRequest)
		return
	}
	// TODO: 2. (bonus) broker paths
	// 3. switch on req.Method for files
	switch req.Method {
	case http.MethodGet:
		a.serveFile(conn, req)
	case http.MethodPost:
		a.storeFile(conn, req)
	default:
		// Method present in the standard but not implemented in this lab.
		// Error 501 is returned.
		writeError(conn, http.StatusNotImplemented)
	}
}
