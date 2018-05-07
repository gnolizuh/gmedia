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
		go c.serve()
	}
}

type tcpKeepAliveListener struct {
	*net.TCPListener
}
