//
// Copyright [2024] [https://github.com/gnolizuh]
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package rtmp

import (
	"bufio"
	"net"
	"reflect"
	"time"
)

type ServeState uint

type tcpKeepAliveListener struct {
	*net.TCPListener
}

//type Handler interface {
//	ServeNew(*Peer) ServeState
//	ServeMessage(MessageType, *Peer) ServeState
//	ServeUserMessage(UserMessageType, *Peer) ServeState
//	ServeCommand(string, *Peer) ServeState
//}

type Handler interface {
	ServeMessage(*ChunkStream, *Message) error
}

type Server struct {
	// Addr optionally specifies the TCP address for the server to listen on,
	// in the form "host:port". If empty, ":rtmp" (port 1935) is used.
	Addr string

	// Handler to invoke, rtmp.DefaultServeMux if nil
	Handler any

	// If Handler is set, the TypeHandlers never used. Otherwise, look for
	// the correct callback function in TypeHandlers.
	TypeHandlers []TypeHandler

	// If either Handler or TypeHandlers with MessageTypeUserControl is set,
	// the UserHandlers never used. Otherwise, look for the correct callback
	// function in UserHandlers.
	UserHandlers []TypeHandler

	// ConnState specifies an optional callback function that is
	// called when a client connection changes state. See the
	// ConnState type and associated constants for details.
	ConnState func(net.Conn, ConnState)
}

func ListenAndServe(addr string, handler any) error {
	server := &Server{
		Addr:         addr,
		Handler:      handler,
		TypeHandlers: make([]TypeHandler, MessageTypeMax),
	}
	return server.ListenAndServe()
}

func (srv *Server) ListenAndServe() error {
	addr := srv.Addr
	if addr == "" {
		addr = ":rtmp"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	return srv.Serve(tcpKeepAliveListener{ln.(*net.TCPListener)})
}

func (srv *Server) Serve(l net.Listener) error {
	defer func(l net.Listener) {
		_ = l.Close()
	}(l)

	for {
		rw, e := l.Accept()
		if e != nil {
			return e
		}

		c := srv.newConn(rw)

		// init connection state with StateServerRecvChallenge which
		// means peer is server side.
		c.setState(c.rwc, StateServerNew)
		go c.serve()
	}
}

func (srv *Server) RegisterTypeHandler(typ MessageType, th TypeHandler) {
	if typ < MessageTypeMax {
		srv.TypeHandlers[typ] = th
	}
}

// serverHandler delegates to either the server's Handler or
// DefaultServeMux.
type serverHandler struct {
	srv *Server
}

var (
	handlerType = reflect.TypeOf((*Handler)(nil)).Elem()
)

func (sh *serverHandler) ServeMessage(cs *ChunkStream, msg *Message) error {
	th := sh.srv.TypeHandlers[msg.Header.MessageTypeId]
	if th != nil {
		return th(cs, msg)
	}

	e := reflect.ValueOf(sh.srv.Handler).Elem()
	t := e.Type()
	for i := 0; i < e.NumField(); i++ {
		if t.Field(i).Type.Implements(handlerType) {
			handler := sh.srv.Handler.(Handler)
			return handler.ServeMessage(cs, msg)
		}
	}

	return DefaultServeMux.ServeMessage(cs, msg)
}

// --------------------------------------------- conn --------------------------------------------- //

// Create new connection from conn.
func (srv *Server) newConn(rwc net.Conn) *conn {
	c := &conn{
		server:    srv,
		rwc:       rwc,
		chunkSize: DefaultReadChunkSize,
	}

	c.epoch = uint32(time.Now().UnixNano() / 1000)

	c.bufr = bufio.NewReader(rwc)
	c.bufw = bufio.NewWriter(rwc)

	c.chunkStreams = make([]ChunkStream, MaxStreamsNum)
	for i := range c.chunkStreams {
		c.chunkStreams[i].conn = c
	}

	c.peer = Peer{
		RemoteAddr: c.rwc.RemoteAddr().String(),
		conn:       c,
	}

	return c
}
