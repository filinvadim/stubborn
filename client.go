package stubborn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	//sync "github.com/sasha-s/go-deadlock"
	"time"
)

var (
	errNotInit          = errors.New("stubborn wasn't initialized")
	errAuthTimeout      = errors.New("auth timeout")
	errNoMessageHandler = errors.New("message handler wasn't set")
	errNoURL            = errors.New("connection URL wasn't set")
	errNoDialer         = errors.New("dialer wasn't set")

	ErrCritical = errors.New("critical")
	ErrMajor    = errors.New("major")
	ErrMinor    = errors.New("minor")
)

type Config struct {
	URL             string
	IsReconnectable bool
	// default message type
	MessageType int
	// use your favourite logger
	Print PrintFunc
	// use your favourite client
	Dialerf     DialerFunc
	AuthTimeOut time.Duration
	// it is easier to define expected error because exceptions always unexpected :)
	UnimportantErrs []error
}

type Client struct {
	url  string
	conn DuplexConnector

	config Config
	print  PrintFunc

	// service fields
	ctx           context.Context
	msgMx         *sync.Mutex
	once          *sync.Once
	stopRead      chan struct{}
	stopReadWait  chan struct{}
	stopKeepAlive chan struct{}
	isClosed      bool

	// healthcheck processing fields
	keep      *KeepAlive
	timeMx    *sync.Mutex
	timeAlive time.Time

	// auth processing fields
	authHandler AuthHandler
	authResp    []byte
	authChan    chan struct{}

	// messages processing fields
	messageHandler MessageHandler
	errorHandler   ErrorHandler
	errChan        chan error

	// reconnecting processing fields
	critErrChan   chan error
	backoff       *backoff
	reconnectedCh chan struct{}
}

func NewStubborn(
	conf Config,
) *Client {
	s := new(Client)

	s.url = conf.URL
	s.stopRead = make(chan struct{})
	s.stopReadWait = make(chan struct{})
	s.stopKeepAlive = make(chan struct{})
	s.critErrChan = make(chan error, 1)
	s.authChan = make(chan struct{}, 1)
	s.errChan = make(chan error, 100)
	s.reconnectedCh = make(chan struct{}, 1)
	s.msgMx = new(sync.Mutex)
	s.timeMx = new(sync.Mutex)
	s.once = new(sync.Once)
	s.config = conf

	s.print = s.config.Print
	if s.print == nil {
		s.print = func(args ...interface{}) {}
	}
	if s.config.MessageType == 0 {
		s.config.MessageType = TextMessage
	}

	return s
}

// Set auth callback handler
func (s *Client) SetAuthHandler(authH AuthHandler) {
	if s == nil {
		return
	}
	if s.conn != nil {
		return
	}

	s.authHandler = authH
}

// Set message handler excluding errors
func (s *Client) SetMessageHandler(msgH MessageHandler) {
	if s == nil {
		return
	}
	if s.conn != nil {
		return
	}

	s.messageHandler = msgH
}

// Set error handler excluding messages
func (s *Client) SetErrorHandler(errH ErrorHandler) {
	if s == nil {
		return
	}
	if s.conn != nil {
		return
	}

	s.errorHandler = errH
}

// Keep connection intact and trace it state
func (s *Client) SetKeepAliveHandler(keep KeepAlive) {
	if s == nil {
		return
	}
	if s.conn != nil {
		return
	}

	s.keep = &keep
}

func (s *Client) Connect(ctx context.Context) (err error) {
	if s == nil {
		return errNotInit
	}
	if s.messageHandler == nil {
		return errNoMessageHandler
	}
	if s.url == "" {
		return errNoURL
	}
	if s.config.Dialerf == nil {
		return errNoDialer
	}

	s.ctx = ctx

	dialerf := s.config.Dialerf

	s.conn, err = dialerf()
	if err != nil {
		return err
	}
	s.print("connection established")

	s.once.Do(func() {
		s.timeAlive = time.Now()

		go s.handleErrors()

		if s.config.IsReconnectable {
			rand.Seed(time.Now().UTC().UnixNano())
			// should be explicitly configurable or nah?
			s.backoff = &backoff{
				Exponentiation: 1.5,
				Jitter:         true,
				Min:            2 * time.Second,
				Max:            10 * time.Minute,
			}

			go s.reconnect()
		}
		go s.readLoop()

		err = s.auth()
		if err != nil {
			s.errChan <- criticalErr(err)
			return
		}

		if s.keep != nil {
			go s.keepAlive()
		}
	})

	return nil
}

func (s *Client) auth() error {
	if s.authHandler == nil {
		return nil
	}

	authReq, authResp, err := s.authHandler()
	if err != nil {
		return fmt.Errorf("auth error: %w", err)
	}

	s.authResp = authResp

	err = s.Send(s.config.MessageType, authReq)
	if err != nil {
		return fmt.Errorf("auth error: %w", err)
	}

	timeOut := s.config.AuthTimeOut
	if timeOut == 0 {
		timeOut = time.Second * 5
	}

	select {
	case <-s.authChan:
	case <-time.After(timeOut):
		return errAuthTimeout
	}
	return nil
}

func (s *Client) keepAlive() {
	if s.keep.Tick == nil {
		return
	}
	aliveTicker := time.NewTicker(30 * time.Minute)
	defer aliveTicker.Stop()

	for {
		select {
		case <-s.keep.Tick:
			if s.keep.CustomPing == nil {
				err := s.Send(PingMessage, []byte{})
				if err != nil {
					s.errChan <- minorErr(err)
				}
				continue
			}
			s.print("custom ping sent")
			err := s.Send(s.keep.CustomPing())
			if err != nil {
				s.errChan <- majorErr(err)
			}
		case <-aliveTicker.C:
			s.timeMx.Lock()
			s.print(
				"ws connection alive ",
				time.Now().Sub(s.timeAlive).Round(time.Minute),
			)
			s.timeMx.Unlock()
		case <-s.stopKeepAlive:
			s.print("keep alive stopped")
			return
		}

	}
}

