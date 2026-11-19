package app

import (
	"errors"
	"log"
	"net"
	"net/http"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/filestore"
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/response"
)

// serveFile answers a GET request with the content of the requested file.
func (a *App) serveFile(conn net.Conn, req *http.Request) {

	body, size, contentType, err := a.files.Get(req.URL.Path)

	switch {
	case errors.Is(err, filestore.ErrBadExtension):
		// Error 400
		response.Error(conn, http.StatusBadRequest)
	case errors.Is(err, filestore.ErrNotFound):
		// Error 404
		response.Error(conn, http.StatusNotFound)
	case err != nil:
		// General error: log the error, then 400 as required by the lab
		log.Println("Error serving file:", err)
		response.Error(conn, http.StatusBadRequest)
	default:
		// The file is found and can be served.
		// Status 200, streaming the file from disk to the client with its size and contentType.
		defer body.Close() // Get leaves the file open for the copy: closing it is our job
		if writeErr := response.Stream(conn, http.StatusOK, contentType, size, body); writeErr != nil {
			log.Println("write response:", writeErr) // The client may have closed the connection during the write; log server side.
		}
	}
}

// storeFile answers a POST request by storing its body as the requested file.
func (a *App) storeFile(conn net.Conn, req *http.Request) {

	switch err := a.files.Put(req.URL.Path, req.Body); {
	case errors.Is(err, filestore.ErrBadExtension):
		// Error 400
		response.Error(conn, http.StatusBadRequest)
	case err != nil:
		// General error: log the error, then 400 as required by the lab
		log.Println("Error storing file:", err)
		response.Error(conn, http.StatusBadRequest)
	default:
		// The file is stored successfully.
		// Status 201 Created: the body is now a resource that a later GET returns.
		if writeErr := response.Write(conn, http.StatusCreated, "text/plain", []byte("File stored successfully.")); writeErr != nil {
			log.Println("write response:", writeErr) // The client may have closed the connection during the write; log server side.
		}
	}
}

var cont = make(chan struct{}, 1) // TODO: remove, temporary simulation of waiting for event

func (a *App) getEvents(conn net.Conn, req *http.Request, releaseSlot func(), acquireSlot func()) {

	// No events to retrieve, release worker slot
	releaseSlot()
	log.Println("Released slot")
	
	cont <- struct{}{}
	cont <- struct{}{}

	// After 12 seconds, acquire a worker slot again to send the response
	log.Println("Trying to acquire slot")
	acquireSlot()
	log.Println("Slot acquired, sending response")

	if err := response.Write(conn, http.StatusOK, "text/plain", []byte("Event received after 12 seconds.")); err != nil {
		log.Println("write response:", err) // The client may have closed the connection during the write; log server side.
	}

	<-cont
}

func (a *App) publish(conn net.Conn, req *http.Request) {

	log.Println("Publishing event")
	<-cont
	log.Println("Event published, sending response")

	// Handle publish logic here
	if err := response.Write(conn, http.StatusOK, "text/plain", []byte("Published successfully.")); err != nil {
		log.Println("write response:", err) // The client may have closed the connection during the write; log server side.
	}
}
