package broker

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// short is the timeout used when a test expects Wait to return at once or to time out:
// the tests never need the real 15-second long-poll timeout.
const short = 50 * time.Millisecond

// TestSubscribeGivesUniqueIDs checks that every subscription gets a different id, also across
// topics, so that two subscriptions never share a queue.
func TestSubscribeGivesUniqueIDs(t *testing.T) {
	b := New(10)
	// The same client subscribing to two topics must get two different ids.
	a := b.Subscribe("news")
	c := b.Subscribe("sports")
	d := b.Subscribe("news")
	if a == c || a == d || c == d {
		t.Errorf("ids are not unique: %d, %d, %d", a, c, d)
	}
}

// TestPublishThenWait checks that one Wait returns every message already queued, oldest first,
// with sequence numbers starting at 1.
func TestPublishThenWait(t *testing.T) {
	b := New(10)
	id := b.Subscribe("news")
	b.Publish("news", "first")
	b.Publish("news", "second")

	// Both messages are already queued, so one Wait returns both, in order.
	got, err := b.Wait("news", id, short)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	want := []Message{{Seq: 1, Body: "first"}, {Seq: 2, Body: "second"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestWaitTimesOut checks that Wait returns no messages and no error once the timeout passes,
// and not before: this is what becomes the 204 answer of a long poll.
func TestWaitTimesOut(t *testing.T) {
	b := New(10)
	id := b.Subscribe("news")

	start := time.Now()
	got, err := b.Wait("news", id, short)
	if err != nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, nil)", got, err)
	}
	if time.Since(start) < short {
		t.Errorf("Wait returned before the timeout")
	}
}

// TestWaitWakesUpOnPublish checks that a Wait that is already blocked returns as soon as a message
// is published, long before its timeout. This is the core of long polling.
func TestWaitWakesUpOnPublish(t *testing.T) {
	b := New(10)
	id := b.Subscribe("news")

	// Publish while Wait is already blocked: the message must arrive long before the timeout.
	go func() {
		time.Sleep(20 * time.Millisecond)
		b.Publish("news", "hello")
	}()
	got, err := b.Wait("news", id, 5*time.Second)
	if err != nil || len(got) != 1 || got[0].Body != "hello" {
		t.Errorf("got (%v, %v), want one message \"hello\"", got, err)
	}
}

// TestOnlyMessagesOfOwnTopic checks that a subscriber does not receive messages published to
// another topic.
func TestOnlyMessagesOfOwnTopic(t *testing.T) {
	b := New(10)
	id := b.Subscribe("news")
	b.Publish("sports", "goal")

	if got, _ := b.Wait("news", id, short); got != nil {
		t.Errorf("received %v from another topic", got)
	}
}

// TestOnlyMessagesAfterSubscribing checks that a subscriber receives only the messages published
// after it subscribed, as the lab requires.
func TestOnlyMessagesAfterSubscribing(t *testing.T) {
	b := New(10)
	b.Publish("news", "old")
	id := b.Subscribe("news")
	b.Publish("news", "new")

	got, _ := b.Wait("news", id, short)
	if len(got) != 1 || got[0].Body != "new" {
		t.Errorf("got %v, want only the message published after subscribing", got)
	}
}

// TestUnknownSubscriber checks that an unknown id, or a valid id used with the wrong topic, gives
// ErrNotFound (404) in Wait and Unsubscribe.
func TestUnknownSubscriber(t *testing.T) {
	b := New(10)
	id := b.Subscribe("news")

	if _, err := b.Wait("news", id+100, short); !errors.Is(err, ErrNotFound) {
		t.Errorf("Wait with unknown id: err = %v, want ErrNotFound", err)
	}
	// A valid id used with the wrong topic is treated as unknown.
	if _, err := b.Wait("sports", id, short); !errors.Is(err, ErrNotFound) {
		t.Errorf("Wait with wrong topic: err = %v, want ErrNotFound", err)
	}
	if err := b.Unsubscribe("sports", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Unsubscribe with wrong topic: err = %v, want ErrNotFound", err)
	}
}

// TestUnsubscribe checks that after Unsubscribe the subscriber is gone: Publish does not panic
// on its closed queue, and Wait and a second Unsubscribe give ErrNotFound.
func TestUnsubscribe(t *testing.T) {
	b := New(10)
	id := b.Subscribe("news")

	if err := b.Unsubscribe("news", id); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	b.Publish("news", "after") // must not panic, and must not reach the removed subscriber
	if _, err := b.Wait("news", id, short); !errors.Is(err, ErrNotFound) {
		t.Errorf("Wait after Unsubscribe: err = %v, want ErrNotFound", err)
	}
	if err := b.Unsubscribe("news", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Unsubscribe: err = %v, want ErrNotFound", err)
	}
}

// TestUnsubscribeWakesWaiter checks that Unsubscribe wakes a Wait in progress with ErrNotFound at
// once, so that a waiting poll gets 404 instead of hanging until its timeout.
func TestUnsubscribeWakesWaiter(t *testing.T) {
	b := New(10)
	id := b.Subscribe("news")

	go func() {
		time.Sleep(20 * time.Millisecond)
		b.Unsubscribe("news", id)
	}()
	start := time.Now()
	_, err := b.Wait("news", id, 5*time.Second)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("Wait was not woken up by Unsubscribe")
	}
}

// TestPublishNeverBlocks checks that Publish never blocks on a full queue: the subscriber keeps
// the first messages and misses the rest. It matters because one slow subscriber must not
// stop every publisher.
func TestPublishNeverBlocks(t *testing.T) {
	b := New(2)
	id := b.Subscribe("news")

	// The queue holds 2 messages. If Publish blocked on a full queue, this loop would
	// never finish and the test would time out.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Publish("news", "msg")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a full queue")
	}

	// The subscriber got the first 2 messages and missed the rest.
	got, _ := b.Wait("news", id, short)
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Errorf("got %v, want sequence numbers 1 and 2", got)
	}
}

// TestConcurrentPublishersKeepOrder checks that many concurrent publishers are totally ordered:
// every subscriber gets all sequence numbers in order, with no gaps or duplicates. With
// -race it also checks that the broker state is properly locked.
func TestConcurrentPublishersKeepOrder(t *testing.T) {
	const publishers, perPublisher = 10, 50
	b := New(publishers * perPublisher)
	ids := []int{b.Subscribe("news"), b.Subscribe("news")}

	var wg sync.WaitGroup
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				b.Publish("news", "msg")
			}
		}()
	}
	wg.Wait()

	// Every subscriber must see 1, 2, 3, ... with no gaps, duplicates or reordering.
	for _, id := range ids {
		got, err := b.Wait("news", id, short)
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
		if len(got) != publishers*perPublisher {
			t.Fatalf("subscriber %d got %d messages, want %d", id, len(got), publishers*perPublisher)
		}
		for i, m := range got {
			if m.Seq != i+1 {
				t.Fatalf("subscriber %d: message %d has Seq %d, want %d", id, i, m.Seq, i+1)
			}
		}
	}
}
