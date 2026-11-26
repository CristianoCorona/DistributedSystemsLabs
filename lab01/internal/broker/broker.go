// Package broker is the pub-sub service layer: it keeps the topics, their subscribers and
// each subscriber's queue of undelivered messages. It knows nothing about HTTP: callers
// translate its errors (ErrNotFound) into status codes.
//
// All the state is guarded by a single mutex, as the lab suggests. No operation blocks
// while holding it, so a waiting subscriber never delays the others.
package broker

import (
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned when a subscriber id is unknown, belongs to another topic, or
// was removed while its Wait was in progress.
var ErrNotFound = errors.New("unknown subscriber")

// Message is one published message, as delivered to a subscriber. The JSON tags give the
// field names used in the body of a GET /events response.
type Message struct {
	Seq  int    `json:"seq"`  // position of the message in its topic, starting at 1
	Body string `json:"body"` // body of the publish request
}

// subscriber is the state of one subscription.
type subscriber struct {
	// topic is the topic the subscription belongs to. Ids are unique across topics, so
	// it is used to reject a request that names the right id with the wrong topic.
	topic string
	// queue holds the messages not yet delivered, oldest first. Its capacity is
	// Broker.queueSize: Publish skips the subscriber when it is full, so it never blocks.
	// Unsubscribe closes it to wake up a Wait in progress.
	queue chan Message
}

// Broker is a pub-sub broker that delivers each message to every current subscriber of its
// topic, in publication order. It is safe for concurrent use by multiple goroutines.
type Broker struct {
	mu          sync.Mutex              // guards all the fields below
	nextID      int                     // last id given out; ids start at 1 and are never reused
	queueSize   int                     // capacity of each subscriber's queue
	subscribers map[int]*subscriber     // every current subscriber, by id
	topics      map[string]map[int]bool // the set of subscriber ids of each topic with subscribers
	seq         map[string]int          // last sequence number used in each topic
}

// New returns an empty Broker in which each subscriber can have up to queueSize
// undelivered messages. A queueSize below 1 is raised to 1.
func New(queueSize int) *Broker {
	if queueSize <= 0 {
		queueSize = 1
	}

	// Fields that are not listed (mu) start at their zero value, which is ready to use.
	// The maps must be created with make: writing to a nil map panics.
	return &Broker{
		nextID:      0,
		queueSize:   queueSize,
		subscribers: make(map[int]*subscriber),
		topics:      make(map[string]map[int]bool),
		seq:         make(map[string]int),
	}
}

// Subscribe registers a new subscriber on topic and returns its id. The subscriber
// receives only the messages published after this call.
func (b *Broker) Subscribe(topic string) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	// One counter for the whole broker, so the same client subscribing to two topics
	// gets two different ids.
	b.nextID++
	id := b.nextID

	b.subscribers[id] = &subscriber{
		topic: topic,
		queue: make(chan Message, b.queueSize),
	}
	// The first subscriber of a topic has no set of ids yet: reading a missing key gives
	// a nil map, so the set must be created before an id can be added to it.
	if b.topics[topic] == nil {
		b.topics[topic] = make(map[int]bool)
	}
	b.topics[topic][id] = true

	return id
}

// Unsubscribe removes subscriber id from topic. Its undelivered messages are discarded and
// a Wait in progress returns ErrNotFound at once. It returns ErrNotFound if id is unknown
// or belongs to another topic.
func (b *Broker) Unsubscribe(topic string, id int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// A valid id used with the wrong topic is treated as unknown.
	sub, ok := b.subscribers[id]
	if !ok || sub.topic != topic {
		return ErrNotFound
	}

	delete(b.subscribers, id)
	delete(b.topics[topic], id)
	if len(b.topics[topic]) == 0 {
		delete(b.topics, topic) // so that the map does not grow with every topic ever used
	}

	// Closing the queue wakes up a Wait blocked on it. This is safe because Publish only
	// sends while holding b.mu, and once the lock is released this subscriber can no
	// longer be found: nothing will ever send on the closed channel (which would panic).
	close(sub.queue)
	return nil
}

// Publish gives body the next sequence number of topic and appends the message to the
// queue of every current subscriber of topic. It never blocks: a subscriber whose queue is
// full misses the message.
func (b *Broker) Publish(topic, body string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// The sequence number is assigned and the message is queued in the same critical
	// section, so concurrent publishers are totally ordered and every subscriber sees
	// increasing sequence numbers.
	b.seq[topic]++
	msg := Message{
		Seq:  b.seq[topic],
		Body: body,
	}
	for id := range b.topics[topic] {
		queue := b.subscribers[id].queue
		// Publish is the only sender and holds b.mu, so the queue cannot fill up between
		// the check and the send.
		if len(queue) < b.queueSize {
			queue <- msg
		}
	}
}

// Wait returns the messages queued for subscriber id on topic, waiting up to timeout for
// the first one (long polling). When a message arrives, every other message already in
// the queue is returned with it. It returns (nil, nil) if nothing arrives in time, and
// ErrNotFound if id is unknown, belongs to another topic, or is removed while waiting.
func (b *Broker) Wait(topic string, id int, timeout time.Duration) ([]Message, error) {
	// Look up the queue under the lock, then release the lock BEFORE waiting: holding it
	// while blocked would freeze every other broker operation, including the Publish that
	// would wake us up. That is why there is no defer here: the unlock must happen in the
	// middle of the function, on both paths.
	b.mu.Lock()
	sub, ok := b.subscribers[id]
	if !ok || sub.topic != topic {
		b.mu.Unlock()
		return nil, ErrNotFound
	}
	queue := sub.queue
	b.mu.Unlock()

	// Block until a message arrives or the timeout expires.
	select {
	case msg, open := <-queue:
		// open is false when the queue was closed by Unsubscribe while we were waiting.
		if !open {
			return nil, ErrNotFound
		}
		messages := []Message{msg}

		// Take every other message already queued, without waiting for new ones:
		// select with default never blocks.
		for {
			select {
			case msg, open := <-queue:
				if !open {
					return messages, nil // unsubscribed while draining: return what we have
				}
				messages = append(messages, msg)
			default:
				return messages, nil // no more messages in the queue
			}
		}
	case <-time.After(timeout):
		return nil, nil // timeout, no messages received
	}
}
