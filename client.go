package stubborn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"runtime/debug"
	"sync"
	//sync "github.com/sasha-s/go-deadlock"
	"time"
)

var (
	errNotInit          = errors.New("stubborn wasn't initialized")
	errNotConnected     = errors.New("stubborn is not connected")
	errAuthTimeout      = errors.New("auth timeout")
	errNoMessageHandler = errors.New("message handler wasn't set")
	errNoURL            = errors.New("connection URL wasn't set")
	errNoDialer         = errors.New("dialer wasn't set")

	ErrCritical = errors.New("critical")
	ErrMajor    = errors.New("major")
	ErrMinor    = errors.New("minor")
)

type Config struct {
	IsReconnectable bool
	// default message type
	MessageType int
	// use your favourite logger
	Logger CustomLogger
	// use your favourite client
	Dialerf     DialerFunc
	AuthTimeOut time.Duration
	// it is easier to define expected error because exceptions always unexpected :)
	UnimportantErrs []error
}

type Client struct {
	conn DuplexConnector

	config Config
	l      CustomLogger

	// service fields
	ctx           context.Context
	writeMx       *sync.Mutex
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
	isAuthed    bool

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

	s.stopRead = make(chan struct{})
	s.stopReadWait = make(chan struct{})
	s.stopKeepAlive = make(chan struct{})
	s.critErrChan = make(chan error, 1)
	s.authChan = make(chan struct{}, 1)
	s.errChan = make(chan error, 100)
	s.reconnectedCh = make(chan struct{}, 1)
	s.writeMx = new(sync.Mutex)
	s.timeMx = new(sync.Mutex)
	s.config = conf

	s.l = s.config.Logger
	if s.l == nil {
		s.l = defaultLogger{}
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

// SetKeepAliveHandler keep connection intact and trace it state
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
	if s.config.Dialerf == nil {
		return errNoDialer
	}

	s.ctx = ctx

	dialerf := s.config.Dialerf

	s.conn, err = dialerf(s.ctx)
	if err != nil {
		return err
	}

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
		return err
	}
	s.l.Infoln("connection established")

	if s.keep != nil {
		go s.keepAlive()
	}

	return nil
}

func (s *Client) ManualReconnect() {
	if !s.isClosed {
		_ = s.conn.Close()
	}
}

func (s *Client) auth() error {
	if s.authHandler == nil {
		return nil
	}
	s.isAuthed = false

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
		s.isAuthed = true
	case <-time.After(timeOut):
		return errAuthTimeout
	}
	return nil
}

func (s *Client) keepAlive() {
	if s.keep.Tick == 0 {
		return
	}
	aliveTicker := time.NewTicker(30 * time.Minute)
	defer aliveTicker.Stop()
	var err error

	// we can't rely on select random therefore pinging must be called first despite everything
	go func() {
		for {
			select {
			case <-time.Tick(s.keep.Tick):
				if s.keep.CustomPing == nil {
					err := s.Send(PingMessage, []byte{})
					if err != nil {
						s.errChan <- minorErr(err)
					}
					continue
				}

				err = s.Send(s.keep.CustomPing())
				if err != nil {
					s.errChan <- majorErr(err)
				} else {
					s.l.Debugln("custom ping sent")
				}

			case <-s.stopKeepAlive:
				s.l.Debugln("pinging stopped")
				return
			}
		}
	}()

	for {
		select {
		case <-aliveTicker.C:
			s.timeMx.Lock()
			s.l.Infoln(
				"ws connection alive ",
				time.Now().Sub(s.timeAlive).Round(time.Minute),
			)
			s.timeMx.Unlock()
		case <-s.stopKeepAlive:
			s.l.Debugln("keep alive stopped")
			return
		}
	}
}

func (s *Client) Send(msgType int, message []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v \n %s", r, string(debug.Stack()))
		}
	}()
	if s == nil {
		return errNotInit
	}
	if s.conn == nil {
		return errNotConnected
	}
	if s.isClosed {
		return nil
	}

	s.writeMx.Lock()
	defer s.writeMx.Unlock()
	return s.conn.WriteMessage(msgType, message)
}

func (s *Client) read() (messageType int, p []byte, err error) {
	if s == nil {
		return 0, nil, errNotInit
	}
	if s.conn == nil {
		return 0, nil, errNotConnected
	}

	messageType, p, err = s.conn.ReadMessage()
	return
}

