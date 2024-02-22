package rtmp

import "testing"

type ServerTest struct {
}

func (st *ServerTest) ServeRTMP(m *Message) {
	return
}

func TestListenAndServe(t *testing.T) {
	handler := &ServerTest{}
	_ = ListenAndServe(":1935", handler)
}
