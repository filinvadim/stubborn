package stubborn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

const (
	countMessages = 5

	sampleMessage     = "websocket sample message"
	authSuccessString = "auth success"
	authReqType       = "auth"
	pinged            = "pinged"
	customPingMessage = "custom ping message"

	sub = "sub"
)

func init() {
	rand.Seed(time.Now().Unix())
}

var (
	errMust = errors.New("must be error")
)

func TestConnectSuccess(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "test",
	})
	defer stub.Close()

	errChan := make(chan error, 1)
	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})
	stub.SetMessageHandler(func(resp []byte) {})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-errChan:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestConnectFailNoDialer(t *testing.T) {
	errNoDialer := errors.New("no dialer")

	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return nil, errNoDialer
		},
		URL: "test",
	})
	defer stub.Close()

	stub.SetMessageHandler(func(resp []byte) {})
	err := stub.Connect(context.Background())
	if err == nil {
		t.Fatal(errMust)
	}

	if err != errNoDialer {
		t.Fatal(errMust)
	}
}

func TestConnectFailNoMessageHandler(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "test",
	})

	errChan := make(chan error, 1)
	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})
	err := stub.Connect(context.Background())
	if err == nil {
		t.Fatal(errMust)
	}
	if err.Error() != "message handler wasn't set" {
		t.Fatal(errMust)
	}
	select {
	case err = <-errChan:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestConnectFailNoURL(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "",
	})
	stub.SetMessageHandler(func(resp []byte) {})
	err := stub.Connect(context.Background())
	if err == nil {
		t.Fatal(errMust)
	}
	if err.Error() != "connection URL wasn't set" {
		t.Fatal(errMust)
	}
}

func TestAuthSuccess(t *testing.T) {
	authStruct := struct {
		Auth interface{} `json:"auth"`
	}{
		Auth: 200,
	}
	btStruct, _ := json.Marshal(authStruct)

	authArray := []interface{}{authReqType, 200}
	btArray, _ := json.Marshal(authArray)

	testCases := []struct {
		name string
		req  []byte
		resp []byte
	}{
		{"byte auth", []byte(authReqType), []byte(authSuccessString)},
		{"struct auth", []byte(authReqType), btStruct},
		{"array auth", []byte(authReqType), btArray},
	}

	for _, cs := range testCases {
		t.Run(cs.name, func(t *testing.T) {
			stub := NewStubborn(Config{
				Dialerf: func() (WSConnector, error) {
					return &MockConn{}, nil
				},
				AuthTimeOut: time.Second,
				URL:         "test",
			})

			errChan := make(chan error, 1)
			stub.SetErrorHandler(func(err error) {
				errChan <- err
			})
			stub.SetMessageHandler(func(resp []byte) {})

			stub.SetAuthHandler(func() (msg []byte, resp []byte, err error) {
				return cs.req, cs.resp, nil
			})

			err := stub.Connect(context.Background())
			if err != nil {
				stub.Close()
				t.Fatal(err)
			}
			select {
			case err = <-errChan:
				stub.Close()
				t.Fatal(err)
			case <-time.After(100 * time.Millisecond):
				stub.Close()
			}
		})
	}
}

func TestAuthFail(t *testing.T) {
	errChan := make(chan error, 3)
	authStruct := struct {
		Auth interface{} `json:"auth"`
	}{
		Auth: 500,
	}
	btStruct, _ := json.Marshal(authStruct)

	testCases := []struct {
		name string
		req  []byte
		resp []byte
	}{
		{"empty auth", []byte(authReqType), []byte{}},
		{"wrong byte auth", []byte(authReqType), []byte("wrong auth response")},
		{"wrong struct auth", []byte(authReqType), btStruct},
	}

	for _, cs := range testCases {
		t.Run(cs.name, func(t *testing.T) {

			stub := NewStubborn(Config{
				Dialerf: func() (WSConnector, error) {
					return &MockConn{}, nil
				},
				AuthTimeOut: time.Second * time.Duration(rand.Intn(3)),
				URL:         "test",
			})

			stub.SetErrorHandler(func(err error) {
				errChan <- err
			})
			stub.SetMessageHandler(func(resp []byte) {})
			stub.SetAuthHandler(func() (msg []byte, resp []byte, err error) {
				resp = cs.resp
				msg = cs.req
				return cs.req, cs.resp, nil
			})

			err := stub.Connect(context.Background())
			if err != nil {
				stub.Close()
				t.Fatal(err)
			}
			select {
			case <-time.After(2 * time.Second):
				stub.Close()
				t.Fatal("wait auth error timeout")
			case err = <-errChan:
			}
			stub.Close()
		})
	}
}

