// Package app is the application layer: it decides what to do with a parsed request and maps
// the errors of the file store to HTTP status codes. It does not know how connections are
// accepted (that is the server package) nor how files are laid out on disk (that is the
// filestore package).
package app

import (
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/filestore"
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/pubsub"
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/response"
)

// App is the main application struct. It holds the file store.
type App struct {
	files *filestore.Store
	pubsub *pubsub.Broker
}

// New returns an App that serves and stores files in files.
func New(files *filestore.Store, pubsub *pubsub.Broker) *App {
	return &App{
		files: files,
		pubsub: pubsub,
	}
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
func (a *App) Route(conn net.Conn, req *http.Request, releaseSlot func(), acquireSlot func()) {
	// 0. Log the request received.
	log.Printf("Received request: %s %s", req.Method, req.URL.Path)
	// 1. unknown method -> 400
	if !knownMethods[req.Method] {
		response.Error(conn, http.StatusBadRequest)
		return
	}
	// TODO: 2. (bonus) broker paths
	// 3. switch on req.Method for files
	switch req.Method {
	case http.MethodGet:
		switch {
			case strings.HasPrefix(req.URL.Path, "/events"):
				a.getEvent(conn, req, releaseSlot, acquireSlot)
			default:
				a.serveFile(conn, req)
		}
	case http.MethodPost:
		switch {
			case strings.HasPrefix(req.URL.Path, "/subscribe"):
				a.subscribe(conn, req)
			case strings.HasPrefix(req.URL.Path, "/unsubscribe"):
				a.unsubscribe(conn, req)
			case strings.HasPrefix(req.URL.Path, "/publish"):
				a.publish(conn, req)
			default:
				a.storeFile(conn, req)
		}
	default:
		// Method present in the standard but not implemented in this lab.
		// Error 501 is returned.
		response.Error(conn, http.StatusNotImplemented)
	}
}
