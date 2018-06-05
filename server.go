package rtmp

import (
	"net"
	"time"
	"bufio"
)

type Handler_ interface {
	ServeRTMP(MessageType, *Peer)
}

type Server struct {
	Addr        string
	ReadTimeout time.Duration
}

func ListenAndServe(addr string) error {
	server := &Server{
		Addr: addr,
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

		// Set connection state.
		c.state = StateServerRecvChallenge
		c.handler = newServerHandler(c)

		go c.serve()
	}
}

func (srv *Server) OnUserControl(event uint16, reader *bufio.Reader) error {
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