func (s *Client) Send(msgType int, message []byte) error {
	if s == nil {
		return errNotInit
	}
	if s.isClosed {
		return nil
	}

	s.msgMx.Lock()
	err := s.conn.WriteMessage(msgType, message)
	s.msgMx.Unlock()
	return err
}

func (s *Client) read() (messageType int, p []byte, err error) {
	if s == nil {
		return 0, nil, errNotInit
	}

	messageType, p, err = s.conn.ReadMessage()
	return
}

func (s *Client) reconnect() {
	for cErr := range s.critErrChan {
		if s.isClosed {
			return
		}
		s.print("reconnecting...")
		dialerf := s.config.Dialerf

		if err := s.conn.Close(); err != nil {
			s.errChan <- minorErr(err)
		}

		if s.config.IsReconnectable {
			// duration: minTime = minTime ** exponential
			time.Sleep(s.backoff.Duration())
		}

		var err error
		s.conn, err = dialerf()
		if err != nil {
			// just warn if not reconnectable
			if !s.config.IsReconnectable {
				s.print(criticalErr(err))
				return
			}
			s.critErrChan <- criticalErr(err)
			continue
		}

		s.reconnectedCh <- struct{}{}

		err = s.auth()
		if err != nil {
			s.critErrChan <- criticalErr(err)
			continue
		}

		s.timeMx.Lock()
		s.timeAlive = time.Now()
		s.timeMx.Unlock()

		// sending notification about reconnect event by error
		s.errChan <- criticalErr(cErr)
		s.print("reconnected")

		// reconnect once only if panic occurs
		if !s.config.IsReconnectable {
			return
		}

	}
	s.print("reconnect stopped")
}

func (s *Client) handleErrors() {
	for err := range s.errChan {
		if s.errorHandler != nil {
			s.errorHandler(err)
		}
	}
	s.print("errors handling stopped")
}

func (s *Client) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			if s.isClosed {
				return
			}
			if !s.config.IsReconnectable {
				go s.reconnect()
			}

			s.critErrChan <- fmt.Errorf("%v", r)
			s.errChan <- criticalErr(fmt.Errorf("%v", r))
			<-s.reconnectedCh
			go s.readLoop()
		}
	}()
	if s.conn == nil {
		return
	}

	for {
		select {
		case <-s.stopRead:
			close(s.stopReadWait)
			return

		default:
			msgType, msg, err := s.read()
			if err != nil {
				s.print(err.Error())
				if s.isUnimportant(err) {
					break
				}
				if s.config.IsReconnectable && !s.isClosed {
					s.critErrChan <- criticalErr(err)
					<-s.reconnectedCh
				}
				break
			}

			if msg == nil {
				break
			}

			s.print("message received", string(msg))

			var payload []byte
			switch msgType {
			case BinaryMessage:
				payload, err = GZipDecompress(msg)
				if err != nil {
					s.errChan <- majorErr(err)
				}
			case TextMessage:
				payload = msg
			default:
				payload = msg
			}

			tp, p := s.checkPong(msgType, payload)
			if tp != 0 {
				s.print("ping received")
				if err := s.Send(tp, p); err != nil {
					s.errChan <- majorErr(err)
				}
				break
			}

			if ok := s.checkAuth(payload); ok {
				break
			}

			s.messageHandler(payload)
		}
	}
}

func (s *Client) checkPong(msgType int, payload []byte) (int, []byte) {
	if s.keep == nil {
		return 0, nil
	}
	if s.keep.CustomPong == nil {
		return 0, nil
	}

	return s.keep.CustomPong(msgType, payload)

}

func (s *Client) isUnimportant(err error) (ok bool) {
	if err == nil {
		return
	}
	if len(s.config.UnimportantErrs) == 0 {
		return
	}
	for _, e := range s.config.UnimportantErrs {
		if err.Error() == e.Error() {
			return true
		}
	}
	return
}

func (s *Client) checkAuth(data []byte) (ok bool) {
	if s.authHandler == nil {
		return
	}
	if s.authResp == nil {
		s.authChan <- struct{}{}
		return
	}

	// auth response may look like just 'success' string
	if !json.Valid(s.authResp) {
		if string(s.authResp) == string(data) {
			s.authChan <- struct{}{}
			return true
		}
		return
	}

	opts := DefaultJSONOptions()
	diff, _ := CompareJSON(s.authResp, data, &opts)
	if diff == FullMatch {
		s.authChan <- struct{}{}
		return true
	}
	return
}

// in case this important
func (s *Client) waitErrors() {
	for {
		if len(s.errChan) == 0 {
			return
		}
	}
}

func criticalErr(err error) error {
	return fmt.Errorf("%s %w", err, ErrCritical)
}

func majorErr(err error) error {
	return fmt.Errorf("%s %w", err, ErrMajor)
}

func minorErr(err error) error {
	return fmt.Errorf("%s %w", err, ErrMinor)
}

func (s *Client) Close() {
	s.isClosed = true

	close(s.stopRead)
	s.print("stubborn closing...")
	select {
	case <-s.stopReadWait:
	case <-time.After(time.Second * 5):
	}
	close(s.stopKeepAlive)

	if s.conn != nil {
		if err := s.conn.Close(); err != nil {
			s.errChan <- minorErr(err)
		}
	}

	if s.config.IsReconnectable {
		close(s.critErrChan)
		close(s.reconnectedCh)
	}
	s.waitErrors()
	close(s.errChan)
	close(s.authChan)
	s.print("stubborn closed")
}
