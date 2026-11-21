//pubsub knows nothing about HTTP: it only implements the publish-subscribe logic. 
// The application layer translates its errors into status codes.
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

const pollTimeout = 15 * time.Second

func topicParam(req *http.Request) (topic string, ok bool) {
	topic = req.URL.Query().Get("topic")
	return topic, topic != ""
}

func idParam(req *http.Request) (int, bool) {
	id, err := strconv.Atoi(req.URL.Query().Get("id"))
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

func writeJSON(conn net.Conn, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Println("Cannot marshal JSON:", err)
		response.Error(conn, http.StatusBadRequest)
		return
	}
	if err := response.Write(conn, status, "application/json", body); err != nil {
		log.Println("Cannot write response:", err)
	}
}

// writes a plain text response with the given status and text.
func writeMessage(conn net.Conn, status int, text string) {
	if err := response.Write(conn, status, "text/plain", []byte(text)); err != nil {
		log.Println("write response:", err)
	}
}


// one subscriber and its bounded queue per topic. The broker is safe for concurrent use by multiple goroutines.
func (a *App) subscribe(conn net.Conn, req *http.Request) {
	topic, ok := topicParam(req)
	if !ok {
		response.Error(conn, http.StatusBadRequest)
		return
	}
	id := a.broker.Subscribe(topic) // the id is unique across all topics, so it can be used to identify the subscriber in future requests
	writeJSON(conn, http.StatusCreated, map[string]int{"id": id})

}

// removes a subscriber from a topic. The id must be the one returned by Subscribe for that topic.
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
		response.Error(conn, http.StatusNotFound)
	case err != nil:
		log.Println("Error unsubscribing:", err)
		response.Error(conn, http.StatusBadRequest)
	default:
		writeMessage(conn, http.StatusOK, "Unsubscribed.")
	}
}

// publishes a message to a topic. All subscribers of that topic will receive the message, unless their queue is full.
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

// waits for messages for a subscriber of a topic. The id must be the one returned by Subscribe for that topic.
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

	release() // the slot is no longer needed: the handler will wait for messages, which does not count towards MaxConns

	messages, err := a.broker.Wait(topic, id, pollTimeout)
	switch {
	case errors.Is(err, broker.ErrNotFound):
		response.Error(conn, http.StatusNotFound)
	case err != nil:
		log.Println("Error waiting for messages:", err)
		response.Error(conn, http.StatusBadRequest)
	case len(messages) == 0:
		writeMessage(conn, http.StatusNoContent, "")
	default:
		writeJSON(conn, http.StatusOK, messages)
	}
}
