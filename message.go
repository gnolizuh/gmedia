package rtmp

import (
	"io"
	"errors"
	"bufio"
	"fmt"
	"log"
	"sync"
)

type MessageType uint

const (
	MessageSetChunkSize MessageType = iota + 1     // 1
	MessageAbort
	MessageAck
	MessageUserControl
	MessageWindowAckSize
	MessageSetPeerBandwidth
	MessageEdge
	MessageAudio
	MessageVideo
)

const (
	MessageAmf3Meta = iota + MessageVideo + 6      // 15
	MessageAmf3Shared
	MessageAmf3Cmd
	MessageAmf0Meta
	MessageAmf0Shared
	MessageAmf0Cmd
)

const (
	MessageAggregate = iota + MessageAmf0Cmd + 2   // 22
	MessageMax
)

const (
	UserMessageStreamBegin = iota                  // 0
	UserMessageStreamEOF
	UserMessageStreamDry
	UserMessageStreamSetBufLen
	UserMessageStreamIsRecorded
	UserMessageUnknown
	UserMessagePingRequest
	UserMessagePingResponse
	UserMessageMax
)

type MessageReader interface {
	OnUserControl(uint16, *bufio.Reader) error
	OnEdge() error
	OnAudio() error
	OnVideo() error
	OnAmf() error
	OnAggregate() error
}

func min(n, m uint32) uint32 {
	if n < m { return n } else { return m }
}

func max(n, m uint32) uint32 {
	if n > m { return n } else { return m }
}

func messageType(typo uint8) string {
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

	if typo < uint8(len(types)) {
		return types[typo]
	} else {
		return "?"
	}
}

var sharedBufferPool = sync.Pool{
	New: func() interface{} {
		hd := make([]byte, DefaultSendChunkSize)
		return &hd
	},
}

// RTMP message chunk declare.
type Chunk struct {
	buf  []byte

	// for reading
	offr uint32

	// for writing
	offw uint32
	head uint32
}

func newChunk(cs uint32) *Chunk {
	return &Chunk{
		buf: make([]byte, cs + MaxMessageHeaderSize),
		offr: MaxMessageHeaderSize,
		offw: MaxMessageHeaderSize,
		head: MaxMessageHeaderSize,
	}
}

func (ck *Chunk) Bytes(n uint32) []byte {
	// assert n <= len(ck.buf[ck.off:])
	return ck.buf[ck.offr:ck.offr+n]
}

func (ck *Chunk) Read(p []byte) (int, error) {
	n := uint32(len(p))
	m := uint32(len(ck.buf[ck.offr:]))

	if r := min(n, m); r > 0 {
		copy(p, ck.buf[ck.offr:ck.offr+r])
		ck.offr += r
		return int(r), nil
	}

	return 0, io.EOF
}

