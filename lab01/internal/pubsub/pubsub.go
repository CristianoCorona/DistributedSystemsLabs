package pubsub

import (
	"fmt"
	"log"
)

type BrokerConfig struct {
	MaxEventBufferSize int
}

type Broker struct {
	cfg BrokerConfig
	// Map to store subscribers for each topic
	topicSubscribers map[string]map[int]*Subscriber
	// Map to store locks for each topic
	topicLock map[string]chan struct{}
	// Lock for subscriber ID generation
	brokerLock chan struct{}
	// Counter for generating unique subscriber IDs
	subscriberIdCounter int
}

type Subscriber struct {
	// Channel to send events to the subscriber
	EventChannel chan []byte
	// Unique identifier for the subscriber
	Id int
}

func NewBroker() *Broker {
	return &Broker{
		cfg: BrokerConfig{
			MaxEventBufferSize: 10,
		},
		topicSubscribers:    make(map[string]map[int]*Subscriber),
		topicLock:           make(map[string]chan struct{}),
		brokerLock:          make(chan struct{}, 1),
		subscriberIdCounter: 0,
	}
}

func (b *Broker) NewSubscriber() *Subscriber {
	return &Subscriber{
		EventChannel: make(chan []byte, b.cfg.MaxEventBufferSize),
		Id:           b.nextSubscriberId(),
	}
}

func (b *Broker) Subscribe(topic string) *Subscriber {
	subscriber := b.NewSubscriber()
	if _, exists := b.topicLock[topic]; !exists { // Create a lock for the topic if it doesn't exist
		b.brokerLock <- struct{}{} // Make sure only one goroutine can create a lock for a topic at a time
		if _, exists := b.topicLock[topic]; !exists {
			b.topicLock[topic] = make(chan struct{}, 1)
			b.topicSubscribers[topic] = make(map[int]*Subscriber)
		}
		<-b.brokerLock
	}
	b.topicLock[topic] <- struct{}{}
	defer func() { <-b.topicLock[topic] }()
	b.topicSubscribers[topic][subscriber.Id] = subscriber
	return subscriber
}

func (b *Broker) Unsubscribe(topic string, subscriberId int) error {
	if _, exists := b.topicLock[topic]; !exists {
		return fmt.Errorf("topic does not exist") // Topic does not exist, nothing to do
	}
	b.topicLock[topic] <- struct{}{}
	defer func() { <-b.topicLock[topic] }()
	close(b.topicSubscribers[topic][subscriberId].EventChannel)
	delete(b.topicSubscribers[topic], subscriberId)
	return nil
}

func (b *Broker) Publish(topic string, event []byte) error { // TODO: the part about seq numbers
	if _, exists := b.topicLock[topic]; !exists {
		return fmt.Errorf("topic does not exist") // Topic does not exist, nothing to do
	}
	b.topicLock[topic] <- struct{}{}
	defer func() { <-b.topicLock[topic] }()
	for _, subscriber := range b.topicSubscribers[topic] {
		select {
		case subscriber.EventChannel <- event:
		default: // Ignore the channel for this event if the channel is full to avoid blocking
		}
	}
	return nil
}

func (b *Broker) GetEvent(topic string, subscriberId int) <-chan []byte {
	if _, exists := b.topicLock[topic]; !exists {
		log.Printf("Topic %s does not exist", topic)
		return nil
	}
	if _, exists := b.topicSubscribers[topic][subscriberId]; !exists {
		log.Printf("Subscriber %d does not exist for topic %s", subscriberId, topic)
		return nil
	}
	readChan := make(chan []byte, 1) // Create a new channel to return the event to the caller
	// Start goroutine to read from the event channel and send it to the readChan when an event is available
	// This prevents the caller from accessing the subscriber's event channel directly.
	log.Printf("Getting event for topic: %s, subscriberId: %d", topic, subscriberId)
	go func() {
		defer close(readChan) // Close the readChan when the goroutine exits to close the channel after the event is read
		readChan <- <-b.topicSubscribers[topic][subscriberId].EventChannel
	}()
	return readChan
}

func (b *Broker) nextSubscriberId() int { // TODO: improve ID generation (persistence etc.)
	b.brokerLock <- struct{}{}
	defer func() { <-b.brokerLock }()
	b.subscriberIdCounter++
	return b.subscriberIdCounter
}