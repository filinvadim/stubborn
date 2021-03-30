package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/filinvadim/stubborn"
	"github.com/gorilla/websocket"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// graceful shutdown
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGTERM, syscall.SIGINT)

	url := "wss://echo.websocket.org"

	stub := stubborn.NewStubborn(stubborn.Config{
		URL:             url,
		IsReconnectable: true,
		MessageType:     stubborn.TextMessage,
		Dialerf: func() (stubborn.WSConnector, error) {
			conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
			return conn, err
		},
		AuthTimeOut: time.Second * 5,
	})

	stub.SetAuthHandler(func() (msg []byte, resp []byte, err error) {
		// auth request
		req := []byte("auth")
		// expected auth success response
		bt := []byte("auth")
		return req, bt, nil
	})

	stub.SetKeepAliveHandler(stubborn.KeepAlive{
		Tick: time.After(1 * time.Minute),
		CustomPing: func() (msgType int, payload []byte) {
			return stubborn.TextMessage, []byte("PING")
		},
		CustomPong: func(typ int, payload []byte) (int, []byte) {
			ping := struct {
				Action string `json:"action"`
				Data   *struct {
					Timestamp int64 `json:"ts"`
				} `json:"data"`
			}{}
			err := json.Unmarshal(payload, &ping)
			if err != nil || ping.Action != "ping" {
				return 0, nil
			}
			if ping.Data == nil {
				return stubborn.PongMessage, nil
			}
			pong := fmt.Sprintf("{\"action\": \"pong\", \"data\": { \"ts\": %d } }", ping.Data.Timestamp)
			return stubborn.TextMessage, []byte(pong)
		},
	})

	stub.SetMessageHandler(func(resp []byte) {
		fmt.Println("RESPONSE:", string(resp))
	})

	stub.SetErrorHandler(func(err error) {
		fmt.Println("ERROR:", err)
	})

	err := stub.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}

	for i, m := range []string{" hey ", " hey ", " hey ", " hey ", " hey "} {
		stub.Send(stubborn.TextMessage, []byte(strconv.Itoa(i)+m))
	}
	defer stub.Close()

	<-interrupt
}
