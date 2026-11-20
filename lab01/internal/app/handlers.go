package app

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"

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

func (a *App) getEvent(conn net.Conn, req *http.Request, releaseSlot func(), acquireSlot func()) {

	if req.URL.Query().Get("id") == "" {
		log.Println("Missing request ID")
		response.Error(conn, http.StatusBadRequest)
		return
	}
	reqId, err := strconv.Atoi(req.URL.Query().Get("id"))
	if err != nil {
		log.Println("Invalid request ID:", err)
		response.Error(conn, http.StatusBadRequest)
		return
	}
	topic := req.URL.Query().Get("topic")
	if topic == "" {
		log.Println("Missing topic parameter")
		response.Error(conn, http.StatusBadRequest)
		return
	}
	eventChan := a.pubsub.GetEvent(topic, reqId)
	if eventChan == nil {
		log.Println("No events available for topic:", topic, "and request ID:", reqId)
		response.Error(conn, http.StatusBadRequest)
		return
	}
	var event []byte
	select {
	case event = <-eventChan:
		log.Println("Event received:", string(event))
	default:
		log.Println("No events available, releasing slot")
		releaseSlot()

		timeoutChan := make(chan struct{})
		errChan := make(chan error, 1)
		// Start a goroutine that blocks trying to read from the connection until the timeout occurs
		// When the timeout occurs, the Read call will unblock and the goroutine will send a signal to the timeoutChan
		go func() {
			_, err := conn.Read(nil)
			if err == nil {
				errChan <- errors.New("unexpected read success")
				return
			}
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				errChan <- err
				return
			}
			timeoutChan <- struct{}{}
		}()

		select {
		case event = <-eventChan:
			acquireSlot()
			log.Println("Event received after waiting:", string(event))
		case <-timeoutChan:
			acquireSlot()
			log.Println("Timeout waiting for event")
			response.Error(conn, http.StatusNotFound)
			return
		case err := <-errChan:
			acquireSlot()
			log.Println("Error while waiting for event:", err)
			response.Error(conn, http.StatusInternalServerError)
			return
		}
	}
	if err := response.Write(conn, http.StatusOK, "text/plain", event); err != nil {
		log.Println("write response:", err) // The client may have closed the connection during the write; log server side.
	}
}

func (a *App) subscribe(conn net.Conn, req *http.Request) {
	// Handle subscription logic here
	topic := req.URL.Query().Get("topic")
	if topic == "" {
		log.Println("Missing topic parameter")
		response.Error(conn, http.StatusBadRequest)
		return
	}
	subscriber := a.pubsub.Subscribe(topic)
	data := []byte("{ \"id\": \"" + strconv.Itoa(subscriber.Id) + "\" }")
	if err := response.Write(conn, http.StatusCreated, "text/plain", data); err != nil {
		log.Println("write response:", err) // The client may have closed the connection during the write; log server side.
	}
}

func (a *App) unsubscribe(conn net.Conn, req *http.Request) {
	// Handle unsubscription logic here
	subscriberId, err := strconv.Atoi(req.URL.Query().Get("id"))
	if err != nil {
		log.Println("Invalid subscriber ID:", err)
		response.Error(conn, http.StatusBadRequest)
		return
	}
	topic := req.URL.Query().Get("topic")
	if topic == "" {
		log.Println("Missing topic parameter")
		response.Error(conn, http.StatusBadRequest)
		return
	}
	if err := a.pubsub.Unsubscribe(topic, subscriberId); err != nil {
		log.Println("Error unsubscribing:", err)
		response.Error(conn, http.StatusBadRequest)
		return
	}
	if err := response.Write(conn, http.StatusOK, "text/plain", []byte("Unsubscribed successfully.")); err != nil {
		log.Println("write response:", err) // The client may have closed the connection during the write; log server side.
	}
}

func (a *App) publish(conn net.Conn, req *http.Request) {

	// Handle publish logic here

	topic := req.URL.Query().Get("topic")
	if topic == "" {
		log.Println("Missing topic parameter")
		response.Error(conn, http.StatusBadRequest)
		return
	}
	eventData, err := io.ReadAll(req.Body)
	if err != nil {
		log.Println("Error reading request body:", err)
		response.Error(conn, http.StatusInternalServerError)
		return
	}
	if err := a.pubsub.Publish(topic, eventData); err != nil {
		log.Println("Error publishing event:", err)
		response.Error(conn, http.StatusInternalServerError)
		return
	}

	if err := response.Write(conn, http.StatusOK, "text/plain", []byte("Published successfully.")); err != nil {
		log.Println("write response:", err) // The client may have closed the connection during the write; log server side.
	}
}
