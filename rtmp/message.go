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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"sync"
)

type Reader interface {
	Read(b []byte) (int, error)
	ReadByte() (byte, error)
	ReadUInt32() (uint32, error)
	ReadUInt16() (uint16, error)
	ReadUInt8() (uint8, error)
}

// see https://rtmp.veriskope.com/docs/spec/#51message-format
//
// 01: SetChunkSize
// 02: Abort
// 03: Ack
// 04: User Control Message
// 05: Window Ack Size
// 06: Peer Bandwidth
// 08: Audio Message
// 09: Video Message
// 15: Data Message (AMF3)
// 16: Shared Object Message (AMF3)
// 17: Command Message (AMF3)
// 18: Data Message (AMF0)
// 19: Shared Object Message (AMF0)
// 20: Command Message (AMF0)
// 22: Aggregate Message

type MessageType uint

const (
	MessageTypeSetChunkSize MessageType = iota + 1 // 1
	MessageTypeAbort
	MessageTypeAck
	MessageTypeUserControl
	MessageTypeWindowAckSize
	MessageTypeSetPeerBandwidth
)

const (
	MessageTypeAudio = iota + MessageTypeSetPeerBandwidth + 2
	MessageTypeVideo
)

const (
	MessageTypeAMF3Meta = iota + MessageTypeVideo + 6 // 15
	MessageTypeAMF3Shared
	MessageTypeAMF3Cmd
	MessageTypeAMF0Meta
	MessageTypeAMF0Shared
	MessageTypeAMF0Cmd
)

const (
	MessageTypeAggregate = iota + MessageTypeAMF0Cmd + 2 // 22
	MessageTypeMax
)

const (
	MaxChunkSize = 10485760
)

func (mt MessageType) String() string {
	types := []string{
		"?",
		"chunk_size",
		"abort",
		"ack",
		"user",
		"ack_size",
		"bandwidth",
		"edge",
		"audio",
		"video",
		"?",
		"?",
		"?",
		"?",
		"?",
		"amf3_meta",
		"amf3_shared",
		"amf3_cmd",
		"amf_meta",
		"amf_shared",
		"amf_cmd",
		"?",
		"aggregate",
	}

	if mt < MessageType(len(types)) {
		return types[mt]
	} else {
		return "?"
	}
}

type UserMessageType uint

const (
	UserMessageTypeStreamBegin UserMessageType = iota // 0
	UserMessageTypeStreamEOF
	UserMessageTypeStreamDry
	UserMessageTypeStreamSetBufLen
	UserMessageTypeStreamIsRecorded
	UserMessageTypePingRequest = iota + 1
	UserMessageTypePingResponse
	UserMessageTypeMax
)

type ChunkReader struct {
	conn  *conn
	chunk *Chunk
}

func (cr ChunkReader) ReadN(n uint32) (int, error) {
	w, err := cr.conn.ReadFull(cr.chunk.bytes(OffsetWrite, n))
	if err != nil {
		return w, err
	}
	cr.chunk.offw += uint32(w)
	return w, nil
}

type OffsetType int8

const (
	OffsetRead OffsetType = iota
	OffsetWrite
	OffsetSend
)

// Chunk RTMP message chunk declare.
type Chunk struct {
	msg *Message

	// specifies RTMP ChunkSize.
	s uint32

	buf []byte

	// offset for reading, writing and sending.
	offr, offw, offs, head uint32
}

var chunkPool sync.Pool

func newChunk(chunkSize uint32) *Chunk {
	if v := chunkPool.Get(); v != nil {
		chunk := v.(*Chunk)
		chunk.msg = nil
		chunk.s = chunkSize
		chunk.buf = make([]byte, chunkSize+MaxHeaderBufferSize)
		chunk.offr = MaxHeaderBufferSize
		chunk.offw = MaxHeaderBufferSize
		chunk.head = MaxHeaderBufferSize
		chunk.offs = MaxHeaderBufferSize
		return chunk
	}
	return &Chunk{
		s:    chunkSize,
		buf:  make([]byte, chunkSize+MaxHeaderBufferSize),
		offr: MaxHeaderBufferSize,
		offw: MaxHeaderBufferSize,
		head: MaxHeaderBufferSize,
		offs: MaxHeaderBufferSize,
	}
}

