package rtmp

import (
	"io"
	"sync"
	"encoding/binary"
	"net"
	"bufio"
	"errors"
	"fmt"
	"log"
	"time"
)

const (
	// Basic Header (1 to 3 bytes)
	MaxBasicHeaderSize = 3

	// Message Header (0, 3, 7, or 11 bytes)
	MaxMessageHeaderSize = 11

	// Extended Timestamp (0 or 4 bytes)
	MaxExtendHeaderSize = 4

	MaxHeaderSize = MaxBasicHeaderSize + MaxMessageHeaderSize + MaxExtendHeaderSize

	DefaultChunkSize = 128

	MaxStreamsNum = 32
)

type MessageHandler func (*Message) error

// Header declare.
type Header struct {
	fmt       uint8
	csid      uint32
	timestamp uint32
	mlen      uint32
	typo      uint8   // typo means type, u know why.
	msid      uint32
}

// to prevent GC.
var headerSharedPool = sync.Pool{
	New: func() interface{} {
		hd := make([]byte, MaxHeaderSize)
		return &hd
	},
}

// RTMP stream declare.
type Stream struct {
	hdr Header
	msg *Message
	len uint32
}

// The Conn type represents a RTMP connection.
type Conn struct {
	conn       net.Conn

	// Connection incoming time(microsecond) and incoming time from remote peer.
	epoch         uint32
	incomingEpoch uint32

	// State and digest be used in RTMP handshake.
	state      int
	digest     []byte

	// Read and write buffer.
	bufReader  *bufio.Reader
	bufWriter  *bufio.Writer

	// stream message
	streams    []Stream

	// chunk message
	chunkSize  uint32
	chunkPool  *sync.Pool

	// ack window size
	ackWinSize uint32
	inBytes    uint32
	inLastAck  uint32

	// message callback function.
	msgReader  MessageReader

	handlers   [MessageMax]MessageHandler
}

// Create new connection from conn.
func newConn(conn net.Conn) *Conn {
	c := &Conn{
		conn:          conn,
		chunkSize:     DefaultChunkSize,
	}

	c.epoch = uint32(time.Now().UnixNano() / 1000)

	c.bufReader = bufio.NewReader(conn)
	c.bufWriter = bufio.NewWriter(conn)

	c.streams = make([]Stream, MaxStreamsNum)
	c.chunkPool = &sync.Pool{
		New: func() interface{} {
			return &ChunkType{
				buf: make([]byte, c.chunkSize),
				off: 0,
			}
		},
	}

	// init callback handler.
	initHandlers(c)

	return c
}

func initHandlers(c *Conn) {
	c.handlers[MessageSetChunkSize] = c.onSetChunkSize
	c.handlers[MessageAbort] = c.onAbort
	c.handlers[MessageAck] = c.onAck
	c.handlers[MessageUserControl] = c.onUserControl
	c.handlers[MessageWindowAckSize] = c.onWinAckSize
	c.handlers[MessageSetPeerBandwidth] = c.onSetPeerBandwidth
	c.handlers[MessageEdge] = c.onEdge
	c.handlers[MessageAudio] = c.onAudio
	c.handlers[MessageVideo] = c.onVideo

	c.handlers[MessageAmf3Meta] = c.onAmf3Meta
	c.handlers[MessageAmf3Shared] = c.onAmf3Shared
	c.handlers[MessageAmf3Cmd] = c.onAmf3Cmd
	c.handlers[MessageAmf0Meta] = c.onAmf0Meta
	c.handlers[MessageAmf0Shared] = c.onAmf0Shared
	c.handlers[MessageAmf0Cmd] = c.onAmf0Cmd

	c.handlers[MessageAggregate] = c.onAggregate
}

// Serve a new connection.
func (c *Conn) serve() {
	err := c.handshake()
	if err != nil {
		return
	}

	for {
		err := c.pumpMessage()
		if err != nil {
			log.Panic(err)
			return
		}
	}
}

