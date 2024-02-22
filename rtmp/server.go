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
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/gnolizuh/gamf"
	"io"
	"log"
	"math"
	"net"
	"sync"
	"sync/atomic"
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
	ServeRTMP(*Message)
}

type Server struct {
	// Addr optionally specifies the TCP address for the server to listen on,
	// in the form "host:port". If empty, ":rtmp" (port 1935) is used.
	Addr string

	Handler Handler // handler to invoke, http.DefaultServeMux if nil

	// ConnState specifies an optional callback function that is
	// called when a client connection changes state. See the
	// ConnState type and associated constants for details.
	ConnState func(net.Conn, ConnState)
}

func ListenAndServe(addr string, handler Handler) error {
	server := &Server{
		Addr:    addr,
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
	defer func(l net.Listener) {
		_ = l.Close()
	}(l)

	for {
		rw, e := l.Accept()
		if e != nil {
			return e
		}

		c := srv.newConn(rw)

		// init connection state with StateServerRecvChallenge which means peer is server side.
		c.setState(c.rwc, StateServerNew)
		go c.serve()
	}
}

// serverHandler delegates to either the server's Handler or
// DefaultServeMux.
type serverHandler struct {
	srv *Server
}

func (sh *serverHandler) ServeRTMP(m *Message) {
	handler := sh.srv.Handler
	if handler == nil {
		handler = DefaultServeMux
	}
	//handler.ServeMessage(m)
}

var serverMessageHandler = [MessageTypeMax]MessageHandler{
	nil,
	serveSetChunkSize, serveAbort, serveAck, serveUserControl,
	serveWinAckSize, serveSetPeerBandwidth, serveEdge,
	serveAudio, serveVideo,
	nil, nil, nil, nil, nil,
	serveAMF3Meta, serveAMF3Shared, serveAMF3Cmd,
	serveAMF0Meta, serveAMF0Shared, serveAMF0Cmd,
	nil,
	serveAggregate,
}

func serveSetChunkSize(p *Peer) error {
	cs, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("set chunk size, cs: %d", cs)

	p.conn.SetChunkSize(cs)

	return nil
}

func serveAbort(p *Peer) error {
	csid, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("abort, csid: %d", csid)

	return nil
}

func serveAck(p *Peer) error {
	seq, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("ack, seq: %d", seq)

	return nil
}

func serveUserControl(p *Peer) error {
	evt, err := p.Reader.ReadUint16()
	if err != nil {
		return err
	}

	umt := UserMessageType(evt)
	if umt >= UserMessageMax {
		return errors.New(fmt.Sprintf("user message type out of range: %d", umt))
	}

	return nil
}

func serveWinAckSize(p *Peer) error {
	win, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("ack window size, win: %d", win)

	p.conn.ackWinSize = win

	return nil
}

func serveSetPeerBandwidth(p *Peer) error {
	bandwidth, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	limit, err := p.Reader.ReadUint8()
	if err != nil {
		return err
	}

	log.Printf("set peer bandwidth, bandwidth: %d, limit: %d", bandwidth, limit)

	return nil
}

func serveEdge(p *Peer) error {
	return nil
}

func serveAudio(p *Peer) error {
	return nil
}

func serveVideo(p *Peer) error {
	return nil
}

func serveAMF3Meta(p *Peer) error {
	return nil
}

func serveAMF3Shared(p *Peer) error {
	return nil
}

func serveAMF3Cmd(p *Peer) error {
	return nil
}

func serveAMF0Meta(p *Peer) error {
	return nil
}

func serveAMF0Shared(p *Peer) error {
	return nil
}

func serveAMF0Cmd(p *Peer) error {
	var name string
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&name)
	if err != nil {
		log.Println(err)
		return err
	}
	return nil
}

func serveAggregate(p *Peer) error {
	return nil
}

var serverUserMessageHandlers = [UserMessageMax]UserMessageHandler{
	serveUserStreamBegin, serveUserStreamEOF, serveUserStreamDry,
	serveUserSetBufLen, serveUserIsRecorded, nil,
	serveUserPingRequest, serveUserPingResponse,
}

