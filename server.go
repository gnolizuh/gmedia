package rtmp

import (
	"net"
	"log"
	"time"
	"errors"
	"fmt"
	"github.com/gnolizuh/rtmp/amf"
)

const (
	DefaultAckWindowSize = 5000000
)

type ServeState uint

type tcpKeepAliveListener struct {
	*net.TCPListener
}

const (
	ServeDone ServeState = iota
	ServeError
	ServeDeclined
)

type Handler interface {
	ServeNew(*Peer) ServeState
	ServeMessage(MessageType, *Peer) ServeState
	ServeUserMessage(UserMessageType, *Peer) ServeState
	ServeCommand(string, *Peer) ServeState
}

type Server struct {
	Addr        string
	ReadTimeout time.Duration
	Handler     Handler
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

		// Set connection state.
		c.setState(StateServerRecvChallenge)
		c.handler = &serverHandler{ srv: srv, c: c }
		c.peer.handler = srv.Handler

		go c.serve()
	}
}

type serverHandler struct {
	srv *Server
	c   *Conn
}

func (sh *serverHandler) serveNew(peer *Peer) error {
	h := sh.srv.Handler
	if h != nil {
		switch h.ServeNew(peer) {
		case ServeDone:
		case ServeDeclined:
		case ServeError:
			return errors.New("ServeNew: serve peer failed")
		}
	}
	return nil
}

func (sh *serverHandler) serveMessage(typo MessageType, peer *Peer) error {
	h := sh.srv.Handler
	if h != nil {
		switch h.ServeMessage(typo, peer) {
		case ServeDone:
		case ServeDeclined:
			if h := serverMessageHandler[typo]; h != nil {
				h(peer)
			} else {
				return errors.New(fmt.Sprintf("RTMP message type %d unknown", typo))
			}
		case ServeError:
			return errors.New("ServeMessage: serve peer failed")
		}
	}
	return nil
}

func serveUserMessage(utypo UserMessageType, peer *Peer) error {
	h := peer.handler
	if h != nil {
		switch h.ServeUserMessage(utypo, peer) {
		case ServeDone:
		case ServeDeclined:
			if uh := serverUserMessageHandlers[utypo]; uh != nil {
				uh(peer)
			} else {
				return errors.New(fmt.Sprintf("RTMP user message type %d unknown", utypo))
			}
		case ServeError:
			return errors.New("ServeUserMessage: serve peer failed")
		}
	}
	return nil
}