func TestKeepAliveSuccess(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "test",
	})
	defer stub.Close()

	pingedChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)
	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})
	stub.SetMessageHandler(func(resp []byte) {
		if string(resp) == pinged {
			pingedChan <- struct{}{}
		}
	})

	stub.SetKeepAliveHandler(KeepAlive{
		Tick:       time.After(time.Second / 4),
		CustomPing: nil,
		CustomPong: nil,
	})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("pong timeout")
	case err = <-errChan:
		t.Fatal(err)
	case <-pingedChan:
	}
}

func TestKeepAliveFail(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "test",
	})
	defer stub.Close()

	pingedChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})
	stub.SetMessageHandler(func(resp []byte) {
		if string(resp) == pinged {
			pingedChan <- struct{}{}
		}
	})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-time.After(1 * time.Second):
	case err = <-errChan:
		t.Fatal(err)
	case <-pingedChan:
		t.Fatal("must not ping")
	}
}

func TestKeepAliveCustomPingSuccess(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "test",
	})
	defer stub.Close()

	pingedChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})
	stub.SetMessageHandler(func(resp []byte) {
		if string(resp) == customPingMessage {
			pingedChan <- struct{}{}
		}
	})

	stub.SetKeepAliveHandler(KeepAlive{
		Tick: time.After(time.Second / 4),
		CustomPing: func() (msgType int, payload []byte) {
			return PingMessage, []byte(customPingMessage)
		},
	})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("pong timeout")
	case err = <-errChan:
		t.Fatal(err)
	case <-pingedChan:
	}
}

func TestKeepAliveCustomPingFail(t *testing.T) {
	pingedChan := make(chan struct{}, 1)

	testCases := []struct {
		name    string
		msgType int
		payload []byte
	}{
		{"wrong msg type", BinaryMessage, []byte{}},
		{"wrong message", PingMessage, []byte("wrong message")},
	}

	for _, cs := range testCases {
		t.Run(cs.name, func(t *testing.T) {

			stub := NewStubborn(Config{
				Dialerf: func() (WSConnector, error) {
					return &MockConn{}, nil
				},
				URL: "test",
			})

			stub.SetMessageHandler(func(resp []byte) {
				if string(resp) == customPingMessage {
					pingedChan <- struct{}{}
				}
			})
			stub.SetKeepAliveHandler(KeepAlive{
				Tick: time.After(time.Second / 4),
				CustomPing: func() (msgType int, payload []byte) {
					return cs.msgType, cs.payload
				},
			})
			err := stub.Connect(context.Background())
			if err != nil {
				stub.Close()
				t.Fatal(err)
			}

			select {
			case <-time.After(2 * time.Second):
			case <-pingedChan:
				stub.Close()
				t.Fatal("must not ping")
			}
			stub.Close()
		})
	}
}

func TestKeepAliveCustomPongSuccess(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "test",
	})
	defer stub.Close()

	pongedChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})
	stub.SetMessageHandler(func(resp []byte) {})

	stub.SetKeepAliveHandler(KeepAlive{
		CustomPong: func(msgTp int, data []byte) (msgType int, payload []byte) {
			if msgTp != TextMessage {
				return 0, nil
			}

			ping := struct {
				Action string `json:"action"`
				Data   *struct {
					Timestamp int64 `json:"ts"`
				} `json:"data"`
			}{}
			if err := json.Unmarshal(data, &ping); err != nil {
				return 0, nil
			}
			pongMsg := fmt.Sprintf(
				"{\"action\": \"pong\", \"data\": { \"ts\": %d } }",
				ping.Data.Timestamp,
			)
			pongedChan <- struct{}{}

			return TextMessage, []byte(pongMsg)
		},
	})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("pong timeout")
	case err = <-errChan:
		t.Fatal(err)
	case <-pongedChan:
	}
}

