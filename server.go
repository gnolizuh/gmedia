package rtmp

import (
	"net"
	"time"
)

type Handler interface {
	ServeRTMP(*Client)
}

type Server struct {
	Addr        string
	Handler     Handler
	ReadTimeout time.Duration
}

func ListenAndServe(addr string, handler Handler) error {
	server := &Server{
		Addr: addr,
		Handler: handler,
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
	defer l.Close()

	for {
		rw, e := l.Accept()
		if e != nil {
			return e
		}

		c := newConn(rw)

		// Register message reader and set connection state.
		c.msgReader = srv
		c.state = StateServerRecvChallenge

		go c.serve()
	}
}

func (srv *Server) readMessage(c *Conn, msg *Message) error {
	// message callback.
	return nil
}

func (srv *Server) OnSetChunkSize() error {
	return nil
}

func (srv *Server) OnAbort() error {
	return nil
}

func (srv *Server) OnAck() error {
	return nil
}

func (srv *Server) OnUserControl() error {
	return nil
}

func (srv *Server) OnWinAckSize() error {
	return nil
}

func (srv *Server) OnSetPeerBandwidth() error {
	return nil
}

func (srv *Server) OnEdge() error {
	return nil
}

func (srv *Server) OnAudio() error {
	return nil
}

func (srv *Server) OnVideo() error {
	return nil
}

func (srv *Server) OnAmf() error {
	return nil
}

func (srv *Server) OnAggregate() error {
	return nil
}

type tcpKeepAliveListener struct {
	*net.TCPListener
}