func (chunk *Chunk) reset(n uint32) {
	if n > chunk.head {
		panic(fmt.Sprintf("chunk reset overflow: %d > %d", n, chunk.head))
	}
	chunk.head -= n
	chunk.offs = chunk.head
}

func (chunk *Chunk) bytes(typ OffsetType, n uint32) []byte {
	var off uint32
	switch typ {
	case OffsetRead:
		off = chunk.offr
	case OffsetWrite:
		off = chunk.offw
	case OffsetSend:
		off = chunk.offs
	}
	return chunk.buf[off : off+n]
}

func (chunk *Chunk) size() uint32 {
	return chunk.offw - chunk.head
}

func (chunk *Chunk) Send(w io.Writer) error {
	for chunk.offs < chunk.offw {
		n, err := w.Write(chunk.buf[chunk.offs:chunk.offw])
		if err != nil {
			return err
		}
		chunk.offs += uint32(n)
	}
	return nil
}

func (chunk *Chunk) Read(p []byte) (int, error) {
	n := float64(len(p))
	m := float64(len(chunk.buf[chunk.offr:chunk.offw]))
	if r := uint32(math.Min(n, m)); r > 0 {
		copy(p, chunk.buf[chunk.offr:chunk.offr+r])
		chunk.offr += r
		return int(r), nil
	}
	return 0, io.EOF
}

func (chunk *Chunk) Write(p []byte) (int, error) {
	n := float64(len(p))
	m := float64(len(chunk.buf[chunk.offw:]))
	if w := uint32(math.Min(n, m)); w > 0 {
		copy(chunk.buf[chunk.offw:], p[:w])
		chunk.offw += w
		return int(w), nil
	}
	return 0, io.EOF
}

func (chunk *Chunk) ReadByte() (byte, error) {
	bs := make([]byte, 1)
	_, err := chunk.Read(bs)
	if err != nil {
		return 0, err
	}
	return bs[0], nil
}

func (chunk *Chunk) WriteByte(b byte) error {
	bs := make([]byte, 1)
	bs[0] = b
	_, err := chunk.Write(bs)
	if err != nil {
		return err
	}
	return nil
}

type Chunks struct {
	chunks []*Chunk

	s uint32

	// index of array for reading, writing and sending
	offr, offw, offs uint32
}

func newChunks() *Chunks {
	return &Chunks{
		chunks: []*Chunk{},
	}
}

func (chunks *Chunks) len() uint32 {
	return uint32(len(chunks.chunks))
}

func (chunks *Chunks) size() uint32 {
	return chunks.s
}

func (chunks *Chunks) append(chunk *Chunk) {
	chunks.chunks = append(chunks.chunks, chunk)
	chunks.s += chunk.size()
}

// Read reads data into p. It returns the number of bytes
// read into p. The bytes are taken from at most one Read
// on the underlying Reader, hence n may be less than
// len(p). At EOF, the count will be zero and err will be
// io.EOF.
func (chunks *Chunks) Read(p []byte) (int, error) {
	l := len(p)
	r := 0
	for chunks.offr < chunks.len() {
		ch := chunks.chunks[chunks.offr]
		n, err := ch.Read(p[r:])
		if err != nil {
			return r + n, err
		}
		r += n
		l -= n
		if l == 0 {
			return r, nil
		}
		chunks.offr++
	}
	return r, io.EOF
}

func (chunks *Chunks) Write(p []byte) (int, error) {
	l := len(p)
	w := 0
	for chunks.offw < chunks.len() {
		ch := chunks.chunks[chunks.offw]
		n, err := ch.Write(p[w:])
		if err != nil {
			return w + n, err
		}
		w += n
		l -= n
		if l == 0 {
			return w, nil
		}
		chunks.offw++
	}
	return w, io.EOF
}

func (chunks *Chunks) ReadByte() (byte, error) {
	if chunks.offr < chunks.len() {
		ch := chunks.chunks[chunks.offr]
		b, err := ch.ReadByte()
		if err != nil {
			return 0, err
		}
		if len(ch.buf[ch.offr:]) == 0 {
			chunks.offr++
		}
		return b, nil
	}
	return 0, io.EOF
}

func (chunks *Chunks) WriteByte(c byte) error {
	if chunks.offw < chunks.len() {
		ch := chunks.chunks[chunks.offw]
		err := ch.WriteByte(c)
		if err != nil {
			return err
		}
		if len(ch.buf[ch.offw:]) == 0 {
			chunks.offw++
		}
		return nil
	}
	return io.EOF
}

