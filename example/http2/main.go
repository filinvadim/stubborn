package main

import (
	"context"
	"fmt"
	"github.com/filinvadim/stubborn"
	h2 "github.com/posener/h2conn"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

type H2Adapter struct {
	*h2.Conn
}

func NewH2(ctx context.Context, url string) (*H2Adapter, error) {
	cli := h2.Client{
		Method: http.MethodPut,
		Header: http.Header{"client": []string{"stubborn"}},
		Client: nil,
	}
	conn, _, err := cli.Connect(ctx, url)
	return &H2Adapter{conn}, err
}

func (h *H2Adapter) ReadMessage() (messageType int, p []byte, err error) {
	buf := make([]byte, 256)

	_, err = h.Conn.Read(buf)
	return 0, buf, err
}

func (h *H2Adapter) WriteMessage(messageType int, data []byte) error {
	_, err := h.Conn.Write(data)
	return err
}

func (h *H2Adapter) Close() error {
	return h.Conn.Close()
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// graceful shutdown
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGTERM, syscall.SIGINT)

	url := "https://http2.golang.org/ECHO"

	stub := stubborn.NewStubborn(stubborn.Config{
		URL:             url,
		IsReconnectable: true,
		MessageType:     stubborn.TextMessage,
		Dialerf: func() (stubborn.WSConnector, error) {
			return NewH2(ctx, url)
		},
		AuthTimeOut: time.Second * 5,
	})

	// no auth for http2

	// no ping-pongs for http2

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
