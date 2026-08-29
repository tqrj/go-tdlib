package client

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrClientClosed is returned by Send when the TDLib client has already
// reached authorizationStateClosed. TDLib itself answers every request sent
// to a closed client with 500 "Request aborted", but that answer is delivered
// asynchronously; failing fast here keeps callers from waiting for a response
// that would only tell them the same thing.
var ErrClientClosed = errors.New("tdlib client is closed")

// ErrResponseTimeout is returned by Send when no response arrived within the
// fallback timeout (see WithFallbackTimeout). It deliberately does NOT unwrap
// to context.DeadlineExceeded: that one means "the caller gave up", this one
// means "TDLib is slow right now" — callers retry the latter and not the
// former. The message is kept stable on purpose: callers that classify TDLib
// failures by text ("response catching timeout") have relied on it since the
// pre-context API.
var ErrResponseTimeout = errors.New("response catching timeout")

// responseQueueSize bounds how many undelivered responses/updates a client may
// hold. When it is full the global receiver blocks (back-pressure on TDLib)
// exactly like the buffered channel it replaces.
const responseQueueSize = 1000

type Client struct {
	jsonClient      *JsonClient
	extraGenerator  ExtraGenerator
	responses       *responseQueue
	resultHandler   ResultHandler
	catchersStore   *sync.Map
	fallbackTimeout time.Duration
	closed          atomic.Bool
}

type Option func(*Client)

func WithExtraGenerator(extraGenerator ExtraGenerator) Option {
	return func(client *Client) {
		client.extraGenerator = extraGenerator
	}
}

func WithFallbackTimeout(timeout time.Duration) Option {
	return func(client *Client) {
		client.fallbackTimeout = timeout
	}
}

func WithProxy(req *AddProxyRequest) Option {
	return func(client *Client) {
		client.AddProxy(context.Background(), req)
	}
}

func WithResultHandler(resultHandler ResultHandler) Option {
	return func(client *Client) {
		client.resultHandler = resultHandler
	}
}

type ResultHandler interface {
	OnResult(result Type)
}

type CallbackResultHandler struct {
	callback func(result Type)
}

func (handler *CallbackResultHandler) OnResult(result Type) {
	handler.callback(result)
}

func NewCallbackResultHandler(callback func(result Type)) *CallbackResultHandler {
	return &CallbackResultHandler{
		callback: callback,
	}
}

func NewClient(authorizationStateHandler AuthorizationStateHandler, options ...Option) (*Client, error) {
	client := &Client{
		jsonClient:    NewJsonClient(),
		responses:     newResponseQueue(responseQueueSize),
		catchersStore: &sync.Map{},
	}

	client.extraGenerator = UuidV4Generator()
	client.resultHandler = NewCallbackResultHandler(func(result Type) {})
	client.fallbackTimeout = 60 * time.Second

	// Options are applied synchronously and before the first update can be
	// dispatched: a result handler installed from a goroutine would race with
	// the receiver and miss the first updates.
	for _, option := range options {
		option(client)
	}

	tdlibInstance.addClient(client)
	go client.receiver()

	err := Authorize(client, authorizationStateHandler)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// IsClosed reports whether the client has reached authorizationStateClosed.
// A closed client never delivers anything again; Send fails fast on it.
func (client *Client) IsClosed() bool {
	return client.closed.Load()
}

// deliver hands a response from the global receiver to this client. It is a
// no-op once the client is closed, so a late "Request aborted" for a closed
// client can never reach a channel nobody reads any more.
func (client *Client) deliver(response *Response) {
	client.responses.push(response)
}

func (client *Client) receiver() {
	for {
		response, ok := client.responses.pop()
		if !ok {
			return
		}

		if response.MetaExtra != "" {
			value, ok := client.catchersStore.Load(response.MetaExtra)
			if ok {
				// The catcher is buffered (1) and never closed, so a response
				// that arrives after Send gave up neither blocks nor panics.
				select {
				case value.(chan *Response) <- response:
				default:
				}
			}
		}

		typ, err := UnmarshalType(response.Data)
		if err != nil {
			continue
		}

		client.resultHandler.OnResult(typ)

		if typ.GetConstructor() == ConstructorUpdateAuthorizationState &&
			typ.(*UpdateAuthorizationState).AuthorizationState.AuthorizationStateConstructor() == ConstructorAuthorizationStateClosed {
			// Order matters: first leave the registry (no new deliveries can
			// start), then close the queue (deliveries already in flight are
			// dropped). Whatever is still queued — typically 500 "Request
			// aborted" for requests TDLib discarded while closing — is drained
			// below so the waiting Send calls fail immediately instead of
			// hitting their timeout.
			tdlibInstance.removeClient(client.jsonClient.id)
			client.closed.Store(true)
			client.responses.close()
		}
	}
}

func (client *Client) Send(ctx context.Context, req Request) (*Response, error) {
	if client.closed.Load() {
		return nil, ErrClientClosed
	}

	req.SetExtra(client.extraGenerator())
	req.SetType(req.GetFunctionName())

	catcher := make(chan *Response, 1)

	client.catchersStore.Store(req.GetExtra(), catcher)
	defer client.catchersStore.Delete(req.GetExtra())

	err := client.jsonClient.Send(req)
	if err != nil {
		return nil, err
	}

	fallbackCtx, cancel := context.WithTimeout(context.Background(), client.fallbackTimeout)
	defer cancel()

	select {
	case response := <-catcher:
		return response, nil

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-fallbackCtx.Done():
		return nil, ErrResponseTimeout
	}
}

func (client *Client) Execute(req Request) (*Response, error) {
	req.SetExtra(client.extraGenerator())
	req.SetType(req.GetFunctionName())

	return client.jsonClient.Execute(req)
}

// responseQueue is a bounded FIFO that can be closed safely from the consumer
// side while a producer is blocked on it — the one thing a Go channel cannot
// do (close + concurrent send = panic). The producer is the process-wide
// td_receive loop, so a panic there takes the whole process down.
type responseQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []*Response
	cap    int
	closed bool
}

func newResponseQueue(capacity int) *responseQueue {
	q := &responseQueue{cap: capacity}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends an item, blocking while the queue is full. It returns false
// (dropping the item) if the queue is or becomes closed.
func (q *responseQueue) push(item *Response) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) >= q.cap && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		return false
	}
	q.items = append(q.items, item)
	q.cond.Broadcast()
	return true
}

// pop removes the oldest item, blocking while the queue is empty. It returns
// false only when the queue is closed and fully drained.
func (q *responseQueue) pop() (*Response, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return nil, false
	}
	item := q.items[0]
	q.items[0] = nil
	q.items = q.items[1:]
	q.cond.Broadcast()
	return item, true
}

// close stops accepting items and wakes every blocked producer/consumer.
// Items already queued stay readable until drained.
func (q *responseQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}
