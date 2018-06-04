package rtmp

import (
	"io"
	"errors"
	"bufio"
	"fmt"
	"log"
)

type MessageType int

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
	hdr    *Header
	body   *ChunkList
}

func newMessage(hdr *Header) *Message {
	msg := Message {
		hdr: hdr,
		body: newChunkList(),
	}
	return &msg
}

func (m *Message) appendChunk(ck *Chunk) {
	m.body.appendChunk(ck)
}

func (m *Message) Read(p []byte) (int, error) {
	return m.body.Read(p)
}

func (m *Message) ReadByte() (byte, error) {
	return m.body.ReadByte()
}

func (m *Message) prepare(hdr *Header) error {
	hdrsize := []uint8{12, 8, 4, 1}

	if m.hdr.csid > MaxStreamsNum {
		return errors.New(fmt.Sprintf("RTMP out chunk stream too big: %d >= %d", m.hdr.csid, MaxStreamsNum))
	}

	mlen := m.body.Size()
	timestamp := m.hdr.timestamp

	ft := uint8(0)
	if hdr != nil && hdr.csid > 0 && hdr.msid == m.hdr.msid {
		ft++
		if hdr.typo == m.hdr.typo && mlen > 0 && mlen == m.hdr.mlen {
			ft++
			if hdr.timestamp == m.hdr.timestamp {
				ft++
			}
		}
		timestamp = m.hdr.timestamp - hdr.timestamp
	}

	if hdr != nil {
        *hdr = *m.hdr
		hdr.mlen = mlen
    }

	hsize := hdrsize[ft]

	log.Printf("RTMP prep %s (%d) fmt=%d csid=%d timestamp=%d mlen=%d msid=%d",
		messageType(hdr.typo), hdr.typo, ft,
		hdr.csid, timestamp, hdr.mlen, hdr.msid)

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

	fch := m.body.chs[0]
	fch.head -= uint32(hsize)

	// basic header
	thsize := uint32(0)
	ftt := ft << 6
	if m.hdr.csid >= 2 && m.hdr.csid <= 63 {
		fch.buf[fch.head] = ftt | (uint8(m.hdr.csid) & 0x3f)
		thsize = 1
	} else if m.hdr.csid >= 64 &&  m.hdr.csid < 320 {
		fch.buf[fch.head] = ftt
		fch.buf[fch.head+1] = uint8(m.hdr.csid - 64)
		thsize = 2
	} else {
		fch.buf[fch.head] = ftt | 0x01
		fch.buf[fch.head+1] = uint8(m.hdr.csid - 64)
		fch.buf[fch.head+2] = uint8(m.hdr.csid - 64) >> 8
		thsize = 3
	}

	// TODO: message header
	if ft <= 2 {
		if ft <= 1 {
			if ft == 0 {
			}
		}
	}

	// TODO: extend timestamp
	if exttime > 0 {
	}

	for i := m.body.offw; i < m.body.has; i++ {
		ch := m.body.chs[m.body.offw]
		ch.head -= thsize
		ch.buf[ch.head] = uint8(fch.buf[fch.head] | 0xc0)
		copy(ch.buf[ch.head:], fch.buf[fch.head+1:thsize-1])
	}

	return nil
}
