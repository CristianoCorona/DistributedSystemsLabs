// Package app is the application layer: it decides what to do with a parsed request and maps
// the errors of the file store to HTTP status codes. It does not know how connections are
// accepted (that is the server package) nor how files are laid out on disk (that is the
// filestore package).
package app

import (
	"log"
	"net"
	"net/http"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/broker" // new import 
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/filestore"
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/response"
)

// App is the main application struct. It holds the file store.
type App struct {
	files  *filestore.Store
	broker *broker.Broker //now uses a broker field 
}

// New returns an App that serves and stores files in files.
// added a b parameter to intialize the broker field
func New(files *filestore.Store, b *broker.Broker) *App {
	return &App{files: files, broker: b}
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

// Route is the server's request handler: it picks the action for req
// based on its method and path.
// release func() parameter has been added to allow the handler to release the connection slot before returning, 
// so that a long poll does not count towards MaxConns. Calling it is optional, 
// and calling it more than once is harmless because the slot is returned exactly once.
func (a *App) Route(conn net.Conn, req *http.Request, release func()) {
	// 0. Log the request received.
	log.Printf("Received request: %s %s", req.Method, req.URL.Path)
	// 1. unknown method -> 400
	if !knownMethods[req.Method] {
		response.Error(conn, http.StatusBadRequest)
		return
	}
	// TODO: 2. (bonus) broker paths implemented
	// file extension, so the file store would reject them with 400.
	// subscribe, unsubscribe, publish and events 
	switch req.URL.Path {
	case "/subscribe":
		if requireMethod(conn, req, http.MethodPost) {
			a.subscribe(conn, req)
		}
		return
	case "/unsubscribe":
		if requireMethod(conn, req, http.MethodPost) {
			a.unsubscribe(conn, req)
		}
		return
	case "/publish":
		if requireMethod(conn, req, http.MethodPost) {
			a.publish(conn, req)
		}
		return
	case "/events":
		if requireMethod(conn, req, http.MethodGet) {
			a.events(conn, req, release)
		}
		return
	}
	// 3. switch on req.Method for files
	//
	switch req.Method {
	case http.MethodGet:
		a.serveFile(conn, req)
	case http.MethodPost:
		a.storeFile(conn, req)
	default:
		// Method present in the standard but not implemented in this lab.
		// Error 501 is returned.
		response.Error(conn, http.StatusNotImplemented)
	}
}

// requireMethod reports whether req uses method. If it does not, it answers
// 501 Not Implemented, as the lab asks for other methods on the broker paths.
func requireMethod(conn net.Conn, req *http.Request, method string) bool {
	if req.Method != method {
		response.Error(conn, http.StatusNotImplemented)
		return false
	}
	return true
}
