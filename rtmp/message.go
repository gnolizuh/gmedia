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
	ReadUint32() (uint32, error)
	ReadUint16() (uint16, error)
	ReadUint8() (uint8, error)
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
	UserMessageStreamBegin UserMessageType = iota // 0
	UserMessageStreamEOF
	UserMessageStreamDry
	UserMessageStreamSetBufLen
	UserMessageStreamIsRecorded
	UserMessageUnknown
	UserMessagePingRequest
	UserMessagePingResponse
	UserMessageMax
)

type CommandName string

var chunkPool = sync.Pool{
	New: func() any {
		return newChunk(DefaultChunkSize)
	},
}

// Chunk RTMP message chunk declare.
type Chunk struct {
	buf []byte

	// for reading
	offr uint32

	// for writing
	offw uint32
	head uint32

	// for sending
	offs uint32
}

func newChunk(cs uint32) *Chunk {
	return &Chunk{
		buf:  make([]byte, cs+MaxHeaderSize),
		offr: MaxHeaderSize,
		offw: MaxHeaderSize,
		head: MaxHeaderSize,
		offs: MaxHeaderSize,
	}
}

func (ck *Chunk) reset(n uint32) {
	if n > ck.head {
		panic(fmt.Sprintf("chunk reset overflow: %d > %d", n, ck.head))
	}

	ck.head -= n
	ck.offs = ck.head
}

func (ck *Chunk) Send(w io.Writer) error {
	for ck.offs < ck.offw {
		n, err := w.Write(ck.buf[ck.offs:ck.offw])
		if err != nil {
			return err
		}
		ck.offs += uint32(n)
	}
	return nil
}

func (ck *Chunk) Bytes(n uint32) []byte {
	// assert n <= len(ck.buf[ck.off:])
	return ck.buf[ck.offr : ck.offr+n]
}

func (ck *Chunk) Read(p []byte) (int, error) {
	n := uint32(len(p))
	m := uint32(len(ck.buf[ck.offr:]))

	if r := uint32(math.Min(float64(n), float64(m))); r > 0 {
		copy(p, ck.buf[ck.offr:ck.offr+r])
		ck.offr += r
		return int(r), nil
	}

	return 0, io.EOF
}

func (ck *Chunk) Write(p []byte) (int, error) {
	n := uint32(len(p))
	m := uint32(len(ck.buf[ck.offw:]))

	if w := uint32(math.Min(float64(n), float64(m))); w > 0 {
		copy(ck.buf[ck.offw:], p[:w])
		ck.offw += w
		return int(w), nil
	}

	return 0, io.EOF
}

func (ck *Chunk) ReadByte() (byte, error) {
	if len(ck.buf[ck.offr:]) <= 0 {
		return 0, io.EOF
	}
	b := ck.buf[ck.offr]
	ck.offr++
	return b, nil
}

func (ck *Chunk) WriteByte(c byte) error {
	if ck.offw == uint32(len(ck.buf)) {
		return io.EOF
	}
	ck.buf[ck.offw] = c
	ck.offw++
	return nil
}

func (ck *Chunk) Size() uint32 {
	return ck.offw - ck.head
}

type Chunks struct {
	chunks []*Chunk

	has uint32

	// for reading
	offr uint32

	// for writing
	offw uint32

	// for sending
	offs uint32
}

func newChunks() *Chunks {
	return &Chunks{
		chunks: []*Chunk{},
		has:    0,
		offr:   0,
		offw:   0,
		offs:   0,
	}
}

func (cks *Chunks) appendChunk(ch *Chunk) {
	cks.chunks = append(cks.chunks, ch)
	cks.has++
}

// Read reads data into p. It returns the number of bytes
// read into p. The bytes are taken from at most one Read
// on the underlying Reader, hence n may be less than
// len(p). At EOF, the count will be zero and err will be
// io.EOF.
func (cks *Chunks) Read(p []byte) (int, error) {
	l := len(p)
	r := 0
	for cks.offr < cks.has {
		ch := cks.chunks[cks.offr]
		n, err := ch.Read(p[r:])
		if err != nil {
			return r + n, err
		}
		r += n
		l -= n
		if len(ch.buf[ch.offr:]) == 0 {
			cks.offr++
		}
		if l == 0 {
			return r, nil
		}
	}
	return r, io.EOF
}

func (cks *Chunks) Write(p []byte) (int, error) {
	l := len(p)
	w := 0
	for cks.offw < cks.has {
		ch := cks.chunks[cks.offw]
		n, err := ch.Write(p[w:])
		if err != nil {
			return w + n, err
		}
		w += n
		l -= n
		if len(ch.buf[ch.offw:]) == 0 {
			cks.offw++
		}
		if l == 0 {
			return w, nil
		}
	}
	return w, io.EOF
}

func (cks *Chunks) ReadByte() (byte, error) {
	if cks.offr < cks.has {
		ch := cks.chunks[cks.offr]
		b, err := ch.ReadByte()
		if err != nil {
			return 0, err
		}
		if len(ch.buf[ch.offr:]) == 0 {
			cks.offr++
		}
		return b, nil
	}
	return 0, io.EOF
}

func (cks *Chunks) WriteByte(c byte) error {
	if cks.offw < cks.has {
		ch := cks.chunks[cks.offw]
		err := ch.WriteByte(c)
		if err != nil {
			return err
		}
		if len(ch.buf[ch.offw:]) == 0 {
			cks.offw++
		}
		return nil
	}
	return io.EOF
}

