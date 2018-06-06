package rtmp

import (
	"net"
	"log"
	"time"
	"errors"
	"fmt"
	"github.com/gnolizuh/rtmp/amf"
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

var serverMessageHandler = [MessageMax]MessageHandler{
	nil,
	serveSetChunkSize, serveAbort, serveAck, serveUserControl,
	serveWinAckSize, serveSetPeerBandwidth, serveEdge,
	serveAudio, serveVideo,
	nil, nil, nil, nil, nil,
	serveAmf3Meta, serveAmf3Shared, serveAmf3Cmd,
	serveAmf0Meta, serveAmf0Shared, serveAmf0Cmd,
	nil,
	serveAggregate,
}

func serveSetChunkSize(c *Conn, msg *Message) error {
	cs, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("set chunk size, cs: %d", cs)

	c.SetChunkSize(cs)

	return nil
}

func serveAbort(c *Conn, msg *Message) error {
	csid, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("abort, csid: %d", csid)

	return nil
}

func serveAck(c *Conn, msg *Message) error {
	seq, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("ack, seq: %d", seq)

	return nil
}

func serveUserControl(c *Conn, msg *Message) error {
	evt, err := readUint16(msg)
	if err != nil {
		return err
	}

	if UserMessageType(evt) >= UserMessageMax {
		return errors.New(fmt.Sprintf("user message type out of range: %d", evt))
	}

	uh := sh.userMessageHandlers[evt]
	if uh != nil {
		return uh(msg)
	}

	return errors.New(fmt.Sprintf("unexpected user message type: %d", evt))
}

func serveWinAckSize(c *Conn, msg *Message) error {
	win, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("ack window size, win: %d", win)

	c.ackWinSize = win

	return nil
}


func serveSetPeerBandwidth(c *Conn, msg *Message) error {
	bandwidth, err := readUint32(msg)
	if err != nil {
		return err
	}

	limit, err := readUint8(msg)
	if err != nil {
		return err
	}

	log.Printf("set peer bandwidth, bandwidth: %d, limit: %d", bandwidth, limit)

	return nil
}

func serveEdge(c *Conn, msg *Message) error {
	return nil
}

func serveAudio(c *Conn, msg *Message) error {
	return nil
}

func serveVideo(c *Conn, msg *Message) error {
	return nil
}

func serveAmf3Meta(c *Conn, msg *Message) error {
	return nil
}

func serveAmf3Shared(c *Conn, msg *Message) error {
	return nil
}

func serveAmf3Cmd(c *Conn, msg *Message) error {
	return nil
}

func serveAmf0Meta(c *Conn, msg *Message) error {
	return nil
}

func serveAmf0Shared(c *Conn, msg *Message) error {
	return nil
}

func serveAmf0Cmd(c *Conn, msg *Message) error {
	var name string
	err := amf.DecodeWithReader(msg, &name)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Printf("receive AMF0 command: name=%s\n", name)

	h, ok := sh.amfHandlers[name]
	if !ok {
		log.Printf("AMF command '%s' no handler", name)
		return nil
	}

	return h(msg)
}

func serveAggregate(c *Conn, msg *Message) error {
	return nil
}

type tcpKeepAliveListener struct {
	*net.TCPListener
}
