# Stubborn WebSocket

Stubborn WebSocket is a framework that's written in [Go](http://golang.org/) for
[WebSocket](http://www.rfc-editor.org/rfc/rfc6455.txt) clients.

### Examples

* [Echo server example](https://github.com/filinvadim/stubborn/tree/master/example/main.go)

### Status

The Stubborn WebSocket framework provides a complete and tested persistent connection for 
the [WebSocket](http://www.rfc-editor.org/rfc/rfc6455.txt) protocol using various types of clients. 

### Installation

    go get github.com/filinvadim/stubborn

### Stubborn WebSocket advantages

1. Agnostic to any WebSocket clients by ```WSConnector interface```.
2. Therefore, it's fully testable using mock that implements three methods:
   ```
   Close() error
   ReadMessage() (messageType int, p []byte, err error)
   WriteMessage(messageType int, data []byte) error
   ```
3. Persistence. Stubborn WebSocket framework won't stop fetching data even if `panic()` occurs
   (that's useful if for example Gorilla WebSocket was used as client).
4. Fairly configurable for any WebSocker API requirements.
5. No external dependencies whatsoever.

### Stubborn WebSocket disadvantages

1. Framework wasn't benchmark tested but expected average speed.