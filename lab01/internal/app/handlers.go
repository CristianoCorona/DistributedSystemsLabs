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

	data, contentType, err := a.files.Get(req.URL.Path)

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
		// Status 200 with data and contentType
		if writeErr := response.Write(conn, http.StatusOK, contentType, data); writeErr != nil {
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
		// Status 200
		if writeErr := response.Write(conn, http.StatusOK, "text/plain", []byte("File stored successfully.")); writeErr != nil {
			log.Println("write response:", writeErr) // The client may have closed the connection during the write; log server side.
		}
	}
}
