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

import (
	"sync"
)

const (
	// MaxBasicHeaderSize : Basic Header (1 to 3 bytes)
	MaxBasicHeaderSize = 3

	// MaxMessageHeaderSize : Message Header (0, 3, 7, or 11 bytes)
	MaxMessageHeaderSize = 11

	// MaxExtendHeaderSize : Extended Timestamp (0 or 4 bytes)
	MaxExtendHeaderSize = 4

	// MaxHeaderSize : Chunk header:
	//   max 3  basic header
	// + max 11 message header
	// + max 4  extended header (timestamp)
	MaxHeaderSize = MaxBasicHeaderSize + MaxMessageHeaderSize + MaxExtendHeaderSize

	DefaultReadChunkSize = 128
	DefaultChunkSize     = 4096

	MaxStreamsNum = 32

	DefaultAckWindowSize = 5000000

	DefaultLimitDynamic = 2

	DefaultFMSVersion   = "FMS/3,0,1,123"
	DefaultCapabilities = 31

	DefaultMessageStreamID = 1
)

type MessageHandler func(*Peer) error
type UserMessageHandler func(*Peer) error
type AMFCommandHandler func(*Peer) error

// Header declare.
type Header struct {
	fmt       uint8
	csid      uint32
	timestamp uint32
	mlen      uint32
	typo      MessageType // typo means type, u know why.
	msid      uint32
}

func newHeader(typo MessageType, csid uint32) *Header {
	return &Header{
		typo: typo,
		csid: csid,
	}
}

var headerPool = sync.Pool{
	New: func() any {
		hd := make([]byte, MaxHeaderSize)
		return &hd
	},
}

const (
	AudioChunkStream = iota
	VideoChunkStream
	MaxChunkStream
)

type ChunkStream struct {
	active    bool
	timestamp uint32
	csid      uint32
	dropped   uint32
}

// Stream : RTMP stream declare.
type Stream struct {
	hdr Header
	msg *Message
	len uint32
}