func (s *Client) reconnect() {
	defer func() {
		if r := recover(); r != nil {
			if s.isClosed {
				return
			}
			if !s.config.IsReconnectable {
				go s.reconnect()
			}

			s.critErrChan <- fmt.Errorf("%v", r)
			s.errChan <- criticalErr(fmt.Errorf("%v \n %s", r, string(debug.Stack())))
			<-s.reconnectedCh
		}
	}()

	for cErr := range s.critErrChan {
		if s.isClosed {
			return
		}
		s.l.Infoln("reconnecting...")
		dialerf := s.config.Dialerf

		if s.conn != nil {
			if err := s.conn.Close(); err != nil {
				s.errChan <- minorErr(err)
			}
		}

		if s.config.IsReconnectable {
			// duration: minTime = minTime ** exponential
			time.Sleep(s.backoff.Duration())
		}

		var err error
		s.conn, err = dialerf(s.ctx)
		if err != nil {
			// just warn if not reconnectable
			if !s.config.IsReconnectable {
				s.l.Errorln(criticalErr(err))
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
		s.l.Infoln("reconnected")

		// reconnect once only if panic occurs
		if !s.config.IsReconnectable {
			return
		}

	}
	s.l.Debugln("reconnect stopped")
}

func (s *Client) handleErrors() {
	for err := range s.errChan {
		if s.errorHandler != nil {
			s.errorHandler(err)
		}
	}
	s.l.Debugln("errors handling stopped")
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
			s.errChan <- criticalErr(fmt.Errorf("%v \n %s", r, string(debug.Stack())))
			<-s.reconnectedCh
			go s.readLoop()
		}
	}()

	for {
		select {
		case <-s.stopRead:
			close(s.stopReadWait)
			return

		default:
			if s.conn == nil {
				time.Sleep(5 * time.Second)
				s.errChan <- errNotConnected
				continue
			}
			msgType, msg, err := s.read()
			if err != nil {
				s.l.Errorln(err.Error())
				if s.isUnimportant(err) {
					break
				}
				if s.config.IsReconnectable && !s.isClosed {
					s.critErrChan <- criticalErr(err)
					<-s.reconnectedCh
				}
				break
			}

			if msgType == PingMessage {
				s.l.Debugln("ping received")
				s.Send(PongMessage, msg)
				break
			}

			if msg == nil {
				break
			}

			var payload []byte
			switch msgType {
			case BinaryMessage:
				compressionType := http.DetectContentType(msg)
				switch compressionType {
				case gzipCompressionType:
					payload, err = GZipDecompress(msg)
					if err != nil {
						s.errChan <- majorErr(err)
						payload = msg
					}
				case flateCompressionType:
					payload, err = FlateDecompress(msg)
					if err != nil {
						s.errChan <- majorErr(err)
						payload = msg
					}
				default:
					payload = msg
				}
			case TextMessage:
				payload = msg
			default:
				payload = msg
			}

			if len(payload) > 80 {
				s.l.Debugln("message received", string(payload[:80]))
			} else {
				s.l.Debugln("message received", string(payload))
			}

			tp, p := s.recognizeCustomPing(msgType, payload)
			if tp != 0 {
				s.l.Debugln("custom ping received")
				if err := s.Send(tp, p); err != nil {
					s.errChan <- majorErr(err)
				}
				break
			}

			if ok := s.recognizeAuth(payload); ok {
				break
			}

			s.messageHandler(payload)
		}
	}
}

func (s *Client) recognizeCustomPing(msgType int, payload []byte) (int, []byte) {
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

func (s *Client) recognizeAuth(data []byte) (itsAuth bool) {
	if s.authHandler == nil {
		return
	}

	if s.isAuthed {
		s.l.Debugln("already authenticated")
		return false
	}

	if s.authResp == nil {
		s.authChan <- struct{}{}
	}

	// auth response may look like just 'success' string
	if !json.Valid(s.authResp) {
		if string(s.authResp) == string(data) {
			s.authChan <- struct{}{}
			return true
		}
		s.l.Debugln("failed auth via invalid auth json. Response:", string(data))
		return
	}

	opts := DefaultJSONOptions()
	// SupersetMatch - means first item is a superset of a second item (first contains second).
	diff, _ := CompareJSON(data, s.authResp, &opts)
	if diff == SupersetMatch || diff == FullMatch {
		s.authChan <- struct{}{}
		return true
	}
	s.l.Debugln("failed auth. Diff:", diff.String())
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
	defer func() {
		if r := recover(); r != nil {
			s.l.Errorln(minorErr(fmt.Errorf("%v \n", r)))
		}
	}()
	s.isClosed = true

	close(s.stopRead)
	s.l.Debugln("stubborn closing...")
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
	s.l.Infoln("stubborn closed")
}