func serveUserStreamBegin(p *Peer) error {
	msid, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: stream_begin msid=%d", msid)

	return nil
}

func serveUserStreamEOF(p *Peer) error {
	msid, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: stream_eof msid=%d", msid)

	return nil
}

func serveUserStreamDry(p *Peer) error {
	msid, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: stream_dry msid=%d", msid)

	return nil
}

func serveUserSetBufLen(p *Peer) error {
	msid, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	buflen, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: set_buflen msid=%d buflen=%d", msid, buflen)

	return nil
}

func serveUserIsRecorded(p *Peer) error {
	msid, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: recorded msid=%d", msid)

	return nil
}

func serveUserPingRequest(p *Peer) error {
	timestamp, err := p.Reader.ReadUint32()
	if err != nil {
		return err
	}

	log.Printf("receive: ping request timestamp=%d", timestamp)

	// TODO: send ping response.

	return nil
}

func serveUserPingResponse(p *Peer) error {
	timestamp, err := p.Reader.ReadUint32()
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

func serveConnect(p *Peer) error {
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

	var transactionID uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID)
	if err != nil {
		return err
	}

	if transactionID != 1 {
		return errors.New(fmt.Sprintf("unexpected transaction ID: %d", transactionID))
	}

	var obj Object
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&obj)
	if err != nil {
		log.Println(err)
		return err
	}

	if err := p.conn.SendAckWinSize(DefaultAckWindowSize); err != nil {
		return err
	}
	if err := p.conn.SendSetPeerBandwidth(DefaultAckWindowSize, DefaultLimitDynamic); err != nil {
		return err
	}
	if err := p.conn.SendSetChunkSize(DefaultChunkSize); err != nil {
		return err
	}
	if err := p.conn.SendOnBWDone(); err != nil {
		return err
	}
	if err := p.conn.SendConnectResult(transactionID, obj.ObjectEncoding); err != nil {
		return err
	}

	return nil
}

func serveReleaseStream(p *Peer) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID, &null)
	if err != nil {
		log.Println(err)
		return err
	}

	var name string
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&name)
	if err != nil {
		log.Println(err)
		return err
	}

	if err := p.conn.SendReleaseStreamResult(transactionID); err != nil {
		return err
	}

	return nil
}

func serveCreateStream(p *Peer) error {
	var transactionID uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID)
	if err != nil {
		return err
	}

	if err := p.conn.SendCreateStreamResult(transactionID, DefaultMessageStreamID); err != nil {
		return err
	}

	return nil
}

func serveCloseStream(p *Peer) error {
	var stream float64
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&stream)
	if err != nil {
		return err
	}

	log.Println(stream)

	return nil
}

func serveDeleteStream(p *Peer) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var stream float64
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&stream)
	if err != nil {
		return err
	}

	log.Println(stream)

	return nil
}

func serveFCPublish(p *Peer) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var name string
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&name)
	if err != nil {
		return err
	}

	if err := p.conn.SendOnFCPublish(transactionID); err != nil {
		return err
	}
	if err := p.conn.SendFCPublishResult(transactionID); err != nil {
		return err
	}

	return nil
}

func servePublish(p *Peer) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var name, typo string
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&name, &typo)
	if err != nil {
		return err
	}

	log.Println(name, typo)

	return nil
}

func servePlay(p *Peer) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var name string
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&name)
	if err != nil {
		return err
	}

	log.Println(name)

	var start, duration float64
	var reset bool
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&start, &duration, &reset)
	if err != nil {
		return err
	}

	return nil
}

func servePlay2(p *Peer) error {
	type Object struct {
		Start      float64
		StreamName string
	}

	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var obj Object
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&obj)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(obj)

	return nil
}

func serveSeek(p *Peer) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var offset float64
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&offset)
	if err != nil {
		return err
	}

	log.Println(offset)

	return nil
}