func (cks *Chunks) Size() uint32 {
	l := uint32(0)
	for i := cks.offw; i < cks.has; i++ {
		l += cks.chunks[cks.offw].Size()
	}
	return l
}

// Message RTMP message declare. The message header contains
// the following:
//
// Message Type: One byte field to represent the message type.
// A range of type IDs (1-6) are reserved for protocol control
// messages.
//
// Length: Three-byte field that represents the size of the
// payload in bytes. It is set in big-endian format.
//
// Timestamp: Four-byte field that contains a timestamp of
// the message. The 4 bytes are packed in the big-endian order.
//
// Message Stream Id: Three-byte field that identifies the
// stream of the message. These bytes are set in big-endian format.
//
// 0                   1                   2                   3
// 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// | Message Type  |                Payload length                 |
// |    (1 byte)   |                   (3 bytes)                   |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// |                           Timestamp                           |
// |                           (4 bytes)                           |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
// |                 Stream ID                     |
// |                 (3 bytes)                     |
// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
type Message struct {
	// Type are reserved for protocol control messages.
	Type MessageType

	// Length represents the size of the payload in bytes.
	Length uint32

	// Timestamp contains a timestamp of the message.
	Timestamp uint32

	// StreamId identifies the stream of the message
	StreamId uint32

	Chunks *Chunks
}

func NewMessage() *Message {
	msg := Message{
		Chunks: newChunks(),
	}
	return &msg
}

func (m *Message) appendChunk(ck *Chunk) {
	m.Chunks.appendChunk(ck)
}

func (m *Message) Send(w io.Writer) error {
	cks := m.Chunks
	for cks.offs <= cks.offw {
		err := cks.chunks[cks.offs].Send(w)
		if err != nil {
			return err
		}
		cks.offs++
	}
	return nil
}

func (m *Message) Read(p []byte) (int, error) {
	return m.Chunks.Read(p)
}

func (m *Message) ReadByte() (byte, error) {
	return m.Chunks.ReadByte()
}

func (m *Message) ReadUint32() (uint32, error) {
	var b uint32
	err := binary.Read(m, binary.BigEndian, &b)
	if err != nil {
		return 0, err
	}

	return b, nil
}

func (m *Message) ReadUint16() (uint16, error) {
	var b uint16
	err := binary.Read(m, binary.BigEndian, &b)
	if err != nil {
		return 0, err
	}

	return b, nil
}

func (m *Message) ReadUint8() (uint8, error) {
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
		ck := chunkPool.Get().(*Chunk)
		m.appendChunk(ck)
	}
}

var chunkHeaderSize = []uint8{12, 8, 4, 1}

func (m *Message) prepare(prev *Header) error {
	// message whether was prepared or not
	//if m.ready {
	//	return nil
	//}

	if m.hdr.csid > MaxStreamsNum {
		return errors.New(fmt.Sprintf("RTMP out chunk stream too big: %d >= %d", m.hdr.csid, MaxStreamsNum))
	}

	size := m.Chunks.Size()
	timestamp := m.Timestamp

	ft := uint8(0)
	if prev != nil && prev.csid > 0 && m.StreamId == prev.msid {
		ft++
		if m.Type == prev.typo && size > 0 && size == prev.mlen {
			ft++
			if m.Timestamp == prev.timestamp {
				ft++
			}
		}
		timestamp = m.Timestamp - prev.timestamp
	}

	if prev != nil {
		*prev = *m.hdr
		prev.mlen = size
	}

	hsize := chunkHeaderSize[ft]

	log.Printf("RTMP prep %s (%d) fmt=%d csid=%d timestamp=%d mlen=%d msid=%d",
		messageType(m.Type), m.Type, ft,
		m.hdr.csid, timestamp, size, m.StreamId)

	ext := uint32(0)
	if timestamp >= 0x00ffffff {
		ext = timestamp
		timestamp = 0x00ffffff
		hsize += 4
	}

	if m.hdr.csid >= 64 {
		hsize++
		if m.hdr.csid >= 320 {
			hsize++
		}
	}

	fch := m.Chunks.chunks[0]
	fch.reset(uint32(hsize))
	head := fch.head

	var ftsize uint32
	ftt := ft << 6
	if m.hdr.csid >= 2 && m.hdr.csid <= 63 {
		fch.buf[head] = ftt | (uint8(m.hdr.csid) & 0x3f)
		ftsize = 1
	} else if m.hdr.csid >= 64 && m.hdr.csid < 320 {
		fch.buf[head] = ftt
		fch.buf[head+1] = uint8(m.hdr.csid - 64)
		ftsize = 2
	} else {
		fch.buf[head] = ftt | 0x01
		fch.buf[head+1] = uint8(m.hdr.csid - 64)
		fch.buf[head+2] = uint8((m.hdr.csid - 64) >> 8)
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
			fch.buf[head+3] = byte(m.Type)
			head += 4
			if ft == 0 {
				fch.buf[head] = byte(m.StreamId >> 24)
				fch.buf[head+1] = byte(m.StreamId >> 16)
				fch.buf[head+2] = byte(m.StreamId >> 8)
				fch.buf[head+3] = byte(m.StreamId)
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
	for i := m.cks.offw + 1; i < m.cks.has; i++ {
		ch := m.cks.cks[i]
		ch.reset(ftsize)
		ch.buf[ch.head] = fch.buf[fch.head] | 0xc0
		copy(ch.buf[ch.head+1:ch.head+ftsize], fch.buf[fch.head+1:fch.head+ftsize])
	}

	m.ready = true

	return nil
}
