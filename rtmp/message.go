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

func (cr ChunkReader) ReadN(n int) (int, error) {
	w, err := cr.conn.ReadFull(cr.chunk.bytes(OffsetWrite, n))
	if err != nil {
		return w, err
	}
	cr.chunk.w += w
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
	chunks *Chunks

	// specifies RTMP ChunkSize.
	chunkSize uint32

	buf []byte

	// offset for reading, writing and sending.
	// h indicate the head portion.
	r, w, s, h int
}

var chunkPool sync.Pool

func (chunk *Chunk) reset(n int) {
	if n > chunk.h {
		panic(fmt.Sprintf("chunk reset overflow: %d > %d", n, chunk.h))
	}
	chunk.h -= n
	chunk.s = chunk.h
}

func (chunk *Chunk) terminated(typ OffsetType) bool {
	var off int
	switch typ {
	case OffsetRead:
		off = chunk.r
	case OffsetWrite:
		off = chunk.w
	case OffsetSend:
		off = chunk.s
	}
	return off == len(chunk.buf)
}

func (chunk *Chunk) bytes(typ OffsetType, n int) []byte {
	var off int
	switch typ {
	case OffsetRead:
		off = chunk.r
	case OffsetWrite:
		off = chunk.w
	case OffsetSend:
		off = chunk.s
	}
	return chunk.buf[off : off+n]
}

func (chunk *Chunk) size() int {
	return chunk.w - chunk.h
}

func (chunk *Chunk) Len() int {
	return chunk.r - chunk.h
}

func (chunk *Chunk) Send() error {
	conn := chunk.chunks.msg.conn
	for chunk.s < chunk.w {
		n, err := conn.rwc.Write(chunk.buf[chunk.s:chunk.w])
		if err != nil {
			return err
		}
		chunk.s += n
	}
	return nil
}

func (chunk *Chunk) ReadByte() (byte, error) {
	bs := make([]byte, 1)
	_, err := chunk.Read(bs)
	if err != nil {
		return 0, err
	}
	return bs[0], nil
}

func (chunk *Chunk) Read(p []byte) (int, error) {
	n := float64(len(p))
	m := float64(len(chunk.buf[chunk.r:chunk.w]))
	if r := int(math.Min(n, m)); r > 0 {
		copy(p, chunk.buf[chunk.r:chunk.r+r])
		chunk.r += r
		return r, nil
	}
	return 0, io.EOF
}