func servePause(p *Peer) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(p.Reader).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var pause bool
	var position float64
	err = amf.NewDecoder().WithReader(p.Reader).Decode(&pause, &position)
	if err != nil {
		return err
	}

	return nil
}

// --------------------------------------------- conn --------------------------------------------- //

// The conn type represents a RTMP connection.
type conn struct {
	// server is the server on which the connection arrived.
	// Immutable; never nil.
	server *Server

	// rwc is the underlying network connection.
	rwc net.Conn

	// remoteAddr is rwc.RemoteAddr().String(). It is not populated synchronously
	// inside the Listener's Accept goroutine, as some implementations block.
	remoteAddr string

	// Connection incoming time(microsecond) and incoming time from remote peer.
	epoch         uint32
	incomingEpoch uint32

	// State and digest be used in RTMP handshake.
	state  atomic.Uint32
	digest []byte

	// Read and write buffer.
	bufReader *bufio.Reader
	bufWriter *bufio.Writer

	// stream message
	streams []Stream

	// chunk stream
	cs [MaxChunkStream]ChunkStream

	// chunk message
	chunkSize uint32
	chunkPool *sync.Pool

	// ack window size
	ackWinSize uint32
	inBytes    uint32
	inLastAck  uint32

	// peer wrapper
	peer Peer
}

// Create new connection from conn.
func (srv *Server) newConn(rwc net.Conn) *conn {
	c := &conn{
		server:    srv,
		rwc:       rwc,
		chunkSize: DefaultReadChunkSize,
	}

	c.epoch = uint32(time.Now().UnixNano() / 1000)

	c.bufReader = bufio.NewReader(rwc)
	c.bufWriter = bufio.NewWriter(rwc)

	c.streams = make([]Stream, MaxStreamsNum)
	c.chunkPool = &sync.Pool{
		New: func() any {
			return newChunk(c.chunkSize)
		},
	}

	c.peer = Peer{
		RemoteAddr: c.rwc.RemoteAddr().String(),
		conn:       c,
	}

	return c
}

func (c *conn) setState(nc net.Conn, state ConnState) {
	if state > StateServerDone || state < StateServerRecvChallenge {
		panic("internal error")
	}
	c.state.Store(uint32(state))
	if hook := c.server.ConnState; hook != nil {
		hook(nc, state)
	}
}

// serve a new connection.
func (c *conn) serve() {
	if ra := c.rwc.RemoteAddr(); ra != nil {
		c.remoteAddr = ra.String()
	}

	err := c.handshake()
	if err != nil {
		panic(err)
		return
	}

	for {
		m, err := c.readMessage()
		if err != nil {
			panic(err)
			return
		}

		sh := serverHandler{c.server}
		sh.ServeRTMP(m)
	}
}

func (c *conn) SetChunkSize(chunkSize uint32) {
	if c.chunkSize != chunkSize {
		c.chunkSize = chunkSize
		c.chunkPool = &sync.Pool{
			New: func() any {
				return newChunk(c.chunkSize)
			},
		}
	}
}

func (c *conn) getReadChunkSize() uint32 {
	return c.chunkSize
}

func (c *conn) readFull(buf []byte) error {
	n, err := io.ReadFull(c.bufReader, buf)
	if err != nil {
		return err
	}

	c.inBytes += uint32(n)

	if c.inBytes >= 0xf0000000 {
		c.inBytes = 0
		c.inLastAck = 0
	}

	if c.ackWinSize > 0 && c.inBytes-c.inLastAck >= c.ackWinSize {
		c.inLastAck = c.inBytes
		if err = c.SendAck(c.inBytes); err != nil {
			return err
		}
	}

	return nil
}

