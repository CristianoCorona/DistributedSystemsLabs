package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	rootDir       = "files"          // where files are served from and stored to
	maxConcurrent = 10               // max 10 handler goroutines at once
	readTimeout   = 10 * time.Second // limit how long a client may take to send its request
)

var contentTypes = map[string]string{
	".html": "text/html",
	".css":  "text/css",
	".gif":  "image/gif",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".txt":  "text/plain",
}

var notImplementedMethods = map[string]bool{
	"HEAD":    true,
	"PUT":     true,
	"DELETE":  true,
	"OPTIONS": true,
	"PATCH":   true,
	"TRACE":   true,
	"CONNECT": true,
}

func main() {
	log.Println("Hello, World!")

	if len(os.Args) < 2 {
		log.Println("Please provide a port number as an argument.")
		os.Exit(1)
	}

	port := os.Args[1]

	if err := os.MkdirAll(rootDir, 0755); err != nil {
		log.Fatalf("could not create root directory %s: %v", rootDir, err)
	}

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("could not listen on %s: %v", ":"+port, err)
	}
	defer ln.Close()

	log.Printf("Server is listening on %s, serving ./%s", port, rootDir)
	// Accept incoming connections in a loop

	//buffered semaphore channel to limit the number of concurrent connections
	sem := make(chan struct{}, maxConcurrent)

	for {
		conn, err := ln.Accept()

		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}
		sem <- struct{}{} // acquire a token

		go func(c net.Conn) {
			defer func() { <-sem }()
			handleConnection(c)
		}(conn)
	}
}

// helper function to send a complete HTTP 1.0 response to the client, including the status line, headers, and body
// body is []byte because it can be any type of data (jpeg, gif), not just text
func writeResponse(conn net.Conn, status string, contentType string, body []byte) {
	header := fmt.Sprintf("HTTP/1.0 %s\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n",
		status, contentType, len(body))

	if _, err := conn.Write([]byte(header)); err != nil {
		log.Printf("Write error to %s, %v", conn.RemoteAddr(), err)
		return
	}
	if _, err := conn.Write(body); err != nil {
		log.Printf("Write error to %s, %v", conn.RemoteAddr(), err)
	}

}

// badRequest sends a 400
// clients will parse the standard HTTP form "400 Bad Request" and display it to the user
func badRequest(conn net.Conn) {
	writeResponse(conn, "400 Bad Request", "text/plain", []byte("Bad Request (400)\n"))

}

// notImplemented sends a 501, same format as badRequest
func notImplemented(conn net.Conn) {
	writeResponse(conn, "501 Not Implemented", "text/plain", []byte("Not Implemented (501)\n"))
}

func handleConnection(conn net.Conn) {
	defer conn.Close() //close the connection when the function returns

	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) // Set a read timeout of 5 seconds

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		log.Printf("Bad Request from %s, %v", conn.RemoteAddr(), err)
		badRequest(conn)
		return
	}
	log.Printf("Received request from %s: %s %s", conn.RemoteAddr(), req.Method, req.URL.Path)

	// Check that CURRENTLY ONLY GET and POST are implemented, return 501 for other methods
	if req.Method != "GET" && req.Method != "POST" {
		if notImplementedMethods[req.Method] {
			notImplemented(conn)
		} else {
			badRequest(conn)
		}
		return
	}

	// before handling GET or POST, check file type, sanitize path, and check if file exists
	cleanPath := path.Clean("/" + req.URL.Path) // sanitize the path to prevent directory traversal attacks
	ext := strings.ToLower(path.Ext(cleanPath)) //toLower to make JPG into jpg, etc. for contentTypes map
	contentType, ok := contentTypes[ext]
	if !ok {
		badRequest(conn)
		return
	}
	fullPath := filepath.Join(rootDir, filepath.FromSlash(cleanPath))

	// GET: read the whole file and send it to the client
	if req.Method == "GET" {
		file, err := os.ReadFile(fullPath)
		if errors.Is(err, os.ErrNotExist) {
			writeResponse(conn, "404 Not Found", "text/plain", []byte("File not found (404)\n"))
			return
		}

		if err != nil {
			badRequest(conn)
			return
		}
		writeResponse(conn, "200 OK", contentType, file)
		return
	}

	// POST: read the request body and write it to the file atomically, using a temporary file and rename
	if req.Method == "POST" {

		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			badRequest(conn)
			return
		}

		tmpFile, err := os.CreateTemp(dir, ".upload-*")
		if err != nil {
			badRequest(conn)
			return
		}
		tmpName := tmpFile.Name()
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, writeErr := tmpFile.ReadFrom(req.Body)
		closeErr := tmpFile.Close()
		if writeErr != nil || closeErr != nil {
			os.Remove(tmpName)
			badRequest(conn)
			return
		}
		if err := os.Rename(tmpName, fullPath); err != nil {
			os.Remove(tmpName)
			badRequest(conn)
			return
		}
		writeResponse(conn, "201 Created", "text/plain", []byte("File uploaded successfully (201)\n"))

		return
	}
	notImplemented(conn)
}
