package stubborn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"

	//sync "github.com/sasha-s/go-deadlock"
	"time"
)

var (
	errNotInit          = errors.New("stubborn wasn't initialized")
	errNotConnected     = errors.New("stubborn is not connected")
	errAuthTimeout      = errors.New("auth timeout")
	errNoMessageHandler = errors.New("message handler wasn't set")
	errNoDialer         = errors.New("dialer wasn't set")

	ErrCritical = errors.New("stubborn critical")
	ErrMajor    = errors.New("stubborn major")
	ErrMinor    = errors.New("stubborn minor")
)

// TODO use opts pattern
type Config struct {
	IsReconnectable bool
	// use your favourite logger
	Logger CustomLogger
	// use your favourite client
	Dialerf     DialerFunc
	AuthTimeOut time.Duration
	// it is easier to define expected error because exceptions always unexpected :)
	UnimportantErrs []error
	// deprecated
	MessageType int
}

type crashController struct {
	reconnectDeadChan chan struct{}
	keepaliveDeadChan chan struct{}
	readloopDeadChan  chan struct{}
}

type Client struct {
	ctx context.Context
	l   CustomLogger

	config Config
	connMx *sync.RWMutex
	conn   DuplexConnector
	cc     crashController

	// healthcheck processing fields
	keep      *KeepAlive
	timeMx    *sync.Mutex
	timeAlive time.Time

	// auth processing fields
	authHandler AuthHandler
	authReq     []byte
	authResp    []byte
	authChan    chan struct{}

	// messages processing fields
	messageHandler MessageHandler
	errorHandler   ErrorHandler
	errChan        chan error

	// reconnecting processing fields
	startReconnChan  chan struct{}
	backoff          *backoff
	finishReconnChan chan struct{}

	authStatus   uint32
	closedStatus uint32
}

func NewStubborn(
	conf Config,
) *Client {
	s := new(Client)

	s.authChan = make(chan struct{}, 1)
	s.errChan = make(chan error, 100)
	s.startReconnChan = make(chan struct{}, 1)
	s.finishReconnChan = make(chan struct{}, 1)
	s.connMx = new(sync.RWMutex)
	s.timeMx = new(sync.Mutex)
	s.config = conf

	s.l = conf.Logger
	if s.l == nil {
		s.l = defaultLogger{}
	}

	s.cc = crashController{
		reconnectDeadChan: make(chan struct{}, 1),
		keepaliveDeadChan: make(chan struct{}, 1),
		readloopDeadChan:  make(chan struct{}, 1),
	}

	return s
}

func (s *Client) isClosed() bool {
	return atomic.LoadUint32(&s.closedStatus) == 1
}

func (s *Client) isAuthed() bool {
	return atomic.LoadUint32(&s.authStatus) == 1
}

func (s *Client) setClosed() {
	atomic.StoreUint32(&s.closedStatus, 1)
}

func (s *Client) setIsAuthed(success bool) {
	if success {
		atomic.StoreUint32(&s.authStatus, 1)
		return
	}
	atomic.StoreUint32(&s.authStatus, 0)
}

// SetAuthHandler auth callback handler
func (s *Client) SetAuthHandler(authH AuthHandler) {
	if s == nil {
		return
	}
	if s.conn != nil {
		return
	}

	s.authHandler = authH
}

// SetMessageHandler message handler excluding errors
func (s *Client) SetMessageHandler(msgH MessageHandler) {
	if s == nil {
		return
	}
	if s.conn != nil {
		return
	}

	s.messageHandler = msgH
}

// SetErrorHandler error handler excluding messages
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

	if s.authHandler != nil {
		s.authReq, s.authResp, err = s.authHandler()
		if err != nil {
			return fmt.Errorf("auth building: %w", err)
		}
	}

	s.ctx = ctx
	dialerf := s.config.Dialerf
	s.conn, err = dialerf(s.ctx)
	if err != nil {
		return err
	}

	go s.trackCrashes()
	go s.readLoop()

	err = s.auth()
	if err != nil {
		s.Close()
		return err
	}
	s.l.Infoln("connection established")
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

	s.timeAlive = time.Now()
	if s.keep != nil {
		go s.keepAlive()
	}

	return nil
}

func (s *Client) ManualReconnect() {
	if !s.isClosed() {
		_ = s.conn.Close()
	}
}

func (s *Client) trackCrashes() {
	for {
		select {
		case _, ok := <-s.cc.readloopDeadChan:
			if !ok {
				return
			}
			go s.readLoop()
		case _, ok := <-s.cc.reconnectDeadChan:
			if !ok {
				return
			}
			if s.config.IsReconnectable {
				go s.reconnect()
			}
		case _, ok := <-s.cc.keepaliveDeadChan:
			if !ok {
				return
			}
			if s.keep != nil {
				go s.keepAlive()
			}
		}
	}
}