func (c *conn) readBasicHeader(b []byte, hdr *Header) (uint32, error) {
	off := uint32(0)
	err := c.readFull(b[off : off+1])
	if err != nil {
		return 0, err
	}

	/*
	 * Chunk stream IDs 2-63 can be encoded in
	 * the 1-byte version of this field.
	 *
	 *  0 1 2 3 4 5 6 7
	 * +-+-+-+-+-+-+-+-+
	 * |fmt|   cs id   |
	 * +-+-+-+-+-+-+-+-+
	 */
	hdr.Format = uint8(b[off]>>6) & 0x03
	hdr.ChunkStreamId = uint32(uint8(b[off]) & 0x3f)

	off += 1
	if hdr.ChunkStreamId == 0 {
		/*
		 * ID is computed as (the second byte + 64).
		 *
		 *  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 * |fmt|     0     |  cs id - 64   |
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 */
		hdr.ChunkStreamId = 64
		err := c.readFull(b[off : off+1])
		if err != nil {
			return 0, err
		}
		hdr.ChunkStreamId += uint32(b[off])
		off += 1
	} else if hdr.ChunkStreamId == 1 {
		/*
		 * ID is computed as ((the third byte)*256 +
		 * (the second byte) + 64).
		 *
		 *  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 * |fmt|     1     |           cs id - 64          |
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 */
		hdr.ChunkStreamId = 64
		err := c.readFull(b[off : off+2])
		if err != nil {
			return 0, err
		}
		hdr.ChunkStreamId += uint32(b[off])
		hdr.ChunkStreamId += 256 * uint32(b[off+1])
		off += 2
	}

	return off, nil
}

func (c *conn) readChunkMessageHeader(b []byte, hdr *Header) (uint32, error) {
	//  0                   1                   2                   3
	//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |                   timestamp                   |message length |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |     message length (cont)     |message type id| msg stream id |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |           message stream id (cont)            |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	off := uint32(0)
	if hdr.Format <= 2 {
		err := c.readFull(b[off : off+3])
		if err != nil {
			return 0, err
		}
		hdr.Timestamp = uint32(b[off+2]) | uint32(b[off+1])<<8 | uint32(b[off])<<16
		off += 3

		if hdr.Format <= 1 {
			err := c.readFull(b[off : off+4])
			if err != nil {
				return 0, err
			}
			hdr.StreamId = uint32(b[off+2]) | uint32(b[off+1])<<8 | uint32(b[off])<<16
			off += 3
			hdr.Type = MessageType(b[off])
			off += 1

			if hdr.Format == 0 {
				err := c.readFull(b[off : off+4])
				if err != nil {
					return 0, err
				}
				hdr.StreamId = binary.BigEndian.Uint32(b[off : off+4])
				off += 4
			}
		}
	}

	if hdr.Timestamp == 0x00ffffff {
		err := c.readFull(b[off : off+4])
		if err != nil {
			return 0, err
		}
		hdr.Timestamp = binary.BigEndian.Uint32(b[off : off+4])
		off += 4
	}

	return off, nil
}

func (c *conn) readMessage() (*Message, error) {
	hdr := Header{}

	// alloc shared buffer.
	b := headerPool.Get().(*[]byte)
	p := *b

	// recycle shared buffer.
	defer headerPool.Put(b)

	// read basic header.
	n, err := c.readBasicHeader(p, &hdr)
	if err != nil {
		return nil, err
	}
	p = p[n:]

	if hdr.ChunkStreamId > MaxStreamsNum {
		return nil, errors.New(fmt.Sprintf("RTMP in chunk stream too big: %d >= %d", hdr.ChunkStreamId, MaxStreamsNum))
	}

	// read chunk message header.
	_, err = c.readChunkMessageHeader(p, &hdr)
	if err != nil {
		return nil, err
	}

	// indicate timestamp whether is absolute or relate.
	stm := c.streams[hdr.ChunkStreamId]
	if hdr.Format > 0 {
		stm.hdr.Timestamp += hdr.Timestamp
	} else {
		stm.hdr.Timestamp = hdr.Timestamp
	}

	// copy temp header to chunk stream header.
	stm.hdr.Format = hdr.Format
	stm.hdr.ChunkStreamId = hdr.ChunkStreamId
	if hdr.Format <= 1 {
		stm.hdr.Length = hdr.Length
		stm.hdr.Type = hdr.Type
		if hdr.Format == 0 {
			stm.hdr.StreamId = hdr.StreamId
		}
	}

	n = uint32(math.Min(float64(stm.hdr.Length-stm.len), float64(c.getReadChunkSize())))
	ck := c.chunkPool.Get().(*Chunk)
	// TODO: ck need to free

	// read message body.
	err = c.readFull(ck.Bytes(n))
	if err != nil {
		return nil, err
	}

	if stm.msg == nil {
		stm.msg = newMessage(&stm.hdr)
	}

	stm.msg.appendChunk(ck)
	stm.len += n

	if stm.hdr.Length == stm.len {
		if hdr.Type >= MessageTypeMax {
			return nil, errors.New(fmt.Sprintf("unexpected RTMP message type: %d", hdr.Type))
		}
		return stm.msg, nil
	} else {
		return nil, errors.New(fmt.Sprintf("unexpected RTMP message len: %d", stm.len))
	}
}

