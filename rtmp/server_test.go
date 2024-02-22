package rtmp

import "testing"

type ServerTest struct {
}

func (st *ServerTest) ServeNew(p *Peer) ServeState {
	return ServeDeclined
}

func (st *ServerTest) ServeMessage(typo MessageType, p *Peer) ServeState {
	return ServeDeclined
}

func (st *ServerTest) ServeUserMessage(typo UserMessageType, p *Peer) ServeState {
	return ServeDeclined
}

func (st *ServerTest) ServeCommand(name string, p *Peer) ServeState {
	return ServeDeclined
}

func TestListenAndServe(t *testing.T) {
	handler := &ServerTest{}
	_ = ListenAndServe(":1935", handler)
}
