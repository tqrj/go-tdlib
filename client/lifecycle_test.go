package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// A single global producer must keep reaching other clients even when one
// client stops consuming. Everything queued for the slow client remains FIFO.
func TestDeliver_SlowClientDoesNotBlockOtherClients(t *testing.T) {
	slow, fast := newTestClient(), newTestClient()
	defer slow.responses.close()
	defer fast.responses.close()
	const n = 2001 // exceeds the former production queue limit
	items := make([]*Response, n)
	for i := range items {
		items[i] = &Response{}
	}
	marker := &Response{}
	delivered := make(chan struct{})
	go func() {
		defer close(delivered)
		for _, item := range items {
			slow.deliver(item)
		}
		fast.deliver(marker)
	}()
	select {
	case <-delivered:
	case <-time.After(time.Second):
		slow.responses.close()
		<-delivered
		t.Fatal("slow client blocked delivery to another client")
	}
	if got, ok := fast.responses.pop(); !ok || got != marker {
		t.Fatal("other client did not receive its response")
	}
	slow.responses.close()
	for i, want := range items {
		if got, ok := slow.responses.pop(); !ok || got != want {
			t.Fatalf("slow client item %d lost or reordered", i)
		}
	}
	if _, ok := slow.responses.pop(); ok {
		t.Fatal("closed queue must end after draining")
	}
	if slow.responses.push(&Response{}) {
		t.Fatal("closed queue must reject new deliveries")
	}
}

func TestResponseQueue_CloseWakesConsumer(t *testing.T) {
	q := newResponseQueue()
	done := make(chan bool, 1)
	go func() {
		_, ok := q.pop()
		done <- ok
	}()
	q.close()
	q.close() // closing is idempotent
	select {
	case ok := <-done:
		if ok {
			t.Fatal("empty closed queue must return no item")
		}
	case <-time.After(time.Second):
		t.Fatal("close must release a waiting consumer")
	}
}

// TestResponseQueue_FIFOUnderContention: concurrent delivery must not reorder or
// lose items — updates carry state transitions whose order matters.
func TestResponseQueue_FIFOUnderContention(t *testing.T) {
	q := newResponseQueue()
	const n = 1000

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			q.push(&Response{meta: meta{MetaExtra: string(rune(i))}})
		}
		q.close()
	}()

	for i := 0; i < n; i++ {
		r, ok := q.pop()
		if !ok {
			t.Fatalf("queue closed after %d items, want %d", i, n)
		}
		if r.MetaExtra != string(rune(i)) {
			t.Fatalf("item %d out of order", i)
		}
	}
	if _, ok := q.pop(); ok {
		t.Fatal("expected closed after draining")
	}
	wg.Wait()
}

// newTestClient builds a Client that never touches TDLib: nothing is sent
// (jsonClient id -1 is never registered) and responses are injected by hand.
func newTestClient() *Client {
	return &Client{
		jsonClient:      &JsonClient{id: -1},
		responses:       newResponseQueue(),
		catchersStore:   &sync.Map{},
		extraGenerator:  UuidV4Generator(),
		resultHandler:   NewCallbackResultHandler(func(Type) {}),
		fallbackTimeout: time.Minute,
	}
}

// TestSend_ClosedClientFailsFast: a closed client never answers, so waiting
// out the fallback timeout (60s by default) only hides the real state.
func TestSend_ClosedClientFailsFast(t *testing.T) {
	c := newTestClient()
	c.closed.Store(true)

	start := time.Now()
	_, err := c.Send(context.Background(), &GetMeRequest{})
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("err = %v, want ErrClientClosed", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Send on a closed client must not wait for a response")
	}
}

// TestReceiver_LateResponseAfterSendGaveUp: Send times out, deletes its
// catcher, and the response arrives afterwards. With an unbuffered catcher the
// receiver would block forever (or panic if the catcher were closed); it must
// simply drop the response and keep going.
func TestReceiver_LateResponseAfterSendGaveUp(t *testing.T) {
	c := newTestClient()
	done := make(chan struct{})
	go func() {
		c.receiver()
		close(done)
	}()

	// Simulate a Send that already gave up: register then delete a catcher.
	catcher := make(chan *Response, 1)
	c.catchersStore.Store("late", catcher)
	c.catchersStore.Delete("late")

	// A response for it, plus one that closes the client.
	c.deliver(&Response{meta: meta{MetaExtra: "late"}, Data: []byte(`{"@type":"ok"}`)})
	c.deliver(&Response{Data: []byte(`{"@type":"updateAuthorizationState","authorization_state":{"@type":"authorizationStateClosed"}}`)})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("receiver did not exit after authorizationStateClosed")
	}
	if !c.IsClosed() {
		t.Fatal("client must be marked closed")
	}
	// Deliveries after close are dropped, never blocking the global receiver.
	for i := 0; i < 100; i++ {
		c.deliver(&Response{Data: []byte(`{"@type":"ok"}`)})
	}
}

// TestReceiver_DrainsAbortedResponsesAfterClosed: requests discarded by TDLib
// during close get their 500 "Request aborted" queued right after the Closed
// update; the waiting Send must receive it instead of hitting the timeout.
func TestReceiver_DrainsAbortedResponsesAfterClosed(t *testing.T) {
	c := newTestClient()
	catcher := make(chan *Response, 1)
	c.catchersStore.Store("pending", catcher)

	c.deliver(&Response{Data: []byte(`{"@type":"updateAuthorizationState","authorization_state":{"@type":"authorizationStateClosed"}}`)})
	c.deliver(&Response{meta: meta{MetaExtra: "pending", MetaType: "error"}, Data: []byte(`{"@type":"error","code":500,"message":"Request aborted"}`)})

	done := make(chan struct{})
	go func() {
		c.receiver()
		close(done)
	}()
	select {
	case r := <-catcher:
		if r.MetaType != "error" {
			t.Fatalf("got %s, want the aborted error", r.MetaType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("aborted response queued after Closed was not drained to its catcher")
	}
	<-done
}
