package rtmp

import "testing"

type ServerTest struct {

}

func (st *ServerTest) ServeNew(peer *Peer) ServeState {
	return ServeDeclined
}

func (st *ServerTest) ServeMessage(typo MessageType, peer *Peer) ServeState {
	return ServeDeclined
}

func (st *ServerTest) ServeUserMessage(typo UserMessageType, peer *Peer) ServeState {
	return ServeDeclined
}

func (st *ServerTest) ServeCommand(name CommandName, peer *Peer) ServeState {
	return ServeDeclined
}

func TestListenAndServe(t *testing.T) {
	handler := &ServerTest{}
	ListenAndServe(":1935", handler)
}