func (ck *Chunk) Write(p []byte) (int, error) {
	n := uint32(len(p))
	m := uint32(len(ck.buf[ck.offw:]))

	if w := min(n, m); w > 0 {
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

func (ck Chunk) Size() uint32 {
	return ck.offw - ck.head
}

type ChunkList struct {
	chs  []*Chunk
	has  uint32

	// for reading
	offr uint32
	// for writing
	offw uint32
}

func newChunkList() *ChunkList {
	return &ChunkList{
		chs: []*Chunk{},
		offr: 0,
		offw: 0,
		has: 0,
	}
}

func (cl *ChunkList) appendChunk(ch *Chunk) {
	cl.chs = append(cl.chs, ch)
	cl.has++
}

// Read reads data into p. It returns the number of bytes
// read into p. The bytes are taken from at most one Read
// on the underlying Reader, hence n may be less than
// len(p). At EOF, the count will be zero and err will be
// io.EOF.
func (cl *ChunkList) Read(p []byte) (int, error) {
	l := len(p)
	r := 0
	for cl.offr < cl.has {
		ch := cl.chs[cl.offr]
		n, err := ch.Read(p[r:])
		if err != nil {
			return r + n, err
		}
		r += n
		l -= n
		if len(ch.buf[ch.offr:]) == 0 {
			cl.offr++
		}
		if l == 0 {
			return r, nil
		}
	}
	return r, io.EOF
}

func (cl *ChunkList) Write(p []byte) (int, error) {
	l := len(p)
	w := 0
	for cl.offw < cl.has {
		ch := cl.chs[cl.offw]
		n, err := ch.Write(p[w:])
		if err != nil {
			return w + n, err
		}
		w += n
		l -= n
		if len(ch.buf[ch.offw:]) == 0 {
			cl.offw++
		}
		if l == 0 {
			return w, nil
		}
	}
	return w, io.EOF
}

func (cl *ChunkList) ReadByte() (byte, error) {
	if cl.offr < cl.has {
		ch := cl.chs[cl.offr]
		b, err := ch.ReadByte()
		if err != nil {
			return 0, err
		}
		if len(ch.buf[ch.offr:]) == 0 {
			cl.offr++
		}
		return b, nil
	}
	return 0, io.EOF
}

func (cl *ChunkList) WriteByte(c byte) error {
	if cl.offw < cl.has {
		ch := cl.chs[cl.offw]
		err := ch.WriteByte(c)
		if err != nil {
			return err
		}
		if len(ch.buf[ch.offw:]) == 0 {
			cl.offw++
		}
		return nil
	}
	return io.EOF
}

func (cl ChunkList) Size() (uint32) {
	l := uint32(0)
	for i := cl.offw; i < cl.has; i++ {
		l += cl.chs[cl.offw].Size()
	}
	return l
}

// RTMP message declare.
type Message struct {
	hdr  *Header
	cl   *ChunkList
}

func newMessage(hdr *Header) *Message {
	msg := Message {
		hdr: hdr,
		cl: newChunkList(),
	}
	return &msg
}

func (m *Message) appendChunk(ck *Chunk) {
	m.cl.appendChunk(ck)
}

func (m *Message) Read(p []byte) (int, error) {
	return m.cl.Read(p)
}

func (m *Message) ReadByte() (byte, error) {
	return m.cl.ReadByte()
}

func (m *Message) Write(p []byte) (int, error) {
	return m.cl.Write(p)
}

func (m *Message) WriteByte(c byte) error {
	return m.cl.WriteByte(c)
}

func (m *Message) alloc(n uint32) {
	for i := (n / DefaultSendChunkSize) + 1; i > 0; i-- {
		ck := sharedBufferPool.Get().(*Chunk)
		m.appendChunk(ck)
	}
}

func (m *Message) prepare(prev *Header) error {
	hdrsize := []uint8{12, 8, 4, 1}

	if m.hdr.csid > MaxStreamsNum {
		return errors.New(fmt.Sprintf("RTMP out chunk stream too big: %d >= %d", m.hdr.csid, MaxStreamsNum))
	}

	size := m.cl.Size()
	timestamp := m.hdr.timestamp

	ft := uint8(0)
	if prev != nil && prev.csid > 0 && m.hdr.msid == prev.msid {
		ft++
		if m.hdr.typo == prev.typo && size > 0 && size == prev.mlen {
			ft++
			if m.hdr.timestamp == prev.timestamp {
				ft++
			}
		}
		timestamp = m.hdr.timestamp - prev.timestamp
	}

	if prev != nil {
		*prev = *m.hdr
		prev.mlen = size
	}

	hsize := hdrsize[ft]

	log.Printf("RTMP prep %s (%d) fmt=%d csid=%d timestamp=%d mlen=%d msid=%d",
		messageType(prev.typo), prev.typo, ft,
		prev.csid, timestamp, prev.mlen, prev.msid)

	exttime := uint32(0)
	if timestamp >= 0x00ffffff {
		exttime = timestamp
		timestamp = 0x00ffffff
		hsize += 4
	}

	if m.hdr.csid >= 64 {
		hsize++
		if m.hdr.csid >= 320 {
			hsize++
		}
	}

	fch := m.cl.chs[0]
	fch.head -= uint32(hsize)
	head := fch.head

	var ftsize uint32
	ftt := ft << 6
	if m.hdr.csid >= 2 && m.hdr.csid <= 63 {
		fch.buf[head] = ftt | (uint8(m.hdr.csid) & 0x3f)
		ftsize = 1
	} else if m.hdr.csid >= 64 &&  m.hdr.csid < 320 {
		fch.buf[head] = ftt
		fch.buf[head+1] = uint8(m.hdr.csid - 64)
		ftsize = 2
	} else {
		fch.buf[head] = ftt | 0x01
		fch.buf[head+1] = uint8(m.hdr.csid - 64)
		fch.buf[head+2] = uint8(m.hdr.csid - 64) >> 8
		ftsize = 3
	}

	head += ftsize
	if ft <= 2 {
		fch.buf[head]   = byte(timestamp >> 16)
		fch.buf[head+1] = byte(timestamp >> 8)
		fch.buf[head+2] = byte(timestamp)
		head += 3
		if ft <= 1 {
			fch.buf[head]   = byte(size >> 16)
			fch.buf[head+1] = byte(size >> 8)
			fch.buf[head+2] = byte(size)
			fch.buf[head+3] = m.hdr.typo
			head += 4
			if ft == 0 {
				fch.buf[head]   = byte(m.hdr.msid >> 24)
				fch.buf[head+1] = byte(m.hdr.msid >> 16)
				fch.buf[head+2] = byte(m.hdr.msid >> 8)
				fch.buf[head+3] = byte(m.hdr.msid)
				head += 4
			}
		}
	}

	// extend timestamp
	if exttime > 0 {
		fch.buf[head]   = byte(exttime >> 24)
		fch.buf[head+1] = byte(exttime >> 16)
		fch.buf[head+2] = byte(exttime >> 8)
		fch.buf[head+3] = byte(exttime)
	}

	// set following chunk's fmt to be 3
	for i := m.cl.offw+1; i < m.cl.has; i++ {
		ch := m.cl.chs[i]
		ch.head -= ftsize
		ch.buf[ch.head] = uint8(fch.buf[fch.head] | 0xc0)
		copy(ch.buf[ch.head+1:ch.head+ftsize], fch.buf[fch.head+1:fch.head+ftsize])
	}

	return nil
}
