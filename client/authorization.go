package client

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type notSupportedAuthorizationState struct {
	state AuthorizationState
}

func (err *notSupportedAuthorizationState) Error() string {
	return fmt.Sprintf("not supported authorization state: %s", err.state.AuthorizationStateConstructor())
}

func NotSupportedAuthorizationState(state AuthorizationState) error {
	return &notSupportedAuthorizationState{
		state: state,
	}
}

type AuthorizationStateHandler interface {
	Handle(client *Client, state AuthorizationState) error
	Close()
}

func Authorize(client *Client, authorizationStateHandler AuthorizationStateHandler) error {
	defer authorizationStateHandler.Close()

	var authorizationError error

	for {
		state, err := client.GetAuthorizationState(context.Background())
		if err != nil {
			// After Handle failed the client has been closed below; the next
			// GetAuthorizationState then races with TDLib tearing the client
			// down and can fail with 500 "Request aborted" (or ErrClientClosed).
			// That failure is a side effect of our own Close — report the real
			// authorization error instead of losing it.
			if authorizationError != nil {
				return authorizationError
			}
			return err
		}

		if state.AuthorizationStateConstructor() == ConstructorAuthorizationStateClosed {
			return authorizationError
		}

		if state.AuthorizationStateConstructor() == ConstructorAuthorizationStateReady {
			// dirty hack for db flush after authorization
			time.Sleep(1 * time.Second)
			return nil
		}

		err = authorizationStateHandler.Handle(client, state)
		if err != nil {
			authorizationError = err
			client.Close(context.Background())
		}
	}
}

type clientAuthorizer struct {
	TdlibParameters *SetTdlibParametersRequest
	PhoneNumber     chan string
	Code            chan string
	State           chan AuthorizationState
	Password        chan string
	ctx             context.Context
	cancel          context.CancelFunc
	stateMu         sync.Mutex // protects State send/close, never held across input or RPC waits
	closeOnce       sync.Once
}

func ClientAuthorizer(tdlibParameters *SetTdlibParametersRequest) *clientAuthorizer {
	ctx, cancel := context.WithCancel(context.Background())
	return &clientAuthorizer{
		TdlibParameters: tdlibParameters,
		ctx:             ctx,
		cancel:          cancel,
		PhoneNumber:     make(chan string),
		Code:            make(chan string),
		State:           make(chan AuthorizationState),
		Password:        make(chan string),
	}
}

func (stateHandler *clientAuthorizer) Handle(client *Client, state AuthorizationState) error {
	if err := stateHandler.sendState(state); err != nil {
		return err
	}

	switch state.AuthorizationStateConstructor() {
	case ConstructorAuthorizationStateWaitTdlibParameters:
		_, err := client.SetTdlibParameters(stateHandler.ctx, stateHandler.TdlibParameters)
		return err

	case ConstructorAuthorizationStateWaitPhoneNumber:
		value, err := stateHandler.input(stateHandler.PhoneNumber)
		if err != nil {
			return err
		}
		_, err = client.SetAuthenticationPhoneNumber(stateHandler.ctx, &SetAuthenticationPhoneNumberRequest{
			PhoneNumber: value,
			Settings: &PhoneNumberAuthenticationSettings{
				AllowFlashCall:       false,
				IsCurrentPhoneNumber: false,
				AllowSmsRetrieverApi: false,
			},
		})
		return err

	case ConstructorAuthorizationStateWaitEmailAddress:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitEmailCode:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitCode:
		value, err := stateHandler.input(stateHandler.Code)
		if err != nil {
			return err
		}
		_, err = client.CheckAuthenticationCode(stateHandler.ctx, &CheckAuthenticationCodeRequest{
			Code: value,
		})
		return err

	case ConstructorAuthorizationStateWaitOtherDeviceConfirmation:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitRegistration:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitPassword:
		value, err := stateHandler.input(stateHandler.Password)
		if err != nil {
			return err
		}
		_, err = client.CheckAuthenticationPassword(stateHandler.ctx, &CheckAuthenticationPasswordRequest{
			Password: value,
		})
		return err

	case ConstructorAuthorizationStateReady:
		return nil

	case ConstructorAuthorizationStateLoggingOut:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateClosing:
		return nil

	case ConstructorAuthorizationStateClosed:
		return nil
	}

	return NotSupportedAuthorizationState(state)
}

// Done closes when this login attempt ends. Input senders must select on it;
// PhoneNumber, Code and Password remain open to avoid racing their senders.
func (stateHandler *clientAuthorizer) Done() <-chan struct{} {
	return stateHandler.ctx.Done()
}

func (stateHandler *clientAuthorizer) sendState(state AuthorizationState) error {
	stateHandler.stateMu.Lock()
	defer stateHandler.stateMu.Unlock()
	// Close cancels before taking this lock. Check before select because a send
	// to a closed State must never be considered, even when Done is also ready.
	if err := stateHandler.ctx.Err(); err != nil {
		return err
	}
	select {
	case stateHandler.State <- state:
		return nil
	case <-stateHandler.Done():
		return stateHandler.ctx.Err()
	}
}

