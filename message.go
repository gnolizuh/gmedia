package rtmp

const (
	MessageSetChunkSize = iota + 1  // 1
	MessageAbort
	MessageAck
	MessageUserControl
	MessageWindowAckSize
	MessageSetPeerBandwidth
	MessageEdge
	MessageAudio
	MessageVideo
	MessageAmf3Meta = iota + 6      // 15
	MessageAmf3Shared
	MessageAmf3Cmd
	MessageAmfMeta
	MessageAmfShared
	MessageAmfCmd
	MessageAggregate = iota + 1     // 22
	MessageMax
)

type MessageReader interface {
	readMessage(*Conn, *Message) error
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
