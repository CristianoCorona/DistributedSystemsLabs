package main

import (
	"errors"
	"log"
	"net"
	"net/http"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/filestore"
)

func (a *app) serveFile(conn net.Conn, req *http.Request) {

	data, contentType, err := a.files.Get(req.URL.Path)

	switch {
	case errors.Is(err, filestore.ErrBadExtension):
		// Error 400
		writeError(conn, http.StatusBadRequest)
	case errors.Is(err, filestore.ErrNotFound):
		// Error 404
		writeError(conn, http.StatusNotFound)
	case err != nil:
		// General error: log the error, then 400 as required by the lab
		log.Println("Error serving file:", err)
		writeError(conn, http.StatusBadRequest)
	default:
		// The file is found and can be served.
		// Status 200 with data and contentType
		if writeErr := writeResponse(conn, http.StatusOK, contentType, data); writeErr != nil {
			log.Println("write response:", writeErr) // The client may have closed the connection during the write; log server side.
		}
	}
}

func (a *app) storeFile(conn net.Conn, req *http.Request) {

	switch err := a.files.Put(req.URL.Path, req.Body); {
	case errors.Is(err, filestore.ErrBadExtension):
		// Error 400
		writeError(conn, http.StatusBadRequest)
	case err != nil:
		// General error: log the error, then 400 as required by the lab
		log.Println("Error storing file:", err)
		writeError(conn, http.StatusBadRequest)
	default:
		// The file is stored successfully.
		// Status 200
		if writeErr := writeResponse(conn, http.StatusOK, "text/plain", []byte("File stored successfully.")); writeErr != nil {
			log.Println("write response:", writeErr) // The client may have closed the connection during the write; log server side.
		}
	}
}