func (stateHandler *clientAuthorizer) input(ch <-chan string) (string, error) {
	select {
	case value, ok := <-ch:
		if !ok {
			return "", context.Canceled
		}
		return value, stateHandler.ctx.Err()
	case <-stateHandler.Done():
		return "", stateHandler.ctx.Err()
	}
}

func (stateHandler *clientAuthorizer) Close() {
	// Wake a blocked state send before waiting for its lock.
	stateHandler.cancel()
	stateHandler.closeOnce.Do(func() {
		stateHandler.stateMu.Lock()
		defer stateHandler.stateMu.Unlock()
		close(stateHandler.State)
	})
}

func CliInteractor(clientAuthorizer *clientAuthorizer) {
	for {
		select {
		case <-clientAuthorizer.Done():
			return
		case state, ok := <-clientAuthorizer.State:
			if !ok {
				return
			}

			switch state.AuthorizationStateConstructor() {
			case ConstructorAuthorizationStateWaitPhoneNumber:
				fmt.Println("Enter phone number: ")
				var phoneNumber string
				fmt.Scanln(&phoneNumber)

				select {
				case clientAuthorizer.PhoneNumber <- phoneNumber:
				case <-clientAuthorizer.Done():
					return
				}

			case ConstructorAuthorizationStateWaitCode:
				var code string

				fmt.Println("Enter code: ")
				fmt.Scanln(&code)

				select {
				case clientAuthorizer.Code <- code:
				case <-clientAuthorizer.Done():
					return
				}

			case ConstructorAuthorizationStateWaitPassword:
				fmt.Println("Enter password: ")
				var password string
				fmt.Scanln(&password)

				select {
				case clientAuthorizer.Password <- password:
				case <-clientAuthorizer.Done():
					return
				}

			case ConstructorAuthorizationStateReady:
				return
			}
		}
	}
}

type botAuthorizer struct {
	tdlibParameters *SetTdlibParametersRequest
	token           string
}

func BotAuthorizer(tdlibParameters *SetTdlibParametersRequest, token string) *botAuthorizer {
	return &botAuthorizer{
		tdlibParameters: tdlibParameters,
		token:           token,
	}
}

func (stateHandler *botAuthorizer) Handle(client *Client, state AuthorizationState) error {
	switch state.AuthorizationStateConstructor() {
	case ConstructorAuthorizationStateWaitTdlibParameters:
		_, err := client.SetTdlibParameters(context.Background(), stateHandler.tdlibParameters)
		return err

	case ConstructorAuthorizationStateWaitPhoneNumber:
		_, err := client.CheckAuthenticationBotToken(context.Background(), &CheckAuthenticationBotTokenRequest{
			Token: stateHandler.token,
		})
		return err

	case ConstructorAuthorizationStateWaitEmailAddress:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitEmailCode:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitCode:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitOtherDeviceConfirmation:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitRegistration:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitPassword:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateReady:
		return nil

	case ConstructorAuthorizationStateLoggingOut:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateClosing:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateClosed:
		return NotSupportedAuthorizationState(state)
	}

	return NotSupportedAuthorizationState(state)
}

func (stateHandler *botAuthorizer) Close() {}

type qrAuthorizer struct {
	TdlibParameters *SetTdlibParametersRequest
	Password        chan string
	lastLink        string
	LinkHandler     func(link string) error
}

func QrAuthorizer(tdlibParameters *SetTdlibParametersRequest, linkHandler func(link string) error) *qrAuthorizer {
	stateHandler := &qrAuthorizer{
		TdlibParameters: tdlibParameters,
		Password:        make(chan string),
		LinkHandler:     linkHandler,
	}

	return stateHandler
}

func (stateHandler *qrAuthorizer) Handle(client *Client, state AuthorizationState) error {
	switch state.AuthorizationStateConstructor() {
	case ConstructorAuthorizationStateWaitTdlibParameters:
		_, err := client.SetTdlibParameters(context.Background(), stateHandler.TdlibParameters)
		return err

	case ConstructorAuthorizationStateWaitPhoneNumber:
		_, err := client.RequestQrCodeAuthentication(context.Background(), &RequestQrCodeAuthenticationRequest{})
		return err

	case ConstructorAuthorizationStateWaitOtherDeviceConfirmation:
		link := state.(*AuthorizationStateWaitOtherDeviceConfirmation).Link

		if link == stateHandler.lastLink {
			return nil
		}

		err := stateHandler.LinkHandler(link)
		if err != nil {
			return err
		}

		stateHandler.lastLink = link

		return nil

	case ConstructorAuthorizationStateWaitCode:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateWaitPassword:
		_, err := client.CheckAuthenticationPassword(context.Background(), &CheckAuthenticationPasswordRequest{
			Password: <-stateHandler.Password,
		})
		return err

	case ConstructorAuthorizationStateReady:
		return nil

	case ConstructorAuthorizationStateLoggingOut:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateClosing:
		return NotSupportedAuthorizationState(state)

	case ConstructorAuthorizationStateClosed:
		return NotSupportedAuthorizationState(state)
	}

	return NotSupportedAuthorizationState(state)
}

func (stateHandler *qrAuthorizer) Close() {
	close(stateHandler.Password)
}
