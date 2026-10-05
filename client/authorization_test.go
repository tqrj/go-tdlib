package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestClientAuthorizer_CloseWhileWaitingForInput(t *testing.T) {
	for _, state := range []AuthorizationState{
		&AuthorizationStateWaitPhoneNumber{}, &AuthorizationStateWaitCode{}, &AuthorizationStateWaitPassword{},
	} {
		t.Run(state.AuthorizationStateConstructor(), func(t *testing.T) {
			a := ClientAuthorizer(nil)
			cl := newTestClient()
			cl.closed.Store(true) // no native request should be needed to cancel an input wait
			result := make(chan error, 1)
			go func() { result <- a.Handle(cl, state) }()
			select {
			case <-a.State:
			case <-time.After(time.Second):
				t.Fatal("state was not delivered")
			}
			a.Close()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("got %v, want cancellation without an RPC", err)
				}
			case <-time.After(time.Second):
				t.Fatal("input wait did not stop")
			}
		})
	}
}

func TestClientAuthorizer_CloseDuringStateDelivery(t *testing.T) {
	for i := 0; i < 100; i++ {
		a := ClientAuthorizer(nil)
		result := make(chan error, 1)
		go func() { result <- a.Handle(nil, &AuthorizationStateClosing{}) }()
		var wg sync.WaitGroup
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() { defer wg.Done(); a.Close() }()
		}
		wg.Wait()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want cancellation", err)
			}
		case <-time.After(time.Second):
			t.Fatal("state delivery did not stop")
		}
		if _, ok := <-a.State; ok {
			t.Fatal("State must close")
		}
		if err := a.Handle(nil, &AuthorizationStateClosing{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle after Close: %v", err)
		}
	}
}

// Leave this native client's responses undispatched until cancellation: the
// login RPC must stop without relying on its response or the fallback timeout.
func TestClientAuthorizer_CloseWhileWaitingForRPC(t *testing.T) {
	cl := newTestClient()
	cl.jsonClient = NewJsonClient()
	t.Cleanup(func() {
		tdlibInstance.addClient(cl)
		stopped := make(chan struct{})
		go func() { cl.receiver(); close(stopped) }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := cl.Close(ctx); err != nil {
			t.Errorf("close native client: %v", err)
		}
		select {
		case <-stopped:
		case <-ctx.Done():
			t.Error("native client did not close")
		}
	})
	a := ClientAuthorizer(nil)
	defer a.Close()
	result := make(chan error, 1)
	go func() { result <- a.Handle(cl, &AuthorizationStateWaitCode{}) }()
	select {
	case <-a.State:
	case <-time.After(time.Second):
		t.Fatal("state was not delivered")
	}
	select {
	case a.Code <- "test":
	case <-time.After(time.Second):
		t.Fatal("input was not consumed")
	}
	deadline := time.Now().Add(time.Second)
	for {
		pending := false
		cl.catchersStore.Range(func(_, _ any) bool { pending = true; return false })
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("RPC did not start")
		}
		time.Sleep(time.Millisecond)
	}
	a.Close()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want canceled RPC", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RPC ignored login cancellation")
	}
}
