# Stubborn 

Stubborn  is a framework that's written in [Go](http://golang.org/) for 
clients that uses duplex protocols such as 
[WebSocket](http://www.rfc-editor.org/rfc/rfc6455.txt).

### Status

The Stubborn framework provides a complete and tested persistent connection for 
the duplex protocols using various types of clients. 

### Installation

    go get github.com/filinvadim/stubborn

### Stubborn advantages

1. Agnostic to any clients by ```DuplexConnector interface```.
2. Therefore, it's fully testable using mock that implements three methods:
   ```
   Close() error
   ReadMessage() (messageType int, p []byte, err error)
   WriteMessage(messageType int, data []byte) error
   ```
3. Persistence. Stubborn framework won't stop fetching data even if `panic()` occurs
   (that's useful if for example Gorilla WebSocket was used as client).
4. Fairly configurable for any API requirements.
5. No external dependencies whatsoever.

### Stubborn disadvantages

1. Framework wasn't benchmark tested but expected average speed.


### Protocol examples

* [Echo wss client example](https://github.com/filinvadim/stubborn/tree/master/example/wss/main.go)
* [Echo http2 client example](https://github.com/filinvadim/stubborn/tree/master/example/http2/main.go)


### Crypto exchange examples

* Binance
```
func BinanceStream(key, secret string) (err error) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGTERM, syscall.SIGINT)

	var stream binance.BinanceAPIStreamer

	stub := stubborn.NewStubborn(stubborn.Config{
		URL:             "wss://stream.binance.com:9443/ws/",
		IsReconnectable: true,
		MessageType:     stubborn.TextMessage,
		Print: func(args ...interface{}) {
			fmt.Println(args)
			return
		},
		Dialerf: func(ctx context.Context) (stubborn.DuplexConnector, error) {
			if stream != nil {
				stream.DeleteStream()
			}
			stream, err = b.StartUserDataStream(key, secret)
			if err != nil {
				return nil, err
			}
			conn, _, err := websocket.DefaultDialer.DialContext(
				ctx,
				fmt.Sprintf("%s%s", "wss://stream.binance.com:9443/ws/", stream.ListenKey()),
				nil)
			return conn, err
		},
		UnimportantErrs: []error{io.EOF},
	})

	stub.SetErrorHandler(func(err error) { fmt.Println("binance websocket error:", err) })

	stub.SetKeepAliveHandler(stubborn.KeepAlive{
		Tick: time.Tick(time.Hour - time.Minute),
		CustomPing: func() (msgType int, payload []byte) {
			stream.KeepAliveStream()
			return stubborn.PingMessage, []byte{}
		},
	})

	stub.SetMessageHandler(func(resp []byte) {
		fmt.Println("RESPONSE:", string(resp))
	})

	err = stub.Connect(b.ctx)
	if err != nil {
		return err
	}
	<-interrupt
    
	defer func() {
		stream.DeleteStream()
		stub.Close()
	}
	return nil
}
```
* Huobi
```
func HuobiTradesSubscribe(key, secret string) (err error) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGTERM, syscall.SIGINT)
	
	sub := "{\"action\":\"sub\", \"ch\":\"trade.clearing#*#0\", \"cid\": \"someId\"}"
	unsub := "{\"action\":\"unsub\", \"ch\":\"trade.clearing#*\", \"cid\": \"someId\"}"

	path := "/ws/v2"
	wsUrl := fmt.Sprintf("wss://%s%s", "api-aws.huobi.pro", path)

	stub := stubborn.NewStubborn(stubborn.Config{
		URL:             wsUrl,
		IsReconnectable: true,
		MessageType:     stubborn.TextMessage,
		Print: func(args ...interface{}) {
			fmt.Println(args)
			return
		},
		Dialerf: func(ctx context.Context) (stubborn.DuplexConnector, error) {
			conn, _, err := websocket.DefaultDialer.DialContext(
				ctx,
				wsUrl,
				nil)

			return conn, err
		},
		AuthTimeOut:     5 * time.Second,
		UnimportantErrs: []error{io.EOF},
	})
	stub.SetAuthHandler(func() (req []byte, resp []byte, err error) {
		req, err = new(huobi.WebSocketV2RequestBuilder).Build(key, secret, "api-aws.huobi.pro", path)
		if err != nil {
			return nil, nil, err
		}

		resp = []byte(`{
			"action": "req",
				"code": 200,
				"ch": "auth",
				"data": {}
		}`)
		return
	})

	stub.SetErrorHandler(func(err error) {
		fmt.Println("huobi websocket error:", err)

		if errors.Is(err, stubborn.ErrCritical) {
			// resubscribe
			err = stub.Send(stubborn.TextMessage, []byte(sub))
			if err != nil {
				fmt.Println("huobi websocket error:", err)
			}
		}
	})

	stub.SetKeepAliveHandler(stubborn.KeepAlive{
		Tick: time.After(1 * time.Hour), 
		CustomPong: func(typ int, payload []byte) (int, []byte) {
			ping := struct {
				Action string `json:"action"`
				Data   *struct {
					Timestamp int64 `json:"ts"`
				} `json:"data"`
			}{}
			err := json.Unmarshal(payload, &ping)
			if err != nil {
				return 0, nil
			}
			
			pong := fmt.Sprintf("{\"action\": \"pong\", \"data\": { \"ts\": %d } }", ping.Data.Timestamp)
			return stubborn.TextMessage, []byte(pong)
		},
	})

	stub.SetMessageHandler(func(resp []byte) {
		fmt.Println("RESPONSE:", string(resp))
	})

	err = stub.Connect(hc.ctx)
	if err != nil {
		return err
	}

	// subscribe
	err = stub.Send(stubborn.TextMessage, []byte(sub))
	if err != nil {
		return err
	}
	<-interrupt
	defer func() {
		stub.Send(stubborn.TextMessage, []byte(unsub))
		stub.Close()
	}
	return nil
}
```
* Okex
```
func OkexOrdersSubscribe(instruments []string, key, secret, pass string) (err error) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGTERM, syscall.SIGINT)
	
	var (
		url  = "wss://real.okex.com:8443/ws/v3?compress=true"
		args = make([]string, 0)
	)

	for _, i := range instruments {
		args = append(args, "spot/order:"+i)
	}

	subOp := BaseOp{
		Op:   "subcribe",
		Args: args,
	}

	subMsg, _ := json.Marshal(subOp)

	stub := stubborn.NewStubborn(stubborn.Config{
		URL:             url,
		IsReconnectable: true,
		MessageType:     stubborn.TextMessage,
		Print: func(args ...interface{}) {
			fmt.Println(args)
			return
		},
		Dialerf: func(ctx context.Context) (stubborn.DuplexConnector, error) {
			conn, _, err := websocket.DefaultDialer.DialContext(
				ctx,
				url,
				nil,
			)

			return conn, err
		},
		AuthTimeOut:     5 * time.Second,
		UnimportantErrs: []error{io.EOF},
	})

	stub.SetAuthHandler(func() (req []byte, resp []byte, err error) {
		resp = []byte(`{"event":"login","success":true}`)
        et := epochTime()
		signingString := et + http.MethodGet + "/users/self/verify"
		signed, err := hmacSha256Base64Sign(signingString, secret)
		if err != nil {
			return nil, nil, err
		}

		b := BaseOp{
			Op:   loginOp,
			Args: []string{key, pass, et, signed},
		}
		req, err = json.Marshal(b)
		return
	})

	stub.SetErrorHandler(func(err error) {
		fmt.Println("okex websocket error:", err)
		if errors.Is(err, stubborn.ErrCritical) {
			// resubscribe
			err = stub.Send(stubborn.TextMessage, subMsg)
			if err != nil {
				fmt.Println("okex websocket error:", err)
			}
		}
	})

	stub.SetKeepAliveHandler(stubborn.KeepAlive{
		Tick: time.Tick(25 * time.Second),
		CustomPing: func() (msgType int, payload []byte) {
			return stubborn.TextMessage, []byte("ping")
		},
	})

	stub.SetMessageHandler(func(resp []byte) {
		fmt.Println("RESPONSE:", string(resp))
	})

	err = stub.Connect(oc.ctx)
	if err != nil {
		return err
	}

	// subscribe
	err = stub.Send(stubborn.TextMessage, subMsg)
	if err != nil {
		return err
	}
	<-interrupt
	defer func() {
		unsubOp := BaseOp{
			Op:   "unsubcribe",
			Args: args,
		}
		unsubMsg, _ := json.Marshal(unsubOp)
		stub.Send(stubborn.TextMessage, unsubMsg)
		stub.Close()
	}
	return nil
}
```