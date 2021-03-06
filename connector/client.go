package connector

import (
	"time"
	"net/http"
	"strings"
	"github.com/gorilla/websocket"
	"github.com/cihub/seelog"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second
	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second
	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// Maximum message size allowed from peer.
	maxMessageSize = 1024 * 1024
)

type Client struct {
	listens []string
	hub     *Hub
	conn    *websocket.Conn
	send    chan []byte
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		seelog.Error(err)
		return
	}
	r.ParseForm()
	var listens = strings.TrimSpace(r.Form.Get("listens"))
	if len(listens) == 0 {
		conn.Close()
		return
	}
	//如果listens里面有*的话则只保留*
	var checkListens = "," + listens + ",";
	if checkListens != ",*," && strings.Index(checkListens, ",*,") != -1 {
		listens = "*"
	}
	client := &Client{
		listens: strings.Split(listens, ","),
		hub: hub,
		conn: conn,
		send: make(chan []byte, 256),
	}
	hub.register <- client
	go client.push()
	client.listen()
}

func (c *Client) write(mt int, payload []byte) error {
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(mt, payload)
}

func (c *Client) listen() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				seelog.Errorf("close error: %v", err)
			}
			break
		}
		c.hub.Broadcast <- message
	}
}

func (c *Client) push() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.write(websocket.CloseMessage, []byte{})
				return
			}

			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.write(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

