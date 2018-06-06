package rtmp

import (
	"net"
	"time"
	"errors"
)

type ServeState uint

const (
	ServeError ServeState = iota
	ServeDeclined
)

type Handler interface {
	ServeNew(*Peer) ServeState
	ServeMessage(MessageType, *Peer) ServeState
	ServeUserMessage(UserMessageType, *Peer) ServeState
	ServeCommand(CommandName, *Peer) ServeState
}

type Server struct {
	Addr        string
	ReadTimeout time.Duration
	Handler     Handler
}

func ListenAndServe(addr string, handler Handler) error {
	server := &Server{Addr: addr, Handler: handler}
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
	defer l.Close()

	for {
		rw, e := l.Accept()
		if e != nil {
			return e
		}

		c := newConn(rw)

		// Set connection state.
		c.setState(StateServerRecvChallenge)
		c.handler = &serverHandler{ srv: srv, c: c }

		go c.serve()
	}
}

type serverHandler struct {
	srv *Server
	c   *Conn
}

func (sh *serverHandler) ServeNew() error {
	h := sh.srv.Handler
	if h != nil {
		if h.ServeNew(&sh.c.peer) != ServeDeclined {
			return errors.New("ServeNew: serve peer failed")
		}
	}
	return nil
}

func (sh *serverHandler) ServeMessage(typo MessageType) error {
	h := sh.srv.Handler
	if h != nil {
		if h.ServeMessage(typo, &sh.c.peer) != ServeDeclined {
			return errors.New("ServeMessage: serve peer failed")
		}
	}
	return nil
}

func (sh *serverHandler) ServeUserMessage(typo UserMessageType) error {
	if sh.srv.Handler != nil {
		sh.srv.Handler.ServeUserMessage(typo, &sh.c.peer)
	}
	return nil
}

func (sh *serverHandler) ServeCommand(name CommandName) error {
	if sh.srv.Handler != nil {
		sh.srv.Handler.ServeCommand(name, &sh.c.peer)
	}
	return nil
}

var serverMessageHandler = messageHandler{

}

type messageHandler struct {

}

type tcpKeepAliveListener struct {
	*net.TCPListener
}
