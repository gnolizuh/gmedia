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
var headerPool = sync.Pool{
	New: func() interface{} {
		hd := make([]byte, MaxHeaderSize)
		return &hd
	},
}

// RTMP message declare.
type Message struct {
	hdr    *Header
	chunks []*[]byte
}

func newMessage(hdr *Header) *Message {
	msg := Message {
		hdr: hdr,
		chunks: make([]*[]byte, 4),
	}

	return &msg
}

func (m *Message) append(ch *[]byte) {
	m.chunks = append(m.chunks, ch)
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
}

// Create new connection from conn.
func newConn(conn net.Conn) *Conn {
	c := &Conn{
		conn:          conn,
		chunkSize:     DefaultChunkSize,
		state:         StateServerRecvChallenge,
	}

	c.epoch = uint32(time.Now().UnixNano() / 1000)

	c.bufReader = bufio.NewReader(conn)
	c.bufWriter = bufio.NewWriter(conn)

	c.streams = make([]Stream, MaxStreamsNum)
	c.chunkPool = &sync.Pool{
		New: func() interface{} {
			ck := make([]byte, c.chunkSize)
			return &ck
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
		err := c.readMessage()
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
				ck := make([]byte, chunkSize)
				return &ck
			},
		}
	}
}

func (c *Conn) getChunkSize() uint32 {
	return c.chunkSize
}

func (c *Conn) readBasicHeader(b []byte, hdr *Header) (uint32, error) {
	off := uint32(0)
	_, err := io.ReadFull(c.bufReader, b[off:off+1])
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
	hdr.fmt = (uint8(b[off] >> 6) & 0x03)
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
		_, err := io.ReadFull(c.bufReader, b[off:off+1])
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
		_, err := io.ReadFull(c.bufReader, b[off:off+2])
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
		_, err := io.ReadFull(c.bufReader, b[off:off+3])
		if err != nil {
			return 0, err
		}
		hdr.timestamp = uint32(b[off+2]) | uint32(b[off+1])<<8 | uint32(b[off])<<16
		off += 3

		if hdr.fmt <= 1 {
			_, err := io.ReadFull(c.bufReader, b[off:off+4])
			if err != nil {
				return 0, err
			}
			hdr.mlen = uint32(b[off+2]) | uint32(b[off+1])<<8 | uint32(b[off])<<16
			off += 3
			hdr.typo = uint8(b[off])
			off += 1

			if hdr.fmt == 0 {
				_, err := io.ReadFull(c.bufReader, b[off:off+4])
				if err != nil {
					return 0, err
				}
				hdr.msid = binary.BigEndian.Uint32(b[off:off+4])
				off += 4
			}
		}
	}

	if hdr.timestamp == 0x00ffffff {
		_, err := io.ReadFull(c.bufReader, b[off:off+4])
		if err != nil {
			return 0, err
		}
		hdr.timestamp = binary.BigEndian.Uint32(b[off:off+4])
		off += 4
	}

	return off, nil
}

func min(n, m uint32) uint32 {
	if n < m { return n } else { return m }
}

func (c *Conn) readMessage() error {
	hdr := Header{}
	b := headerPool.Get().(*[]byte)
	p := *b

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
	ch := c.chunkPool.Get().(*[]byte)

	// read message body.
	_, err = io.ReadFull(c.bufReader, (*ch)[:n])
	if err != nil {
		return err
	}

	if stm.msg == nil {
		stm.msg = newMessage(&stm.hdr)
	}

	stm.msg.append(ch)
	stm.len += n

	if stm.hdr.mlen == stm.len {
		// return handle(stm.msg)
	}

	// read continue.
	return nil
}
