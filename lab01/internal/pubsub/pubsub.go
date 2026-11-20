package pubsub

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

func (b *Broker) Unsubscribe(topic string, subscriberId int) {
	if _, exists := b.topicLock[topic]; !exists {
		return // Topic does not exist, nothing to do
	}
	b.topicLock[topic] <- struct{}{}
	defer func() { <-b.topicLock[topic] }()
	close(b.topicSubscribers[topic][subscriberId].EventChannel)
	delete(b.topicSubscribers[topic], subscriberId)
}

func (b *Broker) Publish(topic string, event []byte) { // TODO: the part about seq numbers
	if _, exists := b.topicLock[topic]; !exists {
		return // Topic does not exist, nothing to do
	}
	b.topicLock[topic] <- struct{}{}
	defer func() { <-b.topicLock[topic] }()
	for _, subscriber := range b.topicSubscribers[topic] {
		select {
		case subscriber.EventChannel <- event:
		default: // Ignore the channel for this event if the channel is full to avoid blocking
		}
	}
}

func (b *Broker) GetEvent(topic string, subscriberId int) []byte {
	if _, exists := b.topicLock[topic]; !exists {
		return nil
	}
	return <-b.topicSubscribers[topic][subscriberId].EventChannel // TODO: release worker slot when channel is empty
}

func (b *Broker) nextSubscriberId() int { // TODO: improve ID generation (persistence etc.)
	b.brokerLock <- struct{}{}
	defer func() { <-b.brokerLock }()
	b.subscriberIdCounter++
	return b.subscriberIdCounter
}