func (c *Conn) SetChunkSize(chunkSize uint32) {
	if c.chunkSize != chunkSize {
		c.chunkSize = chunkSize
		c.chunkPool = &sync.Pool{
			New: func() interface{} {
				return &ChunkType{
					buf: make([]byte, c.chunkSize),
					off: 0,
				}
			},
		}
	}
}

func (c *Conn) getChunkSize() uint32 {
	return c.chunkSize
}

func (c *Conn) readFull(buf []byte) error {
	n, err := io.ReadFull(c.bufReader, buf)
	if err != nil {
		return err
	}

	c.inBytes += uint32(n)

	if c.inBytes >= 0xf0000000 {
		log.Printf("resetting byte counter")
		c.inBytes = 0
		c.inLastAck = 0
	}

	if c.ackWinSize > 0 && c.inBytes - c.inLastAck >= c.ackWinSize {
		c.inLastAck = c.inBytes

		// TODO: send ack.
	}
}

func (c *Conn) readBasicHeader(b []byte, hdr *Header) (uint32, error) {
	off := uint32(0)
	err := c.readFull(b[off:off+1])
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
	hdr.fmt = uint8(b[off] >> 6) & 0x03
	hdr.csid = uint32(uint8(b[off]) & 0x3f)

	off += 1
	if hdr.csid == 0 {
		/*
		 * ID is computed as (the second byte + 64).
		 *
		 *  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 * |fmt|     0     |  cs id - 64   |
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 */
		hdr.csid = 64
		err := c.readFull(b[off:off+1])
		if err != nil {
			return 0, err
		}
		hdr.csid += uint32(b[off])
		off += 1
	} else if hdr.csid == 1 {
		/*
		 * ID is computed as ((the third byte)*256 +
		 * (the second byte) + 64).
		 *
		 *  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 * |fmt|     1     |           cs id - 64          |
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 */
		hdr.csid = 64
		err := c.readFull(b[off:off+2])
		if err != nil {
			return 0, err
		}
		hdr.csid += uint32(b[off])
		hdr.csid += 256 * uint32(b[off+1])
		off += 2
	}

	return off, nil
}

func (c *Conn) readChunkMessageHeader(b []byte, hdr *Header) (uint32, error) {
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
	if hdr.fmt <= 2 {
		err := c.readFull(b[off:off+3])
		if err != nil {
			return 0, err
		}
		hdr.timestamp = uint32(b[off+2]) | uint32(b[off+1])<<8 | uint32(b[off])<<16
		off += 3

		if hdr.fmt <= 1 {
			err := c.readFull(b[off:off+4])
			if err != nil {
				return 0, err
			}
			hdr.mlen = uint32(b[off+2]) | uint32(b[off+1])<<8 | uint32(b[off])<<16
			off += 3
			hdr.typo = uint8(b[off])
			off += 1

			if hdr.fmt == 0 {
				err := c.readFull(b[off:off+4])
				if err != nil {
					return 0, err
				}
				hdr.msid = binary.BigEndian.Uint32(b[off:off+4])
				off += 4
			}
		}
	}

	if hdr.timestamp == 0x00ffffff {
		err := c.readFull(b[off:off+4])
		if err != nil {
			return 0, err
		}
		hdr.timestamp = binary.BigEndian.Uint32(b[off:off+4])
		off += 4
	}

	return off, nil
}