func TestKeepAliveCustomPongFail(t *testing.T) {

	testCases := []struct {
		name    string
		msgType int
		payload []byte
	}{
		{"wrong msg type", BinaryMessage, []byte{}},
		{"wrong message", PingMessage, []byte("wrong message")},
	}

	for _, cs := range testCases {
		t.Run(cs.name, func(t *testing.T) {
			stub := NewStubborn(Config{
				Dialerf: func() (WSConnector, error) {
					return &MockConn{}, nil
				},
				URL: "test",
			})

			missedPongChan := make(chan struct{}, 1)
			errChan := make(chan error, 1)

			stub.SetErrorHandler(func(err error) {
				errChan <- err
			})
			stub.SetMessageHandler(func(resp []byte) {
				missedPongChan <- struct{}{}
			})

			stub.SetKeepAliveHandler(KeepAlive{
				CustomPong: func(msgTp int, data []byte) (msgType int, payload []byte) {

					// should not return 'pong' result and proceed to response handler
					msgTp = cs.msgType
					data = payload

					if msgTp != TextMessage {
						return 0, nil
					}

					ping := struct {
						Action string `json:"action"`
						Data   *struct {
							Timestamp int64 `json:"ts"`
						} `json:"data"`
					}{}
					if err := json.Unmarshal(data, &ping); err != nil {
						return 0, nil
					}
					pongMsg := fmt.Sprintf(
						"{\"action\": \"pong\", \"data\": { \"ts\": %d } }",
						ping.Data.Timestamp,
					)

					return TextMessage, []byte(pongMsg)
				},
			})

			err := stub.Connect(context.Background())
			if err != nil {
				stub.Close()
				t.Fatal(err)
			}

			select {
			case <-time.After(2 * time.Second):
				stub.Close()
				t.Fatal("response timeout")
			case err = <-errChan:
				stub.Close()
				t.Fatal(err)
			case <-missedPongChan:
			}
			stub.Close()
		})
	}
}

func TestSubscriptionSuccess(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "test",
	})
	defer stub.Close()

	counter := 0
	waitChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})
	stub.SetMessageHandler(func(resp []byte) {
		if string(resp) == sampleMessage {
			counter++
		}
		if counter == countMessages {
			waitChan <- struct{}{}
		}
	})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	stub.Send(TextMessage, []byte(sub))

	select {
	case <-time.After(1 * time.Second):
		t.Fatal("response timeout")
	case err = <-errChan:
		t.Fatal(err)
	case <-waitChan:
	}
}

func TestNoSubscriptionFail(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "test",
	})
	defer stub.Close()

	waitChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})
	stub.SetMessageHandler(func(resp []byte) {
		if string(resp) == sampleMessage {
			waitChan <- struct{}{}
		}
	})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-time.After(1 * time.Second):
	case err = <-errChan:
		t.Fatal(err)
	case <-waitChan:
		t.Fatal("response must not be present")
	}
}

func TestWrongSubscriptionFail(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{}, nil
		},
		URL: "test",
	})
	defer stub.Close()

	waitChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})
	stub.SetMessageHandler(func(resp []byte) {
		if string(resp) == sampleMessage {
			waitChan <- struct{}{}
		}
	})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stub.Send(TextMessage, []byte("where am i?"))

	select {
	case <-time.After(1 * time.Second):
	case err = <-errChan:
		t.Fatal(err)
	case <-waitChan:
		t.Fatal("response must not be present")
	}
}

