// Command http_server is a concurrent HTTP/1.0 file server and pub-sub broker built on TCP
// sockets.
//
// Usage:
//
//	http_server <port>
//
// It serves and stores files in ./data, relative to the working directory.
package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/app"
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/broker"
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/filestore"
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/server"
)

// docRoot is the directory from which files are served and in which they are stored.
// It is relative to the working directory of the process.
const docRoot = "./data"

// main checks the arguments, opens the document root and wires the layers together:
// the server (transport) calls the app (application), which uses the file store (service).
func main() {
	// Validation of the port (argument): exactly one argument, an integer from 1 to 65535.
	// Port 0 is excluded on purpose: net.Listen would pick a random free port, and the
	// clients would not know where to connect.
	if len(os.Args) != 2 {
		log.Fatal("Usage: ", os.Args[0], " <port>") // logs the error and exits with a non-zero status code
	}
	port, err := strconv.Atoi(os.Args[1])
	if err != nil || port < 1 || port > 65535 {
		log.Fatal("Usage: ", os.Args[0], " <port>")
	}

	// Open creates the document root if it does not exist yet and removes the
	// temporary files of an interrupted upload.
	files, err := filestore.Open(docRoot)
	if err != nil {
		log.Fatal("Cannot create document root: ", err)
	}

	// Each subscriber of the broker can have up to 64 undelivered messages: when its
	// queue is full, new messages of its topic are skipped for it.
	b := broker.New(64)
	// The app answers each request using the file store rooted at docRoot and the broker.
	application := app.New(files, b)
	cfg := server.Config{
		MaxConns:       10,               // the lab asks for at most 10 concurrent handlers
		HeaderTimeout:  5 * time.Second,  // a silent client loses its slot quickly
		RequestTimeout: 60 * time.Second, // should be enough for a large file, even on a slow connection
	}
	// The parsed number is used, not the raw argument, so "+80" or "080" become ":80".
	srv := server.New(":"+strconv.Itoa(port), cfg, application.Route)
	log.Fatal(srv.Serve())
}