func (c *conn) SendAck(seq uint32) error {
	msg := newMessage(newHeader(MessageTypeAck, 2))
	msg.alloc(4)

	_ = binary.Write(msg, binary.BigEndian, seq)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendAckWinSize(win uint32) error {
	msg := newMessage(newHeader(MessageTypeWindowAckSize, 2))
	msg.alloc(4)

	_ = binary.Write(msg, binary.BigEndian, win)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendSetPeerBandwidth(win uint32, limit uint8) error {
	msg := newMessage(newHeader(MessageTypeSetPeerBandwidth, 2))
	msg.alloc(5)

	_ = binary.Write(msg, binary.BigEndian, win)
	_ = msg.WriteByte(limit)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendSetChunkSize(cs uint32) error {
	msg := newMessage(newHeader(MessageTypeSetChunkSize, 2))

	msg.alloc(4)
	_ = binary.Write(msg, binary.BigEndian, cs)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendOnBWDone() error {
	msg := newMessage(newHeader(MessageTypeAMF0Cmd, 3))
	b, _ := amf.Marshal([]any{"onBWDone", 0, nil})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendConnectResult(trans uint32, encoding uint32) error {
	msg := newMessage(newHeader(MessageTypeAMF0Cmd, 3))

	type Object struct {
		FMSVer       string `amf:"fmsVer"`
		Capabilities uint32 `amf:"capabilities"`
	}

	type Info struct {
		Level          string `amf:"level"`
		Code           string `amf:"code"`
		Description    string `amf:"description"`
		ObjectEncoding uint32 `amf:"objectEncoding"`
	}

	obj := Object{
		FMSVer:       DefaultFMSVersion,
		Capabilities: DefaultCapabilities,
	}
	inf := Info{
		Level:          "status",
		Code:           "NetConnection.Connect.Success",
		Description:    "Connection succeeded.",
		ObjectEncoding: encoding,
	}
	b, _ := amf.Marshal([]any{"_result", trans, obj, inf})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendReleaseStreamResult(trans uint32) error {
	msg := newMessage(newHeader(MessageTypeAMF0Cmd, 3))
	var nullArray []uint32
	b, _ := amf.Marshal([]any{"_result", trans, nil, nullArray})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendOnFCPublish(trans uint32) error {
	msg := newMessage(newHeader(MessageTypeAMF0Cmd, 3))
	b, _ := amf.Marshal([]any{"onFCPublish", trans, nil})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendFCPublishResult(trans uint32) error {
	msg := newMessage(newHeader(MessageTypeAMF0Cmd, 3))
	var nullArray []uint32
	b, _ := amf.Marshal([]any{"_result", trans, nil, nullArray})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendCreateStreamResult(trans uint32, stream uint32) error {
	msg := newMessage(newHeader(MessageTypeAMF0Cmd, 3))
	b, _ := amf.Marshal([]any{"_result", trans, nil, stream})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}
