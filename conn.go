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

	DefaultReadChunkSize = 128
	DefaultSendChunkSize = 4096

	MaxStreamsNum = 32
)

type MessageHandler func (*Message) error

type UserMessageHandler func (*Message) error

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

var sharedBufferPool = sync.Pool{
	New: func() interface{} {
		hd := make([]byte, DefaultSendChunkSize)
		return &hd
	},
}

const (
	AudioChunkStream = iota
	VideoChunkStream
	MaxChunkStream
)

type ChunkStream struct {
	active     bool
	timestamp  uint32
	csid       uint32
	dropped    uint32
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

	// chunk stream
	cs         [MaxChunkStream]ChunkStream

	// chunk message
	chunkSize  uint32
	chunkPool  *sync.Pool

	// ack window size
	ackWinSize uint32
	inBytes    uint32
	inLastAck  uint32

	// message callback function.
	handler    Handler
}

// Create new connection from conn.
func newConn(conn net.Conn) *Conn {
	c := &Conn{
		conn:      conn,
		chunkSize: DefaultReadChunkSize,
	}

	c.epoch = uint32(time.Now().UnixNano() / 1000)

	c.bufReader = bufio.NewReader(conn)
	c.bufWriter = bufio.NewWriter(conn)

	c.streams = make([]Stream, MaxStreamsNum)
	c.chunkPool = &sync.Pool{
		New: func() interface{} {
			return newChunk(c.chunkSize)
		},
	}

	return c
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
			log.Println(err)
			return
		}
	}
}

func (c *Conn) SetChunkSize(chunkSize uint32) {
	if c.chunkSize != chunkSize {
		c.chunkSize = chunkSize
		c.chunkPool = &sync.Pool{
			New: func() interface{} {
				return newChunk(c.chunkSize)
			},
		}
	}
}

func (c *Conn) getReadChunkSize() uint32 {
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
		// c.SendAck(c.inBytes)
	}

	return nil
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

	n = min(stm.hdr.mlen - stm.len, c.getReadChunkSize())
	ck := c.chunkPool.Get().(*Chunk)
	// TODO: ck need to free

	// read message body.
	err = c.readFull(ck.Bytes(n))
	if err != nil {
		return err
	}

	if stm.msg == nil {
		stm.msg = newMessage(&stm.hdr)
	}

	stm.msg.appendChunk(ck)
	stm.len += n

	if stm.hdr.mlen == stm.len {
		if hdr.typo >= uint8(MessageMax) {
			return errors.New(fmt.Sprintf("unexpected RTMP message type: %d", hdr.typo))
		}

		return c.handler.Handle(&stm)
	}

	return nil
}

func (c *Conn) SendAck(seq uint32) error {
	return nil
}
