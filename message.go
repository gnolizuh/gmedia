package rtmp

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

type MessageReader interface {
	OnSetChunkSize() error
	OnAbort() error
	OnAck() error
	OnUserControl() error
	OnWinAckSize() error
	OnSetPeerBandwidth() error
	OnEdge() error
	OnAudio() error
	OnVideo() error
	OnAmf() error
	OnAggregate() error
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

func (m *Message) appendChunk(ch *[]byte) {
	m.chunks = append(m.chunks, ch)
}
