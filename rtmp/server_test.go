package rtmp

import "testing"

type ServerTest struct {
}

func (st *ServerTest) ServeMessage(m *Message) error {
	return nil
}

func TestListenAndServe(t *testing.T) {
	handler := &ServerTest{}
	_ = ListenAndServe(":1935", handler)
}
