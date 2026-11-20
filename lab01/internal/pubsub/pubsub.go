package pubsub // TODO: rewrite using receivers in the same way as filestore

type Subscriber struct {
	// Channel to send events to the subscriber
	EventChannel chan []byte
	// Unique identifier for the subscriber
	Id int
}

var topicSubscribers = make(map[string]map[int]*Subscriber)
var topicLockLock = make(chan struct{}, 1)     // Lock for topicSubscribers map
var topicLock = make(map[string]chan struct{}) // Lock for each topic to synchronize access to subscribers
var idLock = make(chan struct{}, 1)            // Lock for subscriber ID generation
var subscriberIdCounter = 0

func Subscribe(topic string) *Subscriber {
	subscriber := &Subscriber{
		EventChannel: make(chan []byte, 10), // Buffered channel to hold events
		Id:           nextSubscriberId(),
	}
	if _, exists := topicLock[topic]; !exists { // Create a lock for the topic if it doesn't exist
		topicLockLock <- struct{}{} // Make sure only one goroutine can create a lock for a topic at a time
		if _, exists := topicLock[topic]; !exists {
			topicLock[topic] = make(chan struct{}, 1)
		}
		<-topicLockLock
	}
	topicLock[topic] <- struct{}{}
	defer func() { <-topicLock[topic] }()
	topicSubscribers[topic][subscriber.Id] = subscriber
	return subscriber
}

func Unsubscribe(topic string, subscriberId int) {
	if _, exists := topicLock[topic]; !exists {
		return // Topic does not exist, nothing to do
	}
	topicLock[topic] <- struct{}{}
	defer func() { <-topicLock[topic] }()
	close(topicSubscribers[topic][subscriberId].EventChannel)
	delete(topicSubscribers[topic], subscriberId)
}

func Publish(topic string, event []byte) { // TODO: the part about seq numbers
	if _, exists := topicLock[topic]; !exists {
		return // Topic does not exist, nothing to do
	}
	topicLock[topic] <- struct{}{}
	defer func() { <-topicLock[topic] }()
	for _, subscriber := range topicSubscribers[topic] {
		select {
		case subscriber.EventChannel <- event:
		default: // Ignore the channel for this event if the channel is full to avoid blocking
		}
	}
}

func GetEvent(topic string, subscriberId int) []byte {
	if _, exists := topicLock[topic]; !exists {
		return nil
	}
	return <-topicSubscribers[topic][subscriberId].EventChannel // TODO: release worker slot when channel is empty
}

func nextSubscriberId() int { // TODO: improve ID generation (persistence etc.)
	idLock <- struct{}{}
	defer func() { <-idLock }()
	subscriberIdCounter++
	return subscriberIdCounter
}