package rtmp

type Peer struct {
	// RemoteAddr allows RTMP servers and other software to record
	// the network address present by remote peer, usually for
	// logging. The RTMP server in this package sets RemoteAddr to
	// an "IP:port" address before invoking a handler.
	//
	// This field is ignored by the RTMP client.
	RemoteAddr string

	// Message is the RTMP message reader.
	//
	// Peer always carry out last message sent from remote peer.
	Reader Reader

	// Conn
	Conn *Conn

	// Handler
	handler Handler
}

func (p *Peer) setReader(reader Reader) {
	p.Reader = reader
}
