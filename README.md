# Stubborn 

Stubborn  is a framework that's written in [Go](http://golang.org/) for 
clients that uses duplex protocols such as 
[WebSocket](http://www.rfc-editor.org/rfc/rfc6455.txt).

### Examples

* [Echo wss client example](https://github.com/filinvadim/stubborn/tree/master/example/wss/main.go)
* [Echo http2 client example](https://github.com/filinvadim/stubborn/tree/master/example/http2/main.go)

### Status

The Stubborn framework provides a complete and tested persistent connection for 
the duplex protocols using various types of clients. 

### Installation

    go get github.com/filinvadim/stubborn

### Stubborn advantages

1. Agnostic to any clients by ```WSConnector interface```.
2. Therefore, it's fully testable using mock that implements three methods:
   ```
   Close() error
   ReadMessage() (messageType int, p []byte, err error)
   WriteMessage(messageType int, data []byte) error
   ```
3. Persistence. Stubborn framework won't stop fetching data even if `panic()` occurs
   (that's useful if for example Gorilla WebSocket was used as client).
4. Fairly configurable for any WebSocker API requirements.
5. No external dependencies whatsoever.

### Stubborn disadvantages

1. Framework wasn't benchmark tested but expected average speed.