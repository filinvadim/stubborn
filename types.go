package stubborn

import (
	"context"
	"time"
)

const (
	TextMessage   = 1
	BinaryMessage = 2
	PingMessage   = 9
	PongMessage   = 10
)

type (
	DuplexConnector interface {
		Close() error
		ReadMessage() (messageType int, p []byte, err error)
		WriteMessage(messageType int, data []byte) error
	}

	KeepAlive struct {
		Tick       time.Duration
		CustomPing func() (msgType int, payload []byte)
		CustomPong func(msgTp int, data []byte) (msgType int, payload []byte)
	}

	DialerFunc func(ctx context.Context) (DuplexConnector, error)

	AuthHandler    func() (req []byte, resp []byte, err error)
	MessageHandler func(resp []byte)
	ErrorHandler   func(err error)

	PrintFunc func(args ...interface{})
)
