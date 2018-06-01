package rtmp

import (
	"io"
	"errors"
	"bufio"
	"fmt"
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

// RTMP message chunk declare.
type Chunk struct {
	buf []byte
	off uint32
	pos uint32
}

func newChunk(cs uint32) *Chunk {
	return &Chunk{
		buf: make([]byte, cs + MaxMessageHeaderSize),
		off: MaxMessageHeaderSize,
		pos: MaxMessageHeaderSize,
	}
}

func (ck *Chunk) Bytes(n uint32) []byte {
	// assert n <= len(ck.buf[ck.off:])
	return ck.buf[ck.off:ck.off+n]
}

func (ck *Chunk) Read(p []byte) (int, error) {
	n := uint32(len(p))
	m := uint32(len(ck.buf[ck.off:]))

	if read := min(n, m); read > 0 {
		copy(p, ck.buf[ck.off:ck.off+read])
		ck.off += read
		return int(read), nil
	}

	return 0, io.EOF
}

func (ck *Chunk) ReadByte() (byte, error) {
	if len(ck.buf[ck.off:]) <= 0 {
		return 0, io.EOF
	}
	b := ck.buf[ck.off]
	ck.off++
	return b, nil
}

func (ck *Chunk) Size() uint32 {
	return ck.off - ck.pos
}

type ChunkList struct {
	chs []*Chunk
	off uint32
	has uint32
}

func newChunkList() *ChunkList {
	return &ChunkList{
		chs: []*Chunk{},
		off: 0,
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
	for cl.off < cl.has {
		ch := cl.chs[cl.off]
		n, err := ch.Read(p[r:])
		if err != nil {
			return r + n, err
		}
		r += n
		l -= n
		if len(ch.buf[ch.off:]) == 0 {
			ch.off++
		}
		if l == 0 {
			return r, nil
		}
	}
	return r, io.EOF
}

func (cl *ChunkList) ReadByte() (byte, error) {
	if cl.off < cl.has {
		ch := cl.chs[cl.off]
		b, err := ch.ReadByte()
		if err != nil {
			return 0, err
		}
		if len(ch.buf[ch.off:]) == 0 {
			ch.off++
		}
		return b, nil
	}
	return 0, io.EOF
}

func (cl *ChunkList) Size() (uint32) {
	l := uint32(0)
	for i := cl.off; i < cl.has; i++ {
		i += cl.chs[cl.off].Size()
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

// TODO: fill with RTMP header
func (m *Message) prepare(hdr *Header) error {
	if m.hdr.csid > MaxStreamsNum {
		return errors.New(fmt.Sprintf("RTMP out chunk stream too big: %d >= %d", m.hdr.csid, MaxStreamsNum))
	}

	// n := m.body.Size()
	return nil
}