var headerBufferPool = sync.Pool{
	New: func() any {
		hdr := make([]byte, MaxHeaderBufferSize)
		return hdr
	},
}

func newHeaderBuffer() []byte {
	if v := headerBufferPool.Get(); v != nil {
		hdr := v.([]byte)
		return hdr
	}
	return make([]byte, MaxHeaderBufferSize)
}

// Header declare.
type Header struct {
	// MessageTypeId are reserved for protocol control messages.
	MessageTypeId MessageType

	// MessageLength represents the size of the payload in bytes.
	MessageLength uint32

	// Timestamp contains a timestamp of the message.
	Timestamp uint32

	// MessageStreamId identifies the chunk stream of the message
	ChunkStreamId uint32

	// MessageStreamId identifies the stream of the message
	MessageStreamId uint32

	// Format identifies one of four format used by the chunk message header.
	Format uint8
}

var headerPool sync.Pool

func newHeader() *Header {
	if v := headerPool.Get(); v != nil {
		hdr := v.(*Header)
		return hdr
	}
	return &Header{}
}

func readHeader(c *conn, hdr *Header) error {
	buf := newHeaderBuffer()
	defer headerBufferPool.Put(buf)

	// basic header
	off := uint32(0)
	_, err := c.ReadFull(buf[off : off+1])
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
	hdr.Format = uint8(buf[off]>>6) & 0x03
	hdr.ChunkStreamId = uint32(uint8(buf[off]) & 0x3f)

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
		_, err := c.ReadFull(buf[off : off+1])
		if err != nil {
			return err
		}
		hdr.ChunkStreamId = 64
		hdr.ChunkStreamId += uint32(buf[off])
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
		_, err := c.ReadFull(buf[off : off+2])
		if err != nil {
			return err
		}
		hdr.ChunkStreamId = 64
		hdr.ChunkStreamId += uint32(buf[off])
		hdr.ChunkStreamId += 256 * uint32(buf[off+1])
		off += 2
	}

	if hdr.ChunkStreamId > MaxStreamsNum {
		return errors.New(fmt.Sprintf("RTMP in chunk stream too big: %d >= %d",
			hdr.ChunkStreamId, MaxStreamsNum))
	}

	//  0                   1                   2
	//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |               timestamp delta                 |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	if hdr.Format <= 2 {
		_, err := c.ReadFull(buf[off : off+3])
		if err != nil {
			return err
		}
		hdr.Timestamp = uint32(buf[off+2]) | uint32(buf[off+1])<<8 | uint32(buf[off])<<16
		off += 3
		//  0                   1                   2                   3
		//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		// |                timestamp delta                |message length |
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		// |     message length (cont)     |message type id|
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		if hdr.Format <= 1 {
			_, err := c.ReadFull(buf[off : off+4])
			if err != nil {
				return err
			}
			hdr.MessageLength = uint32(buf[off+2]) | uint32(buf[off+1])<<8 | uint32(buf[off])<<16
			off += 3
			hdr.MessageTypeId = MessageType(buf[off])
			off += 1
			//  0                   1                   2                   3
			//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
			// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			// |                   timestamp                   |message length |
			// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			// |     message length (cont)     |message type id| msg stream id |
			// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			// |           message stream id (cont)            |
			// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			if hdr.Format == 0 {
				_, err := c.ReadFull(buf[off : off+4])
				if err != nil {
					return err
				}
				hdr.MessageStreamId = binary.LittleEndian.Uint32(buf[off : off+4])
				off += 4
			}
		}
	}

	return nil
}

var messagePool sync.Pool

// Message RTMP message declare. The message header contains
// the following:
//
// Message MessageTypeId: One byte field to represent the message type.
// A range of type IDs (1-6) are reserved for protocol control
// messages.
//
// MessageLength: Three-byte field that represents the size of the
// payload in bytes. It is set in big-endian format.
//
// Timestamp: Four-byte field that contains a timestamp of
// the message. The 4 bytes are packed in the big-endian order.
//
// Message ChunkStream ID: Three-byte field that identifies the
// stream of the message. These bytes are set in big-endian format.
//
// 0                   1                   2                   3
// 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// | Message MessageTypeId  |                Payload length                 |
// |    (1 byte)   |                   (3 bytes)                   |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// |                           Timestamp                           |
// |                           (4 bytes)                           |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// |                 ChunkStream ID                     |
// |                 (3 bytes)                     |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
type Message struct {
	Header      Header
	ChunkStream *ChunkStream
	Chunks      *Chunks

	conn *conn

	// size we have
	hs uint32
}