func (chunk *Chunk) Write(p []byte) (int, error) {
	n := float64(len(p))
	m := float64(len(chunk.buf[chunk.w:]))
	if w := int(math.Min(n, m)); w > 0 {
		copy(chunk.buf[chunk.w:], p[:w])
		chunk.w += w
		return w, nil
	}
	return 0, io.EOF
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

var chunkHeaderSize = []uint8{12, 8, 4, 1}

func (chunk *Chunk) packaging(prev *Header) error {
	if chunk.chunks.msg.ChunkStreamId > MaxStreamsNum {
		return errors.New(fmt.Sprintf("RTMP out chunk stream too big: %d >= %d",
			chunk.chunks.msg.ChunkStreamId, MaxStreamsNum))
	}

	size := chunk.chunks.chunkSize
	timestamp := chunk.chunks.msg.Timestamp

	ft := uint8(0)
	if prev != nil && prev.ChunkStreamId > 0 && chunk.chunks.msg.MessageStreamId == prev.MessageStreamId {
		ft++
		if chunk.chunks.msg.MessageTypeId == prev.MessageTypeId && size > 0 && size == prev.MessageLength {
			ft++
			if chunk.chunks.msg.Timestamp == prev.Timestamp {
				ft++
			}
		}
		timestamp = chunk.chunks.msg.Timestamp - prev.Timestamp
	}

	hs := chunkHeaderSize[ft]

	log.Printf("RTMP prep %s (%d) fmt=%d csid=%d timestamp=%d mlen=%d msid=%d",
		chunk.chunks.msg.MessageTypeId, chunk.chunks.msg.MessageTypeId, ft,
		chunk.chunks.msg.ChunkStreamId, timestamp, size, chunk.chunks.msg.MessageStreamId)

	ext := uint32(0)
	if timestamp >= 0x00ffffff {
		ext = timestamp
		timestamp = 0x00ffffff
		hs += 4
	}

	if chunk.chunks.msg.ChunkStreamId >= 64 {
		hs++
		if chunk.chunks.msg.ChunkStreamId >= 320 {
			hs++
		}
	}

	fch := chunk.chunks.chunks[0]
	fch.reset(int(hs))
	h := fch.h

	var ftsize int
	ftt := ft << 6
	if chunk.chunks.msg.ChunkStreamId >= 2 && chunk.chunks.msg.ChunkStreamId <= 63 {
		fch.buf[h] = ftt | (uint8(chunk.chunks.msg.ChunkStreamId) & 0x3f)
		ftsize = 1
	} else if chunk.chunks.msg.ChunkStreamId >= 64 && chunk.chunks.msg.ChunkStreamId < 320 {
		fch.buf[h] = ftt
		fch.buf[h+1] = uint8(chunk.chunks.msg.ChunkStreamId - 64)
		ftsize = 2
	} else {
		fch.buf[h] = ftt | 0x01
		fch.buf[h+1] = uint8(chunk.chunks.msg.ChunkStreamId - 64)
		fch.buf[h+2] = uint8((chunk.chunks.msg.ChunkStreamId - 64) >> 8)
		ftsize = 3
	}

	h += ftsize
	if ft <= 2 {
		fch.buf[h] = byte(timestamp >> 16)
		fch.buf[h+1] = byte(timestamp >> 8)
		fch.buf[h+2] = byte(timestamp)
		h += 3
		if ft <= 1 {
			fch.buf[h] = byte(size >> 16)
			fch.buf[h+1] = byte(size >> 8)
			fch.buf[h+2] = byte(size)
			fch.buf[h+3] = byte(chunk.chunks.msg.MessageTypeId)
			h += 4
			if ft == 0 {
				fch.buf[h] = byte(chunk.chunks.msg.MessageStreamId >> 24)
				fch.buf[h+1] = byte(chunk.chunks.msg.MessageStreamId >> 16)
				fch.buf[h+2] = byte(chunk.chunks.msg.MessageStreamId >> 8)
				fch.buf[h+3] = byte(chunk.chunks.msg.MessageStreamId)
				h += 4
			}
		}
	}

	// extend timestamp
	if ext > 0 {
		fch.buf[h] = byte(ext >> 24)
		fch.buf[h+1] = byte(ext >> 16)
		fch.buf[h+2] = byte(ext >> 8)
		fch.buf[h+3] = byte(ext)
	}

	// set following chunk's fmt to be 3
	for i := chunk.chunks.w + 1; i < len(chunk.chunks.chunks); i++ {
		ck := chunk.chunks.chunks[i]
		ck.reset(ftsize)
		ck.buf[ck.h] = fch.buf[fch.h] | 0xc0
		copy(ck.buf[ck.h+1:ck.h+ftsize], fch.buf[fch.h+1:fch.h+ftsize])
	}

	return nil
}

type Chunks struct {
	msg *Message

	// specifies RTMP ChunkSize.
	chunkSize uint32

	chunks []*Chunk

	// size of Chunks
	sz uint32

	// index of array for reading, writing and sending
	r, w, s int
}

func (chunks *Chunks) NewChunk() *Chunk {
	if v := chunkPool.Get(); v != nil {
		chunk := v.(*Chunk)
		chunk.chunks = chunks
		chunk.chunkSize = chunks.chunkSize
		chunk.buf = make([]byte, chunks.chunkSize+MaxHeaderBufferSize)
		chunk.r = MaxHeaderBufferSize
		chunk.w = MaxHeaderBufferSize
		chunk.h = MaxHeaderBufferSize
		chunk.s = MaxHeaderBufferSize
		return chunk
	}
	return &Chunk{
		chunks:    chunks,
		chunkSize: chunks.chunkSize,
		buf:       make([]byte, chunks.chunkSize+MaxHeaderBufferSize),
		r:         MaxHeaderBufferSize,
		w:         MaxHeaderBufferSize,
		h:         MaxHeaderBufferSize,
		s:         MaxHeaderBufferSize,
	}
}

// scale enlarges the cap of the chunks by chunk-size
func (chunks *Chunks) scale() {
	chunks.chunks = append(chunks.chunks, chunks.NewChunk())
}

func (chunks *Chunks) size() uint32 {
	return chunks.sz
}

func (chunks *Chunks) append(chunk *Chunk) {
	chunks.chunks = append(chunks.chunks, chunk)
	chunks.sz += uint32(chunk.size())
}

func (chunks *Chunks) Len() int {
	l := 0
	for i := range chunks.chunks {
		l += chunks.chunks[i].Len()
	}
	return l
}

// Read reads data into p. It returns the number of bytes
// read into p. The bytes are taken from at most one Read
// on the underlying Reader, hence n may be less than
// len(p). At EOF, the count will be zero and err will be
// io.EOF.
func (chunks *Chunks) Read(p []byte) (int, error) {
	r := 0
	for len(p)-r > 0 {
		ck := chunks.chunks[chunks.r]
		n, err := ck.Read(p[r:])
		if err != nil {
			return r + n, err
		}
		r += n
		if ck.terminated(OffsetRead) {
			chunks.r++
		}
	}
	return r, nil
}

func (chunks *Chunks) Write(p []byte) (int, error) {
	w := 0
	for len(p)-w > 0 {
		if len(chunks.chunks) <= chunks.w {
			chunks.scale()
		}
		ck := chunks.chunks[chunks.w]
		n, err := ck.Write(p[w:])
		if err != nil {
			return w + n, err
		}
		w += n
		if ck.terminated(OffsetWrite) {
			chunks.w++
		}
	}
	return w, nil
}

func (chunks *Chunks) ReadByte() (byte, error) {
	if chunks.r < len(chunks.chunks) {
		ck := chunks.chunks[chunks.r]
		b, err := ck.ReadByte()
		if err != nil {
			return 0, err
		}
		if len(ck.buf[ck.r:]) == 0 {
			chunks.r++
		}
		return b, nil
	}
	return 0, io.EOF
}

func (chunks *Chunks) WriteByte(c byte) error {
	if chunks.w < len(chunks.chunks) {
		ck := chunks.chunks[chunks.w]
		err := ck.WriteByte(c)
		if err != nil {
			return err
		}
		if len(ck.buf[ck.w:]) == 0 {
			chunks.w++
		}
		return nil
	}
	return io.EOF
}

func (chunks *Chunks) Send() error {
	for chunks.s <= chunks.w {
		if err := chunks.chunks[chunks.s].Send(); err != nil {
			return err
		}
		chunks.s++
	}
	return nil
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

type BasicHeader struct {
	// MessageTypeId are reserved for protocol control messages.
	MessageTypeId MessageType

	// Timestamp contains a timestamp of the message.
	Timestamp uint32

	// ChunkStreamId identifies the chunk stream of the message
	ChunkStreamId uint32

	// MessageStreamId identifies the stream of the message
	MessageStreamId uint32

	// MessageLength represents the size of the payload in bytes.
	MessageLength uint32
}

// Header declare.
type Header struct {
	BasicHeader

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

func readHeader(conn *conn, hdr *Header) error {
	buf := newHeaderBuffer()
	defer headerBufferPool.Put(buf)

	// basic header
	off := uint32(0)
	_, err := conn.ReadFull(buf[off : off+1])
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
		_, err := conn.ReadFull(buf[off : off+1])
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
		_, err := conn.ReadFull(buf[off : off+2])
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
		_, err := conn.ReadFull(buf[off : off+3])
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
			_, err := conn.ReadFull(buf[off : off+4])
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
				_, err := conn.ReadFull(buf[off : off+4])
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
	BasicHeader

	ChunkStream *ChunkStream
	Chunks      *Chunks

	conn *conn

	// size we have
	has uint32
}

func (msg *Message) NewChunks() *Chunks {
	return &Chunks{
		msg:       msg,
		chunkSize: DefaultReadChunkSize,
		chunks:    []*Chunk{},
	}
}

func (msg *Message) append(chunk *Chunk) {
	msg.Chunks.append(chunk)
	msg.has += uint32(chunk.size())
}

func (msg *Message) completed() bool {
	return msg.MessageLength == msg.has
}

func (msg *Message) readChunk() error {
	hdr := newHeader()
	defer headerBufferPool.Put(hdr)

	err := readHeader(msg.conn, hdr)
	if err != nil {
		return err
	}

	// indicate timestamp whether is absolute or relate.
	cs := &msg.conn.chunkStreams[hdr.ChunkStreamId]
	if msg.ChunkStream == nil {
		msg.ChunkStream = cs
	} else if msg.ChunkStream != cs {
		panic("chunk-stream must be strictly continuous")
	}

	if cs.hdr == nil {
		cs.hdr = newHeader()
	}

	cs.hdr.Format = hdr.Format
	cs.hdr.ChunkStreamId = hdr.ChunkStreamId
	switch hdr.Format {
	case 0:
		cs.hdr.Timestamp = hdr.Timestamp
		cs.hdr.MessageLength = hdr.MessageLength
		cs.hdr.MessageTypeId = hdr.MessageTypeId
		cs.hdr.MessageStreamId = hdr.MessageStreamId
	case 1:
		cs.hdr.Timestamp += hdr.Timestamp
		cs.hdr.MessageLength = hdr.MessageLength
		cs.hdr.MessageTypeId = hdr.MessageTypeId
	case 2:
		cs.hdr.Timestamp += hdr.Timestamp
	case 3:
		// see https://rtmp.veriskope.com/docs/spec/#53124-type-3
		if cs.hdr != nil && cs.hdr.Format == 0 {
			cs.hdr.Timestamp += cs.hdr.Timestamp
		}

		// read extend timestamp
		if cs.hdr.Timestamp == 0x00ffffff {
			buf := make([]byte, 4)
			_, err := msg.conn.ReadFull(buf)
			if err != nil {
				return err
			}
			cs.hdr.Timestamp = binary.BigEndian.Uint32(buf)
		}
	default:
		panic("unknown format type")
	}

	// calculate bytes needed.
	need := int(math.Min(float64(cs.hdr.MessageLength-msg.has), float64(msg.conn.chunkSize)))

	chunk := msg.Chunks.NewChunk()
	reader := ChunkReader{conn: msg.conn, chunk: chunk}
	if _, err = reader.ReadN(need); err != nil {
		return err
	}
	msg.append(chunk)

	if msg.completed() {
		msg.ChunkStreamId = hdr.ChunkStreamId
		msg.MessageStreamId = hdr.MessageStreamId
		msg.MessageTypeId = hdr.MessageTypeId
		msg.MessageLength = hdr.MessageLength
		msg.Timestamp = hdr.Timestamp
	}

	return nil
}

func (msg *Message) Send() error {
	return msg.Chunks.Send()
}

// Len returns the number of bytes of the unread portion of the
// slice.
func (msg *Message) Len() int {
	return msg.Chunks.Len()
}

func (msg *Message) Read(p []byte) (int, error) {
	return msg.Chunks.Read(p)
}

func (msg *Message) ReadByte() (byte, error) {
	return msg.Chunks.ReadByte()
}

func (msg *Message) UnreadByte() error {
	return nil
}

func (msg *Message) ReadUInt32() (uint32, error) {
	var b uint32
	err := binary.Read(msg, binary.BigEndian, &b)
	if err != nil {
		return 0, err
	}
	return b, nil
}

func (msg *Message) ReadUInt16() (uint16, error) {
	var b uint16
	err := binary.Read(msg, binary.BigEndian, &b)
	if err != nil {
		return 0, err
	}
	return b, nil
}

func (msg *Message) ReadUInt8() (uint8, error) {
	b, err := msg.ReadByte()
	if err != nil {
		return 0, err
	}
	return b, nil
}

func (msg *Message) Write(p []byte) (int, error) {
	return msg.Chunks.Write(p)
}

func (msg *Message) WriteByte(b byte) error {
	return msg.Chunks.WriteByte(b)
}
