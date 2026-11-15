package main

import (
	"log"
	"os"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/filestore"
)

// Dummy test main
func main() {
	// Validation of the port (argument)
	if len(os.Args) != 2 {
		log.Fatal("Usage: ", os.Args[0], " <port>") // logs the error and exits with a non-zero status code
	}
	port := os.Args[1]

	// Dummy handler that responds with a simple message indicating the requested method and path.
	my_app := &app{files: filestore.New("./data")} // Initialize the app with a file store rooted at the current directory
	srv := NewServer(":"+port, 10, my_app.route, 5)
	log.Fatal(srv.Serve())
}
