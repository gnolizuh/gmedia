package rtmp

import (
	"io"
	"sync"
	"encoding/binary"
	"net"
	"bufio"
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

type Header struct {
	csid      uint32
	timestamp uint32
	mlen      uint32
	typo      uint8   // typo means type, u know why.
	msid      uint32
}

type Stream struct {
	hdr Header
	// TODO: define and use chunk array.
}

// to prevent GC.
var headerPool = sync.Pool{
	New: func() interface{} {
		hd := make([]byte, MaxHeaderSize)
		return &hd
	},
}

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

	// stream message
	streams    []*Stream

	// chunk message
	chunkSize  uint32
	chunkPool  *sync.Pool
}

// Serve a new connection.
func (c *conn) serve() {
	err := c.handshake()
	if err != nil {
		return
	}

	/* for {
		go c.Loop()
	} */
}

func (c *conn) setChunkSize(chunkSize uint32) {
	if c.chunkSize != chunkSize {
		c.chunkPool = &sync.Pool{
			New: func() interface{} {
				ck := make([]byte, chunkSize)
				return &ck
			},
		}
	}
}

func (c *conn) readMessage() (error) {
	hd := Header{}
	off := 0
	b := headerPool.Get().(*[]byte)

	_, err := io.ReadFull(c.bufr, (*b)[off:off+1])
	if err != nil {
		return err
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
	fmt := uint8(((*b)[off] >> 6) & 0x03)
	hd.csid = uint32((*b)[off] & 0x3f)

	off += 1
	if hd.csid == 0 {
		/*
		 * ID is computed as (the second byte + 64).
		 *
		 *  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 * |fmt|     0     |  cs id - 64   |
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 */
		hd.csid = 64
		_, err := io.ReadFull(c.bufr, (*b)[off:off+1])
		if err != nil {
			return err
		}
		hd.csid += uint32((*b)[off])
		off += 1
	} else if hd.csid == 1 {
		/*
		 * ID is computed as ((the third byte)*256 +
		 * (the second byte) + 64).
		 *
		 *  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 * |fmt|     1     |           cs id - 64          |
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 */
		hd.csid = 64
		_, err := io.ReadFull(c.bufr, (*b)[off:off+2])
		if err != nil {
			return err
		}
		hd.csid += uint32((*b)[off])
		hd.csid += 256 * uint32((*b)[off+1])
		off += 2
	}

	if fmt <= 2 {
		_, err := io.ReadFull(c.bufr, (*b)[off:off+4])
		if err != nil {
			return err
		}

		hd.timestamp = binary.BigEndian.Uint32((*b)[off:off+4])
		off += 4
		if fmt <= 1 {
			_, err := io.ReadFull(c.bufr, (*b)[off:off+4])
			if err != nil {
				return err
			}

			hd.mlen = binary.BigEndian.Uint32((*b)[off:off+4])
			off += 4
			if fmt == 0 {
				_, err := io.ReadFull(c.bufr, (*b)[off:off+4])
				if err != nil {
					return err
				}

				hd.msid = binary.BigEndian.Uint32((*b)[off:off+4])
				off += 4
			}
		}
	}

	return nil
}