func newMessage() *Message {
	if v := messagePool.Get(); v != nil {
		m := v.(*Message)
		m.hs = 0
		return m
	}
	return &Message{
		Chunks: newChunks(),
	}
}

func (m *Message) append(chunk *Chunk) {
	m.Chunks.append(chunk)
	m.hs += chunk.size()
}

func (m *Message) completed() bool {
	return m.Header.MessageLength == m.hs
}

func (m *Message) readChunk() error {
	err := readHeader(m.conn, &m.Header)
	if err != nil {
		return err
	}

	// indicate timestamp whether is absolute or relate.
	cs := &m.conn.chunkStreams[m.Header.ChunkStreamId]
	if m.ChunkStream == nil {
		m.ChunkStream = cs
	} else if m.ChunkStream != cs {
		panic("chunk-stream must be strictly continuous")
	}

	if cs.hdr == nil {
		cs.hdr = newHeader()
	}

	cs.hdr.Format = m.Header.Format
	cs.hdr.ChunkStreamId = m.Header.ChunkStreamId
	switch m.Header.Format {
	case 0:
		cs.hdr.Timestamp = m.Header.Timestamp
		cs.hdr.MessageLength = m.Header.MessageLength
		cs.hdr.MessageTypeId = m.Header.MessageTypeId
		cs.hdr.MessageStreamId = m.Header.MessageStreamId
	case 1:
		cs.hdr.Timestamp += m.Header.Timestamp
		cs.hdr.MessageLength = m.Header.MessageLength
		cs.hdr.MessageTypeId = m.Header.MessageTypeId
	case 2:
		cs.hdr.Timestamp += m.Header.Timestamp
	case 3:
		// see https://rtmp.veriskope.com/docs/spec/#53124-type-3
		if cs.hdr != nil && cs.hdr.Format == 0 {
			cs.hdr.Timestamp += cs.hdr.Timestamp
		}

		// read extend timestamp
		if cs.hdr.Timestamp == 0x00ffffff {
			buf := make([]byte, 4)
			_, err := m.conn.ReadFull(buf)
			if err != nil {
				return err
			}
			cs.hdr.Timestamp = binary.BigEndian.Uint32(buf)
		}
	default:
		panic("unknown format type")
	}

	// calculate bytes needed.
	need := uint32(math.Min(float64(cs.hdr.MessageLength-m.hs), float64(m.conn.chunkSize)))

	ck := newChunk(m.conn.chunkSize)
	reader := ChunkReader{conn: m.conn, chunk: ck}
	if _, err = reader.ReadN(need); err != nil {
		return err
	}
	m.append(ck)

	return nil
}

func (m *Message) Send(w io.Writer) error {
	chunks := m.Chunks
	for chunks.offs <= chunks.offw {
		err := chunks.chunks[chunks.offs].Send(w)
		if err != nil {
			return err
		}
		chunks.offs++
	}
	return nil
}

func (m *Message) Read(p []byte) (int, error) {
	return m.Chunks.Read(p)
}

func (m *Message) ReadByte() (byte, error) {
	return m.Chunks.ReadByte()
}

func (m *Message) ReadUInt32() (uint32, error) {
	var b uint32
	err := binary.Read(m, binary.BigEndian, &b)
	if err != nil {
		return 0, err
	}

	return b, nil
}

func (m *Message) ReadUInt16() (uint16, error) {
	var b uint16
	err := binary.Read(m, binary.BigEndian, &b)
	if err != nil {
		return 0, err
	}

	return b, nil
}

func (m *Message) ReadUInt8() (uint8, error) {
	b, err := m.ReadByte()
	if err != nil {
		return 0, err
	}

	return b, nil
}

func (m *Message) Write(p []byte) (int, error) {
	return m.Chunks.Write(p)
}

func (m *Message) WriteByte(c byte) error {
	return m.Chunks.WriteByte(c)
}

