package rtmp

import "testing"

type ServerTest struct {

}

func (s *ServerTest) ServeRTMP(c *Client) {

}

func TestListenAndServe(t *testing.T) {
	server := ServerTest{}
	ListenAndServe(":1935", &server)
}
