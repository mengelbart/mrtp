package http

import "github.com/gorilla/websocket"

type websocketMessage struct{}

type WebsocketMessageHandler interface {
	HandleMessage()
}

type wsConn struct {
	conn       *websocket.Conn
	msgHandler WebsocketMessageHandler
}

func newWSConn(conn *websocket.Conn, handler WebsocketMessageHandler) *wsConn {
	return &wsConn{conn: conn, msgHandler: handler}
}

func (c *wsConn) readLoop() {
	for {
		var msg websocketMessage
		err := c.conn.ReadJSON(&msg)
		if err != nil {
			// TODO: handle error
			break
		}
		// TODO: handle message
		c.msgHandler.HandleMessage()
	}
}
