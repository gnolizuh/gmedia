package rtmp

import (
	"net"
	"time"
	"bufio"
	"sync"
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

// Create new connection from rwc.
func (srv *Server) newConn(rwc net.Conn) *conn {
	c := &conn{
		server:     srv,
		state:      StateServerSendChallenge,
		repoch:     0,
		rwc:        rwc,
		chunkSize:  DefaultChunkSize,
	}

	c.lepoch = uint32(time.Now().UnixNano() / 1000)
	c.remoteAddr = rwc.RemoteAddr().String()
	c.bufr = bufio.NewReader(rwc)
	c.bufw = bufio.NewWriter(rwc)
	c.streams = make([]*Stream, MaxStreamsNum)
	c.chunkPool = &sync.Pool{
		New: func() interface{} {
			ck := make([]byte, c.chunkSize)
			return &ck
		},
	}

	return c
}

func (srv *Server) Serve(l net.Listener) error {
	defer l.Close()

	for {
		rw, e := l.Accept()
		if e != nil {
			return e
		}
		c := srv.newConn(rw)
		go c.serve()
	}
}

type tcpKeepAliveListener struct {
	*net.TCPListener
}
