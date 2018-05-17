package rtmp

import "testing"

type ServerTest struct {

}

func TestListenAndServe(t *testing.T) {
	ListenAndServe(":1935")
}
