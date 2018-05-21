package rtmp

import (
	"io"
	"bufio"
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
type ChunkType struct {
	buf []byte
	off uint32
}

func (c *ChunkType) Read(p []byte) (int, error) {
	n := uint32(len(p))
	m := uint32(len(c.buf[c.off:]))

	if read := min(n, m); read > 0 {
		copy(p, c.buf[:read])
		c.off += read
		return int(read), nil
	}

	return 0, io.EOF
}

type ChunkList struct {
	chunks []*ChunkType
	read   uint32
	off    uint32
	has    uint32
}

func newChunkList() *ChunkList {
	return &ChunkList{
		chunks: make([]*ChunkType, 4),
		read: 0,
		off: 0,
		has: 0,
	}
}

func (cl *ChunkList) appendChunk(ch *ChunkType) {
	cl.chunks = append(cl.chunks, ch)
	cl.has++
}

// Read reads data into p. It returns the number of bytes
// read into p. The bytes are taken from at most one Read
// on the underlying Reader, hence n may be less than
// len(p). At EOF, the count will be zero and err will be
// io.EOF.
func (cl *ChunkList) Read(p []byte) (int, error) {
	off := 0
	for cl.off < cl.has {
		ch := cl.chunks[cl.off]
		n, err := io.ReadFull(ch, p[off:])
		if err != nil {
			return off, err
		}

		off += n

		// last chunk reached.
		if n < len(ch.buf) {
			return off, nil
		}

		// n MUST be equal to len(ch.buf).
		ch.off++
	}
	return off, io.EOF
}

// RTMP message declare.
type Message struct {
	hdr    *Header
	body   *ChunkList
	reader *bufio.Reader
}

func newMessage(hdr *Header) *Message {
	msg := Message {
		hdr: hdr,
		body: newChunkList(),
	}
	msg.reader = bufio.NewReader(msg.body)
	return &msg
}

func (m *Message) appendChunk(ch *ChunkType) {
	m.body.appendChunk(ch)
}
