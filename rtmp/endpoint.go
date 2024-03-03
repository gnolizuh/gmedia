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

type Publisher struct {
}

type Subscriber struct {
}

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

	// conn
	conn *conn
}

func (p *Peer) setReader(reader Reader) {
	p.Reader = reader
}