func (s *Client) auth() error {
	if s.authReq == nil {
		return nil
	}

	s.setIsAuthed(false)

	timeOut := s.config.AuthTimeOut
	if timeOut == 0 {
		timeOut = time.Second * 5
	}

	err := s.Send(TextMessage, s.authReq)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	select {
	case <-s.authChan:
		s.setIsAuthed(true)
	case <-time.After(timeOut):
		return errAuthTimeout
	}
	return nil
}

func (s *Client) keepAlive() {
	aliveTicker := time.NewTicker(30 * time.Minute)
	defer aliveTicker.Stop()
	var err error

	// we can't rely on select random therefore pinging must be called first despite everything
	go func() {
		if s.keep.Tick == 0 {
			return
		}
		for range time.Tick(s.keep.Tick) {
			if s.isClosed() {
				return
			}
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
				continue
			}
			s.l.Debugln("custom ping sent")
		}
	}()

	for range aliveTicker.C {
		if s.isClosed() {
			return
		}
		s.timeMx.Lock()
		s.l.Infoln(
			"ws connection alive ",
			time.Now().Sub(s.timeAlive).Round(time.Minute),
		)
		s.timeMx.Unlock()
	}
}

func (s *Client) Send(msgType int, message []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("sending: %v \n", r)
		}
	}()
	if s == nil {
		return errNotInit
	}

	if s.isClosed() {
		return nil
	}

	s.connMx.Lock()
	defer s.connMx.Unlock()
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
			s.cc.reconnectDeadChan <- struct{}{}
			s.errChan <- majorErr(fmt.Errorf("reconnect: %v \n", r))
		}
	}()

	for range s.startReconnChan {
		if s.isClosed() {
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

		newConn, err := dialerf(s.ctx)
		if err != nil {
			// just warn if not reconnectable
			if !s.config.IsReconnectable {
				s.l.Errorln(err)
				return
			}
			s.startReconnChan <- struct{}{}
			continue
		}
		s.connMx.Lock()
		s.conn = newConn
		s.connMx.Unlock()

		s.finishReconnChan <- struct{}{} // finish it here so auth can be processed

		err = s.auth()
		if err != nil {
			s.startReconnChan <- struct{}{}
			continue
		}

		s.l.Infoln("reconnected")

		s.timeMx.Lock()
		s.timeAlive = time.Now()
		s.timeMx.Unlock()

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
			s.errChan <- majorErr(fmt.Errorf("read loop: %v \n", r))
			s.cc.readloopDeadChan <- struct{}{}
		}
	}()

	for {
		if s.isClosed() {
			s.closeChannels()
			return
		}
		if s.conn == nil {
			time.Sleep(5 * time.Second)
			s.errChan <- minorErr(errNotConnected)
			continue
		}
		msgType, msg, err := s.read()
		if err != nil {
			if s.isUnimportant(err) {
				s.errChan <- minorErr(err)
				continue
			}

			if s.config.IsReconnectable && !s.isClosed() {
				s.errChan <- criticalErr(err)
				s.startReconnChan <- struct{}{}
				<-s.finishReconnChan
			}
			continue
		}

		payload := s.parseMessage(msgType, msg)
		if payload == nil {
			continue
		}

		tp, p := s.recognizeCustomPing(msgType, payload)
		if tp != 0 {
			s.l.Debugln("custom ping received")
			if err := s.Send(tp, p); err != nil {
				s.errChan <- majorErr(err)
			}
			continue
		}

		if ok := s.recognizeAuth(payload); ok {
			continue
		}

		s.messageHandler(payload)
	}
}

func (s *Client) closeChannels() {
	close(s.startReconnChan)
	close(s.finishReconnChan)
	close(s.authChan)
	close(s.cc.readloopDeadChan)
	close(s.cc.reconnectDeadChan)
	close(s.cc.keepaliveDeadChan)
	close(s.errChan)
}

func (s *Client) parseMessage(msgType int, msg []byte) (payload []byte) {
	if msg == nil {
		return nil
	}

	var err error

	switch msgType {
	case PingMessage:
		s.l.Debugln("ping received")
		s.Send(PongMessage, msg)
		return nil
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
	return payload
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

	if s.isAuthed() {
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
	if s.isClosed() {
		return
	}
	s.setClosed()

	s.connMx.Lock()
	s.conn.Close()
	s.connMx.Unlock()

	s.l.Infoln("stubborn closed")
}