func TestSubscriptionReconnectSuccess(t *testing.T) {
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return &MockConn{isFailable: true}, nil
		},
		IsReconnectable: true,
		URL:             "test",
	})
	defer stub.Close()

	counter := 0
	waitChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})

	stub.SetMessageHandler(func(resp []byte) {
		if string(resp) == sampleMessage {
			counter++
		}
		if counter == countMessages {
			waitChan <- struct{}{}
		}
	})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	stub.Send(TextMessage, []byte(sub))

	select {
	case <-time.After(15 * time.Second):
		t.Fatal("response timeout")
	case <-waitChan:
	}
	select {
	case <-time.After(1 * time.Second):
	case err = <-errChan:
		t.Log(err)
	}
}

func TestSubscriptionReconnectPanicSuccess(t *testing.T) {
	mockConn := &MockConn{}
	stub := NewStubborn(Config{
		Dialerf: func() (WSConnector, error) {
			return mockConn, nil
		},
		IsReconnectable: false, // unexpected reconnect due panic
		URL:             "test",
	})
	defer stub.Close()

	counter := 0
	waitChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	stub.SetErrorHandler(func(err error) {
		errChan <- err
	})

	stub.SetMessageHandler(func(resp []byte) {
		if string(resp) == sampleMessage {
			counter++
		}
		if counter == countMessages {
			waitChan <- struct{}{}
		}
	})

	var wasPanic bool
	stub.SetKeepAliveHandler(KeepAlive{
		CustomPong: func(msgTp int, data []byte) (msgType int, payload []byte) {
			if !wasPanic {
				wasPanic = true
				panic("custom panic")
			}
			return 0, nil
		},
	})

	err := stub.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	stub.Send(TextMessage, []byte(sub))

	select {
	case <-time.After(30 * time.Second):
		t.Fatal("response timeout")
	case <-waitChan:
	}
	select {
	case <-time.After(1 * time.Second):
	case err = <-errChan:
		t.Log(err)
	}
}

//////////////////////////////////////////////////////////////////////////////////////////////
///////////////////////////////////////MOCK///////////////////////////////////////////////////
//////////////////////////////////////////////////////////////////////////////////////////////

var (
	mockCustomPing = []byte(`{"action": "ping","data": {"ts": 1575537778295}}`)

	msgCount int
)

type MockConn struct {
	isFailable     bool
	isAuthDone     bool
	authTypesCount int
	subscribed     bool
	pinged         bool
	customPinged   bool
}

func (mc *MockConn) ReadMessage() (messageType int, p []byte, err error) {
	if mc.isAuthDone {
		var bt []byte
		if mc.authTypesCount == 0 {
			authMsg := struct {
				Auth int `json:"auth"`
			}{
				Auth: 200,
			}
			bt, _ = json.Marshal(authMsg)
		}
		if mc.authTypesCount == 1 {
			authMsg := []interface{}{authReqType, 200}
			bt, _ = json.Marshal(authMsg)
		}
		if mc.authTypesCount == 2 {
			bt = []byte(authSuccessString)
		}
		mc.authTypesCount++
		if mc.authTypesCount > 2 {
			mc.isAuthDone = false
		}
		return TextMessage, bt, nil
	}
	if msgCount > countMessages {
		return 0, nil, nil
	}
	if mc.pinged {
		return PongMessage, []byte(pinged), err
	}
	if mc.customPinged {
		return PongMessage, []byte(customPingMessage), err
	}
	if !mc.subscribed && !mc.isFailable {
		time.Sleep(100 * time.Millisecond)
		return TextMessage, mockCustomPing, nil
	}
	if msgCount == 1 && mc.isFailable {
		msgCount++
		return 0, nil, errors.New("CRITICAL")
	}

	msgCount++
	return TextMessage, []byte(sampleMessage), nil

}

func (mc *MockConn) WriteMessage(messageType int, data []byte) error {
	if bytes.Contains(data, []byte(authReqType)) {
		mc.isAuthDone = true
		return nil
	}
	if bytes.Contains(data, []byte(sub)) {
		mc.subscribed = true
		return nil
	}

	if bytes.Contains(data, []byte(customPingMessage)) {
		mc.customPinged = true
		return nil
	}

	if messageType == PingMessage {
		mc.pinged = true
		return nil
	}
	return nil
}

func (mc *MockConn) Close() error {
	if msgCount > countMessages {
		msgCount = 0
	}
	return nil
}
