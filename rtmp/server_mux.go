//
// Copyright [2024] [https://github.com/gnolizuh]
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package rtmp

// DefaultServeMux is the default [ServeMux] used by [Serve].
var DefaultServeMux = &defaultServeMux

var defaultServeMux ServeMux

type ServeMux struct{}

func (mux *ServeMux) findHandler(m *Message) Handler {
	switch m.Header.Type {
	case MessageTypeSetChunkSize:
	case MessageTypeAbort:
	case MessageTypeAck:
	case MessageTypeUserControl:
	case MessageTypeWindowAckSize:
	case MessageTypeSetPeerBandwidth:
	case MessageTypeAudio:
	case MessageTypeVideo:
	case MessageTypeAMF3Meta, MessageTypeAMF3Shared, MessageTypeAMF3Cmd:
	case MessageTypeAMF0Meta, MessageTypeAMF0Shared, MessageTypeAMF0Cmd:
	case MessageTypeAggregate:
	}
	return nil
}

// ServeRTMP dispatches the message to the handler.
func (mux *ServeMux) ServeRTMP(m *Message) {
	h := mux.findHandler(m)
	h.ServeRTMP(m)
}