func (m *Message) alloc(n uint32) {
	for i := (n / DefaultChunkSize) + 1; i > 0; i-- {
		chunk := newChunk(DefaultChunkSize)
		m.append(chunk)
	}
}

var chunkHeaderSize = []uint8{12, 8, 4, 1}

func (m *Message) prepare(prev *Header) error {
	// message whether was prepared or not
	//if m.ready {
	//	return nil
	//}

	if m.Header.ChunkStreamId > MaxStreamsNum {
		return errors.New(fmt.Sprintf("RTMP out chunk stream too big: %d >= %d",
			m.Header.ChunkStreamId, MaxStreamsNum))
	}

	size := m.Chunks.size()
	timestamp := m.Header.Timestamp

	ft := uint8(0)
	if prev != nil && prev.ChunkStreamId > 0 && m.Header.MessageStreamId == prev.MessageStreamId {
		ft++
		if m.Header.MessageTypeId == prev.MessageTypeId && size > 0 && size == prev.MessageLength {
			ft++
			if m.Header.Timestamp == prev.Timestamp {
				ft++
			}
		}
		timestamp = m.Header.Timestamp - prev.Timestamp
	}

	if prev != nil {
		*prev = m.Header
		prev.MessageLength = size
	}

	hs := chunkHeaderSize[ft]

	log.Printf("RTMP prep %s (%d) fmt=%d csid=%d timestamp=%d mlen=%d msid=%d",
		m.Header.MessageTypeId, m.Header.MessageTypeId, ft,
		m.Header.ChunkStreamId, timestamp, size, m.Header.MessageStreamId)

	ext := uint32(0)
	if timestamp >= 0x00ffffff {
		ext = timestamp
		timestamp = 0x00ffffff
		hs += 4
	}

	if m.Header.ChunkStreamId >= 64 {
		hs++
		if m.Header.ChunkStreamId >= 320 {
			hs++
		}
	}

	fch := m.Chunks.chunks[0]
	fch.reset(uint32(hs))
	head := fch.head

	var ftsize uint32
	ftt := ft << 6
	if m.Header.ChunkStreamId >= 2 && m.Header.ChunkStreamId <= 63 {
		fch.buf[head] = ftt | (uint8(m.Header.ChunkStreamId) & 0x3f)
		ftsize = 1
	} else if m.Header.ChunkStreamId >= 64 && m.Header.ChunkStreamId < 320 {
		fch.buf[head] = ftt
		fch.buf[head+1] = uint8(m.Header.ChunkStreamId - 64)
		ftsize = 2
	} else {
		fch.buf[head] = ftt | 0x01
		fch.buf[head+1] = uint8(m.Header.ChunkStreamId - 64)
		fch.buf[head+2] = uint8((m.Header.ChunkStreamId - 64) >> 8)
		ftsize = 3
	}

	head += ftsize
	if ft <= 2 {
		fch.buf[head] = byte(timestamp >> 16)
		fch.buf[head+1] = byte(timestamp >> 8)
		fch.buf[head+2] = byte(timestamp)
		head += 3
		if ft <= 1 {
			fch.buf[head] = byte(size >> 16)
			fch.buf[head+1] = byte(size >> 8)
			fch.buf[head+2] = byte(size)
			fch.buf[head+3] = byte(m.Header.MessageTypeId)
			head += 4
			if ft == 0 {
				fch.buf[head] = byte(m.Header.MessageStreamId >> 24)
				fch.buf[head+1] = byte(m.Header.MessageStreamId >> 16)
				fch.buf[head+2] = byte(m.Header.MessageStreamId >> 8)
				fch.buf[head+3] = byte(m.Header.MessageStreamId)
				head += 4
			}
		}
	}

	// extend timestamp
	if ext > 0 {
		fch.buf[head] = byte(ext >> 24)
		fch.buf[head+1] = byte(ext >> 16)
		fch.buf[head+2] = byte(ext >> 8)
		fch.buf[head+3] = byte(ext)
	}

	// set following chunk's fmt to be 3
	for i := m.Chunks.offw + 1; i < m.Chunks.len(); i++ {
		ch := m.Chunks.chunks[i]
		ch.reset(ftsize)
		ch.buf[ch.head] = fch.buf[fch.head] | 0xc0
		copy(ch.buf[ch.head+1:ch.head+ftsize], fch.buf[fch.head+1:fch.head+ftsize])
	}

	return nil
}