func (c *Conn) pumpMessage() error {
	hdr := Header{}

	// alloc shared buffer.
	b := headerSharedPool.Get().(*[]byte)
	p := *b

	// recycle shared buffer.
	defer headerSharedPool.Put(b)

	// read basic header.
	n, err := c.readBasicHeader(p, &hdr)
	if err != nil {
		return err
	}
	p = p[n:]

	if hdr.csid > MaxStreamsNum {
		return errors.New(fmt.Sprintf("RTMP in chunk stream too big: %d >= %d", hdr.csid, MaxStreamsNum))
	}

	// read chunk message header.
	_, err = c.readChunkMessageHeader(p, &hdr)
	if err != nil {
		return err
	}

	// indicate timestamp whether is absolute or relate.
	stm := c.streams[hdr.csid]
	if hdr.fmt > 0 {
		stm.hdr.timestamp += hdr.timestamp
	} else {
		stm.hdr.timestamp = hdr.timestamp
	}

	// copy temp header to chunk stream header.
	stm.hdr.fmt = hdr.fmt
	stm.hdr.csid = hdr.csid
	if hdr.fmt <= 1 {
		stm.hdr.mlen = hdr.mlen
		stm.hdr.typo = hdr.typo
		if hdr.fmt == 0 {
			stm.hdr.msid = hdr.msid
		}
	}

	n = min(stm.hdr.mlen - stm.len, c.getChunkSize())
	ch := c.chunkPool.Get().(*ChunkType)
	// TODO: ch need to free

	// read message body.
	err = c.readFull((*ch).buf[:n])
	if err != nil {
		return err
	}

	if stm.msg == nil {
		stm.msg = newMessage(&stm.hdr)
	}

	stm.msg.appendChunk(ch)
	stm.len += n

	if stm.hdr.mlen == stm.len {
		if h := c.handlers[hdr.typo]; h != nil {
			return h(stm.msg)
		}
	}

	return nil
}

func readUint32(reader *bufio.Reader) (uint32, error) {
	buf := make([]byte, 4)
	_, err := reader.Read(buf)
	if err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint32(buf), nil
}

func readUint16(reader *bufio.Reader) (uint16, error) {
	buf := make([]byte, 2)
	_, err := reader.Read(buf)
	if err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint16(buf), nil
}

func readUint8(reader *bufio.Reader) (uint8, error) {
	buf, err := reader.ReadByte()
	if err != nil {
		return 0, err
	}

	return uint8(buf), nil
}

func (c *Conn) onSetChunkSize(msg *Message) error {
	cs, err := readUint32(msg.reader)
	if err != nil {
		return err
	}

	log.Printf("set chunk size, cs: %d", cs)

	c.SetChunkSize(cs)

	return nil
}

func (c *Conn) onAbort(msg *Message) error {
	csid, err := readUint32(msg.reader)
	if err != nil {
		return err
	}

	log.Printf("abort, csid: %d", csid)

	return nil
}

func (c *Conn) onAck(msg *Message) error {
	seq, err := readUint32(msg.reader)
	if err != nil {
		return err
	}

	log.Printf("ack, seq: %d", seq)

	return nil
}

func (c *Conn) onUserControl(msg *Message) error {
	if event, err := readUint16(msg.reader); err != nil {
		return err
	} else {
		return c.msgReader.OnUserControl(event, msg.reader)
	}
}

func (c *Conn) onWinAckSize(msg *Message) error {
	win, err := readUint32(msg.reader)
	if err != nil {
		return err
	}

	log.Printf("ack window size, win: %d", win)

	c.ackWinSize = win

	return nil
}

func (c *Conn) onSetPeerBandwidth(msg *Message) error {
	bandwidth, err := readUint32(msg.reader)
	if err != nil {
		return err
	}

	limit, err := readUint8(msg.reader)
	if err != nil {
		return err
	}

	log.Printf("set peer bandwidth, bandwidth: %d, limit: %d", bandwidth, limit)

	return nil
}

func (c *Conn) onEdge(msg *Message) error {
	return nil
}

func (c *Conn) onAudio(msg *Message) error {
	return nil
}

func (c *Conn) onVideo(msg *Message) error {
	return nil
}

func (c *Conn) onAmf3Meta(msg *Message) error {
	return nil
}

func (c *Conn) onAmf3Shared(msg *Message) error {
	return nil
}

func (c *Conn) onAmf3Cmd(msg *Message) error {
	return nil
}

func (c *Conn) onAmf0Meta(msg *Message) error {
	return nil
}

func (c *Conn) onAmf0Shared(msg *Message) error {
	return nil
}

func (c *Conn) onAmf0Cmd(msg *Message) error {
	return nil
}

func (c *Conn) onAggregate(msg *Message) error {
	return nil
}
