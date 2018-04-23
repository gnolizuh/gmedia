package rtmp

import (
	"net"
	"time"
	"bufio"
)

type connState int

// A conn represents the server side of an RTMP connection.
type conn struct {
	server     *Server
	state      connState
	lepoch     uint32
	repoch     uint32
	digest     []byte
	rwc        net.Conn
	remoteAddr string
	bufr       *bufio.Reader
	bufw       *bufio.Writer
}

// Serve a new connection.
func (c *conn) serve() {
	c.remoteAddr = c.rwc.RemoteAddr().String()

	c.bufr = bufio.NewReader(c)
	c.bufw = bufio.NewWriter(c)

	c.handshake()
}

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
		server: srv,
		state:  StateServerSendChallenge,
		lepoch: time.Now().UnixNano() / 1000,
		repoch: 0,
		rwc:    rwc,
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
