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
	mu          sync.Mutex
	nextID      int
	queueSize   int
	subscribers map[int]*subscriber
	topics      map[string]map[int]bool
	seq         map[string]int
}

func New(queueSize int) *Broker {

	if queueSize <= 0 {
		queueSize = 1
	}

	return &Broker{
		nextID:      0,
		queueSize:   queueSize,
		subscribers: make(map[int]*subscriber),
		topics:      make(map[string]map[int]bool),
		seq:         make(map[string]int),
	}

}

func (b *Broker) Subscribe(topic string) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	id := b.nextID

	b.subscribers[id] = &subscriber{
		topic: topic,
		queue: make(chan Message, b.queueSize),
	}
	if b.topics[topic] == nil {
		b.topics[topic] = make(map[int]bool)
	}
	b.topics[topic][id] = true

	return id
}

func (b *Broker) Unsubscribe(topic string, id int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub, ok := b.subscribers[id]
	if !ok || sub.topic != topic {
		return ErrNotFound
	}

	delete(b.subscribers, id)
	delete(b.topics[topic], id)
	if len(b.topics[topic]) == 0 {
		delete(b.topics, topic) // to prevent map from growing indefinitely
	}

	close(sub.queue) // close the channel to signal the subscriber
	return nil
}

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
func (b *Broker) Wait(topic string, id int, timeout time.Duration) ([]Message, error) {
	b.mu.Lock()
	sub, ok := b.subscribers[id]

	if !ok || sub.topic != topic {
		b.mu.Unlock()
		return nil, ErrNotFound
	}
	queue := sub.queue
	b.mu.Unlock()

	select {
	case msg, open := <-queue:
		if !open {
			return nil, ErrNotFound // channel closed, subscriber unsubscribed
		}
		messages := []Message{msg}

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