func serveCommand(name string, peer *Peer) error {
	h := peer.handler
	if h != nil {
		switch h.ServeCommand(name, peer) {
		case ServeDone:
		case ServeDeclined:
			h, ok := serverAMFHandlers[name]
			if !ok {
				log.Printf("AMF command '%s' no handler", name)
				return nil
			}

			return h(peer)
		case ServeError:
			return errors.New("ServeUserMessage: serve peer failed")
		}
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

func serveSetChunkSize(peer *Peer) error {
	cs, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("set chunk size, cs: %d", cs)

	peer.Conn.SetChunkSize(cs)

	return nil
}

func serveAbort(peer *Peer) error {
	csid, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("abort, csid: %d", csid)

	return nil
}

func serveAck(peer *Peer) error {
	seq, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("ack, seq: %d", seq)

	return nil
}

func serveUserControl(peer *Peer) error {
	evt, err := peer.Reader.ReadUint16()
	if err != nil {
		return err
	}

	utypo := UserMessageType(evt)
	if utypo >= UserMessageMax {
		return errors.New(fmt.Sprintf("user message type out of range: %d", utypo))
	}

	return serveUserMessage(utypo, peer)
}

func serveWinAckSize(peer *Peer) error {
	win, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("ack window size, win: %d", win)

	peer.Conn.ackWinSize = win

	return nil
}

func serveSetPeerBandwidth(peer *Peer) error {
	bandwidth, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	limit, err := peer.Reader.ReadUint8()
	if err != nil {
		return err
	}

	log.Printf("set peer bandwidth, bandwidth: %d, limit: %d", bandwidth, limit)

	return nil
}

func serveEdge(peer *Peer) error {
	return nil
}

func serveAudio(peer *Peer) error {
	return nil
}

func serveVideo(peer *Peer) error {
	return nil
}

func serveAmf3Meta(peer *Peer) error {
	return nil
}

func serveAmf3Shared(peer *Peer) error {
	return nil
}

func serveAmf3Cmd(peer *Peer) error {
	return nil
}

func serveAmf0Meta(peer *Peer) error {
	return nil
}

func serveAmf0Shared(peer *Peer) error {
	return nil
}

func serveAmf0Cmd(peer *Peer) error {
	var name string
	err := amf.DecodeWithReader(peer.Reader, &name)
	if err != nil {
		log.Println(err)
		return err
	}
	return serveCommand(name, peer)
}

func serveAggregate(peer *Peer) error {
	return nil
}

var serverUserMessageHandlers = [UserMessageMax]UserMessageHandler{
	serveUserStreamBegin, serveUserStreamEOF, serveUserStreamDry,
	serveUserSetBufLen, serveUserIsRecorded, nil,
	serveUserPingRequest, serveUserPingResponse,
}

func serveUserStreamBegin(peer *Peer) error {
	msid, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: stream_begin msid=%d", msid)

	return nil
}

func serveUserStreamEOF(peer *Peer) error {
	msid, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: stream_eof msid=%d", msid)

	return nil
}

func serveUserStreamDry(peer *Peer) error {
	msid, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: stream_dry msid=%d", msid)

	return nil
}

func serveUserSetBufLen(peer *Peer) error {
	msid, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	buflen, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: set_buflen msid=%d buflen=%d", msid, buflen)

	return nil
}

func serveUserIsRecorded(peer *Peer) error {
	msid, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: recorded msid=%d", msid)

	return nil
}

func serveUserPingRequest(peer *Peer) error {
	timestamp, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: ping request timestamp=%d", timestamp)

	// TODO: send ping response.

	return nil
}

func serveUserPingResponse(peer *Peer) error {
	timestamp, err := peer.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: ping response timestamp=%d", timestamp)

	// TODO: reset next ping request.

	return nil
}

var serverAMFHandlers = map[string]AMFCommandHandler{
	"connect":       serveConnect,
	"releaseStream": serveReleaseStream,
	"createStream":  serveCreateStream,
	"closeStream":   serveCloseStream,
	"deleteStream":  serveDeleteStream,
	"FCPublish":     serveFCPublish,
	"publish":       servePublish,
	"play":          servePlay,
	"play2":         servePlay2,
	"seek":          serveSeek,
	"pause":         servePause,
	"pauseraw":      servePause,
}

func serveConnect(peer *Peer) error {
	type Object struct {
		App            string
		FlashVer       string
		SwfURL         string
		TcURL          string
		AudioCodecs    uint32
		VideoCodecs    uint32
		PageUrl        string
		ObjectEncoding uint32
	}

	var transactionID uint
	err := amf.DecodeWithReader(peer.Reader, &transactionID)
	if err != nil {
		return err
	}

	if transactionID != 1 {
		return errors.New(fmt.Sprintf("unexpected transaction ID: %d", transactionID))
	}

	var obj Object
	err = amf.DecodeWithReader(peer.Reader, &obj)
	if err != nil {
		log.Println(err)
		return err
	}

	// TODO: send _result
	if err := peer.Conn.SendAckWinSize(DefaultAckWindowSize); err != nil {
		return err
	}

	return nil
}

func serveReleaseStream(peer *Peer) error {
	var transactionID uint
	var null int
	err := amf.DecodeWithReader(peer.Reader, &transactionID, &null)
	if err != nil {
		return err
	}

	var name string
	err = amf.DecodeWithReader(peer.Reader, &name)
	if err != nil {
		return err
	}

	log.Println(name)

	return nil
}

func serveCreateStream(peer *Peer) error {
	var transactionID uint
	err := amf.DecodeWithReader(peer.Reader, &transactionID)
	if err != nil {
		return err
	}

	return nil
}

func serveCloseStream(peer *Peer) error {
	var stream float64
	err := amf.DecodeWithReader(peer.Reader, &stream)
	if err != nil {
		return err
	}

	log.Println(stream)

	return nil
}

func serveDeleteStream(peer *Peer) error {
	var transactionID uint
	var null int
	err := amf.DecodeWithReader(peer.Reader, &transactionID, &null)
	if err != nil {
		return err
	}

	var stream float64
	err = amf.DecodeWithReader(peer.Reader, &stream)
	if err != nil {
		return err
	}

	log.Println(stream)

	return nil
}

func serveFCPublish(peer *Peer) error {
	var transactionID uint
	var null int
	err := amf.DecodeWithReader(peer.Reader, &transactionID, &null)
	if err != nil {
		return err
	}

	var name string
	err = amf.DecodeWithReader(peer.Reader, &name)
	if err != nil {
		return err
	}

	log.Println(name)

	return nil
}

func servePublish(peer *Peer) error {
	var transactionID uint
	var null int
	err := amf.DecodeWithReader(peer.Reader, &transactionID, &null)
	if err != nil {
		return err
	}

	var name, typo string
	err = amf.DecodeWithReader(peer.Reader, &name, &typo)
	if err != nil {
		return err
	}

	log.Println(name, typo)

	return nil
}

func servePlay(peer *Peer) error {
	var transactionID uint
	var null int
	err := amf.DecodeWithReader(peer.Reader, &transactionID, &null)
	if err != nil {
		return err
	}

	var name string
	err = amf.DecodeWithReader(peer.Reader, &name)
	if err != nil {
		return err
	}

	log.Println(name)

	var start, duration float64
	var reset bool
	err = amf.DecodeWithReader(peer.Reader, &start, &duration, &reset)
	if err != nil {
		return err
	}

	return nil
}

func servePlay2(peer *Peer) error {
	type Object struct {
		Start      float64
		StreamName string
	}

	var transactionID uint
	var null int
	err := amf.DecodeWithReader(peer.Reader, &transactionID, &null)
	if err != nil {
		return err
	}

	var obj Object
	err = amf.DecodeWithReader(peer.Reader, &obj)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(obj)

	return nil
}

func serveSeek(peer *Peer) error {
	var transactionID uint
	var null int
	err := amf.DecodeWithReader(peer.Reader, &transactionID, &null)
	if err != nil {
		return err
	}

	var offset float64
	err = amf.DecodeWithReader(peer.Reader, &offset)
	if err != nil {
		return err
	}

	log.Println(offset)

	return nil
}

func servePause(peer *Peer) error {
	var transactionID uint
	var null int
	err := amf.DecodeWithReader(peer.Reader, &transactionID, &null)
	if err != nil {
		return err
	}

	var pause bool
	var position float64
	err = amf.DecodeWithReader(peer.Reader, &pause, &position)
	if err != nil {
		return err
	}

	log.Println(pause, position)

	return nil
}
