package broker

import (
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned when a subscriber id is unknown, belongs to another topic, or
// was removed while its Wait was in progress.
var ErrNotFound = errors.New("unknown subscriber")

type Message struct {
	Seq  int
	Body string
}

type subscriber struct {
	topic string
	queue chan Message
}

type Broker struct {
	mu          sync.Mutex // protects the fields below
	nextID      int // last id issued, incremented for each new subscriber
	queueSize   int //capacity of each subscriber's queue
	subscribers map[int]*subscriber // id -> subscriber
	topics      map[string]map[int]bool //topic -> set of subscriber ids
	seq         map[string]int // topic -> last sequence number published
}


// New returns a new Broker with the given queue size for each subscriber. 
// If queueSize is 0 or negative, it defaults to 1.
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

// registers a new subscriber on topic and returns its id.
// The subscriber receives only the messages published after this call. 
func (b *Broker) Subscribe(topic string) int {
	b.mu.Lock()
	defer b.mu.Unlock() // runs when the subscriber returns


	// One counter for the whole broker, so the same client subscribing to two topics
	// gets two different ids.
	b.nextID++
	id := b.nextID

	b.subscribers[id] = &subscriber{
		topic: topic,
		queue: make(chan Message, b.queueSize), // bounded queue for the subscriber
	}
	// The first subscriber of a topic has no set of ids yet: reading a missing key gives
	// a nil map, so the set must be created before an id can be added to it.
	if b.topics[topic] == nil {
		b.topics[topic] = make(map[int]bool)
	}
	b.topics[topic][id] = true

	return id
}

// removes subscriber id from topic. It returns ErrNotFound if the id is
// unknown or belongs to another topic. A Wait in progress for this subscriber returns
// ErrNotFound at once, because its queue is closed.
func (b *Broker) Unsubscribe(topic string, id int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// A valid id used with the wrong topic is treated as unknown (404).
	sub, ok := b.subscribers[id]
	if !ok || sub.topic != topic {
		return ErrNotFound
	}
	// remove the subscriber from both maps 
	delete(b.subscribers, id)
	delete(b.topics[topic], id)
	if len(b.topics[topic]) == 0 {
		delete(b.topics, topic) // to prevent map from growing indefinitely
	}

	
	// Closing the queue wakes up a Wait blocked on it. This is safe because Publish
	// only sends while holding b.mu, and once the lock is released this subscriber can
	// no longer be found: nothing will ever send on the closed channel (which would panic).
	close(sub.queue) 
	return nil
}

// Publish gives body the next sequence number of topic and delivers it to every current
// subscriber of topic. It never blocks: a subscriber whose queue is full is skipped.
func (b *Broker) Publish(topic, body string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.seq[topic]++
	msg := Message{
		Seq:  b.seq[topic],
		Body: body,
	}
	for id := range b.topics[topic] {
		queue := b.subscribers[id].queue
		if len(queue) < b.queueSize {
			queue <- msg // send the message to the subscriber's queue
		}
		// else if the queue is full, the message is dropped for that subscriber.
	}

}

// Wait returns the messages queued for subscriber id, waiting up to timeout for the first
// one (long polling). When a message arrives, every other message already in the queue is
// returned with it. It returns (nil, nil) if nothing arrives in time, and ErrNotFound if
// the subscriber is unknown, belongs to another topic, or is removed while waiting.
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

	//block until a message arrives or the timeout expires. If a message arrives, drain the rest of the queue.
	select {
	case msg, open := <-queue:
		//  open is false when the queue was closed by Unsubscribe while we were waiting.
		if !open {
			return nil, ErrNotFound // channel closed, subscriber unsubscribed
		}
		messages := []Message{msg}
		

		// Take every other message already queued, without waiting for new ones:
		// select with default never blocks.
		for {
			select {
			case msg, open := <-queue:
				if !open {
					return messages, nil // channel closed, return what we have
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
