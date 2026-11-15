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
		if write_err := writeResponse(conn, http.StatusOK, contentType, data); write_err != nil {
			log.Println("write response:", write_err) // The client may have closed the connection during the write; log server side.
		}
	}
}

func (a *app) storeFile(conn net.Conn, req *http.Request) {
	//TODO: stub implementation
	log.Println("storeFile called.")
}
