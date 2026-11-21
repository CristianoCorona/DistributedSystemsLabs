package app

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/broker"
	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/response"
)

// pollTimeout is how long a GET /events request waits for a message before it is answered
// with 204 No Content. The client is then expected to poll again. It must stay below
// RequestTimeout (set in main), or the deadline would expire before the answer is written.
const pollTimeout = 15 * time.Second

// topicParam returns the topic query parameter. ok is false if it is missing or empty.
func topicParam(req *http.Request) (topic string, ok bool) {
	topic = req.URL.Query().Get("topic")
	return topic, topic != ""
}

// idParam returns the id query parameter. ok is false if it is missing, not an integer or
// below 1, the first id the broker gives out.
func idParam(req *http.Request) (int, bool) {
	id, err := strconv.Atoi(req.URL.Query().Get("id"))
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

// writeJSON answers with status and v encoded as JSON.
func writeJSON(conn net.Conn, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Println("Cannot marshal JSON:", err)
		response.Error(conn, http.StatusBadRequest)
		return
	}
	if err := response.Write(conn, status, "application/json", body); err != nil {
		log.Println("Cannot write response:", err) // The client may have closed the connection during the write; log server side.
	}
}

// writeMessage answers with status and text as a plain-text body.
func writeMessage(conn net.Conn, status int, text string) {
	if err := response.Write(conn, status, "text/plain", []byte(text)); err != nil {
		log.Println("write response:", err) // The client may have closed the connection during the write; log server side.
	}
}

// subscribe answers POST /subscribe?topic=X: 201 Created with the new subscriber's id as
// {"id": N}, or 400 if the topic is missing. The id is unique across all topics, and the
// subscriber uses it in its later requests.
func (a *App) subscribe(conn net.Conn, req *http.Request) {
	topic, ok := topicParam(req)
	if !ok {
		response.Error(conn, http.StatusBadRequest)
		return
	}
	id := a.broker.Subscribe(topic)
	writeJSON(conn, http.StatusCreated, map[string]int{"id": id})
}

// unsubscribe answers POST /unsubscribe?topic=X&id=N: 200 OK once the subscriber is
// removed, 400 if a parameter is missing or invalid, 404 if the subscriber is unknown or
// belongs to another topic.
func (a *App) unsubscribe(conn net.Conn, req *http.Request) {
	topic, ok := topicParam(req)
	if !ok {
		response.Error(conn, http.StatusBadRequest)
		return
	}
	id, ok := idParam(req)
	if !ok {
		response.Error(conn, http.StatusBadRequest)
		return
	}

	switch err := a.broker.Unsubscribe(topic, id); {
	case errors.Is(err, broker.ErrNotFound):
		// Error 404
		response.Error(conn, http.StatusNotFound)
	case err != nil:
		// General error: log the error, then 400 as required by the lab
		log.Println("Error unsubscribing:", err)
		response.Error(conn, http.StatusBadRequest)
	default:
		writeMessage(conn, http.StatusOK, "Unsubscribed.")
	}
}

// publish answers POST /publish?topic=X by sending the request body to every current
// subscriber of X, except those whose queue is full: 200 OK, also when the topic has no
// subscribers, or 400 if the topic is missing or the body cannot be read.
func (a *App) publish(conn net.Conn, req *http.Request) {
	topic, ok := topicParam(req)
	if !ok {
		response.Error(conn, http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		// For example, the client sent fewer bytes than its Content-Length announced.
		log.Println("Error reading message:", err)
		response.Error(conn, http.StatusBadRequest)
		return
	}
	a.broker.Publish(topic, string(body))
	writeMessage(conn, http.StatusOK, "Published.")
}

// events answers GET /events?topic=X&id=N with long polling: 200 OK with the queued
// messages as a JSON array as soon as there is at least one, 204 No Content if none
// arrives within pollTimeout, 400 if a parameter is missing or invalid, 404 if the
// subscriber is unknown or unsubscribes while waiting.
//
// The connection's slot is released before waiting: a request that only waits is not
// doing any work, and the lab allows it to be excluded from the connection limit.
func (a *App) events(conn net.Conn, req *http.Request, release func()) {
	topic, ok := topicParam(req)
	if !ok {
		response.Error(conn, http.StatusBadRequest)
		return
	}
	id, ok := idParam(req)
	if !ok {
		response.Error(conn, http.StatusBadRequest)
		return
	}

	release() // the parameters are valid: from here on the handler only waits

	messages, err := a.broker.Wait(topic, id, pollTimeout)
	switch {
	case errors.Is(err, broker.ErrNotFound):
		// Error 404
		response.Error(conn, http.StatusNotFound)
	case err != nil:
		// General error: log the error, then 400 as required by the lab
		log.Println("Error waiting for messages:", err)
		response.Error(conn, http.StatusBadRequest)
	case len(messages) == 0:
		// Timeout: no message arrived.
		writeMessage(conn, http.StatusNoContent, "")
	default:
		writeJSON(conn, http.StatusOK, messages)
	}
}
