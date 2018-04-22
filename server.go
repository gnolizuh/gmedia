package rtmp

import (
	"net"
	"sync"
	"time"
	"bufio"
)

// A conn represents the server side of an RTMP connection.
type conn struct {
						  // server is the server on which the connection arrived.
						  // Immutable; never nil.
	server *Server

						  // rwc is the underlying network connection.
						  // This is never wrapped by other types and is the value given out
						  // to CloseNotifier callers. It is usually of type *net.TCPConn or
						  // *tls.Conn.
	rwc net.Conn

						  // remoteAddr is rwc.RemoteAddr().String(). It is not populated synchronously
						  // inside the Listener's Accept goroutine, as some implementations block.
						  // It is populated immediately inside the (*conn).serve goroutine.
						  // This is the value of a Handler's (*Request).RemoteAddr.
	remoteAddr string

						  // bufr reads from r.
	bufr *bufio.Reader

						  // bufw writes to checkConnErrorWriter{c}, which populates werr on error.
	bufw *bufio.Writer
}

type Handler interface {
	ServeRTMP(*Client)
}

type Server struct {
	Addr    string  // TCP address to listen on, ":rtmp" if empty
	Handler Handler // handler to invoke, http.DefaultServeMux if nil

					// ReadTimeout is the maximum duration for reading the entire
					// request, including the body.
					//
					// Because ReadTimeout does not let Handlers make per-request
					// decisions on each request body's acceptable deadline or
					// upload rate, most users will prefer to use
					// ReadHeaderTimeout. It is valid to use them both.
	ReadTimeout time.Duration

					// ReadHeaderTimeout is the amount of time allowed to read
					// request headers. The connection's read deadline is reset
					// after reading the headers and the Handler can decide what
					// is considered too slow for the body.
	ReadHeaderTimeout time.Duration

					// WriteTimeout is the maximum duration before timing out
					// writes of the response. It is reset whenever a new
					// request's header is read. Like ReadTimeout, it does not
					// let Handlers make decisions on a per-request basis.
	WriteTimeout time.Duration

					// IdleTimeout is the maximum amount of time to wait for the
					// next request when keep-alives are enabled. If IdleTimeout
					// is zero, the value of ReadTimeout is used. If both are
					// zero, ReadHeaderTimeout is used.
	IdleTimeout time.Duration

	mu         sync.Mutex
	listeners  map[net.Listener]struct{}
	activeConn map[*conn]struct{}
	doneChan   chan struct{}
	onShutdown []func()
}

func ListenAndServe(addr string, handler Handler) error {
	server := &Server{Addr: addr, Handler: handler}
	return server.ListenAndServe()
}

// ListenAndServe listens on the TCP network address srv.Addr and then
// calls Serve to handle requests on incoming connections.
// Accepted connections are configured to enable TCP keep-alives.
// If srv.Addr is blank, ":rtmp" is used.
// ListenAndServe always returns a non-nil error.
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

// Serve a new connection.
func (c *conn) serve() {
	c.remoteAddr = c.rwc.RemoteAddr().String()

	c.bufr = bufio.NewReader(c)
	c.bufw = bufio.NewWriter(c)

	handshake(c)
}

type tcpKeepAliveListener struct {
	*net.TCPListener
}
