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
	"bufio"
	"encoding/binary"
	"errors"
	amf "github.com/gnolizuh/gamf"
	"io"
	"math"
	"net"
	"sync/atomic"
)

const (
	// MaxBasicHeaderSize : Basic Header (1 to 3 bytes)
	MaxBasicHeaderSize = 3

	// MaxMessageHeaderSize : Message Header (0, 3, 7, or 11 bytes)
	MaxMessageHeaderSize = 11

	// MaxExtendHeaderSize : Extended Timestamp (0 or 4 bytes)
	MaxExtendHeaderSize = 4

	// MaxHeaderBufferSize : Chunk header:
	//   max 3  basic header
	// + max 11 message header
	// + max 4  extended header (timestamp)
	MaxHeaderBufferSize = MaxBasicHeaderSize + MaxMessageHeaderSize + MaxExtendHeaderSize

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

const (
	AudioChunkStream = iota
	VideoChunkStream
	MaxChunkStream
)

// ChunkStream : RTMP stream declare.
type ChunkStream struct {
	conn *conn
	hdr  *Header
	read uint32
}

func (cs *ChunkStream) abort() {
	cs.hdr = nil
	cs.read = 0
}

// The conn type represents a RTMP connection.
type conn struct {
	// server is the server on which the connection arrived.
	// Immutable; never nil.
	server *Server

	// rwc is the underlying network connection.
	rwc net.Conn

	// remoteAddr is rwc.RemoteAddr().String(). It is not populated synchronously
	// inside the Listener's Accept goroutine, as some implementations block.
	remoteAddr string

	// Connection incoming time(microsecond) and incoming time from remote peer.
	epoch         uint32
	incomingEpoch uint32

	// State and digest be used in RTMP handshake.
	state  atomic.Uint32
	digest []byte

	// Read and write buffer.
	bufr *bufio.Reader
	bufw *bufio.Writer

	// chunkStreams hold chunk stream tunnel.
	chunkStreams []ChunkStream

	// chunk message
	chunkSize uint32

	// ack window size
	winAckSize uint32

	inBytes    uint32
	inLastAck  uint32
	outLastAck uint32

	lastLimitType uint8
	bandwidth     uint32

	// peer wrapper
	peer Peer
}

func (c *conn) setState(nc net.Conn, state ConnState) {
	if state > StateServerDone || state < StateServerRecvChallenge {
		panic("internal error")
	}
	c.state.Store(uint32(state))
	if hook := c.server.ConnState; hook != nil {
		hook(nc, state)
	}
}

// serve a new connection.
func (c *conn) serve() {
	if ra := c.rwc.RemoteAddr(); ra != nil {
		c.remoteAddr = ra.String()
	}

	err := c.handshake()
	if err != nil {
		panic(err)
		return
	}

	for {
		cs, msg, err := c.readMessage()
		if err != nil {
			panic(err)
			return
		}

		sh := serverHandler{c.server}
		if err = sh.ServeMessage(cs, msg); err != nil {
			panic(err)
			return
		}
	}
}

func (c *conn) setChunkSize(chunkSize uint32) {
	if c.chunkSize != chunkSize {
		c.chunkSize = chunkSize
	}
}

func (c *conn) Read(p []byte) (int, error) {
	n, err := c.bufr.Read(p)

	c.inBytes += uint32(n)

	if c.inBytes >= 0xf0000000 {
		c.inBytes = 0
		c.inLastAck = 0
	}

	if c.winAckSize > 0 && c.inBytes-c.inLastAck >= c.winAckSize {
		c.inLastAck = c.inBytes
		if err = c.SendAck(c.inBytes); err != nil {
			return n, err
		}
	}

	return n, err
}

func (c *conn) ReadFull(buf []byte) (n int, err error) {
	n, err = io.ReadFull(c, buf)
	if err != nil || n != len(buf) {
		if err == nil {
			err = errors.New("insufficient bytes were read")
		}
		return n, err
	}
	return n, nil
}

func (c *conn) readChunk() (*ChunkStream, *Chunk, error) {
	hdr := Header{}
	err := readHeader(c, &hdr)
	if err != nil {
		return nil, nil, err
	}

	// indicate timestamp whether is absolute or relate.
	stm := &c.chunkStreams[hdr.ChunkStreamId]
	if stm.hdr == nil {
		stm.hdr = newHeader()
	}

	stm.hdr.Format = hdr.Format
	stm.hdr.ChunkStreamId = hdr.ChunkStreamId
	switch hdr.Format {
	case 0:
		stm.hdr.Timestamp = hdr.Timestamp
		stm.hdr.MessageLength = hdr.MessageLength
		stm.hdr.MessageTypeId = hdr.MessageTypeId
		stm.hdr.MessageStreamId = hdr.MessageStreamId
	case 1:
		stm.hdr.Timestamp += hdr.Timestamp
		stm.hdr.MessageLength = hdr.MessageLength
		stm.hdr.MessageTypeId = hdr.MessageTypeId
	case 2:
		stm.hdr.Timestamp += hdr.Timestamp
	case 3:
		// see https://rtmp.veriskope.com/docs/spec/#53124-type-3
		if stm.hdr != nil && stm.hdr.Format == 0 {
			stm.hdr.Timestamp += stm.hdr.Timestamp
		}

		// read extend timestamp
		if stm.hdr.Timestamp == 0x00ffffff {
			buf := make([]byte, 4)
			_, err := c.ReadFull(buf)
			if err != nil {
				return nil, nil, err
			}
			stm.hdr.Timestamp = binary.BigEndian.Uint32(buf)
		}
	default:
		panic("unknown format type")
	}

	// calculate bytes needed.
	need := uint32(math.Min(float64(stm.hdr.MessageLength-stm.read), float64(c.chunkSize)))

	chunk := newChunk(c.chunkSize)
	reader := ChunkReader{conn: c, chunk: chunk}
	if _, err = reader.ReadN(need); err != nil {
		return nil, nil, err
	}
	stm.read += need

	return stm, chunk, nil
}

func (c *conn) readMessage() (*ChunkStream, *Message, error) {
	msg := newMessage()
	for {
		cs, ck, err := c.readChunk()
		if err != nil {
			return nil, nil, err
		}
		msg.Header = cs.hdr
		msg.append(ck)
		if msg.completed() {
			return cs, msg, nil
		}
	}
}

func (c *conn) SendAck(seq uint32) error {
	msg := newMessage()
	msg.alloc(4)

	_ = binary.Write(msg, binary.BigEndian, seq)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendAckWinSize(win uint32) error {
	msg := newMessage()
	msg.alloc(4)

	_ = binary.Write(msg, binary.BigEndian, win)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendSetPeerBandwidth(win uint32, limit uint8) error {
	msg := newMessage()
	msg.alloc(5)

	_ = binary.Write(msg, binary.BigEndian, win)
	_ = msg.WriteByte(limit)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendSetChunkSize(cs uint32) error {
	msg := newMessage()

	msg.alloc(4)
	_ = binary.Write(msg, binary.BigEndian, cs)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendOnBWDone() error {
	msg := newMessage()
	b, _ := amf.Marshal([]any{"onBWDone", 0, nil})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendConnectResult(trans uint32, encoding uint32) error {
	msg := newMessage()

	type Object struct {
		FMSVer       string `amf:"fmsVer"`
		Capabilities uint32 `amf:"capabilities"`
	}

	type Info struct {
		Level          string `amf:"level"`
		Code           string `amf:"code"`
		Description    string `amf:"description"`
		ObjectEncoding uint32 `amf:"objectEncoding"`
	}

	obj := Object{
		FMSVer:       DefaultFMSVersion,
		Capabilities: DefaultCapabilities,
	}
	inf := Info{
		Level:          "status",
		Code:           "NetConnection.Connect.Success",
		Description:    "Connection succeeded.",
		ObjectEncoding: encoding,
	}
	b, _ := amf.Marshal([]any{"_result", trans, obj, inf})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendReleaseStreamResult(trans uint32) error {
	msg := newMessage()
	var nullArray []uint32
	b, _ := amf.Marshal([]any{"_result", trans, nil, nullArray})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendOnFCPublish(trans uint32) error {
	msg := newMessage()
	b, _ := amf.Marshal([]any{"onFCPublish", trans, nil})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendFCPublishResult(trans uint32) error {
	msg := newMessage()
	var nullArray []uint32
	b, _ := amf.Marshal([]any{"_result", trans, nil, nullArray})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}

func (c *conn) SendCreateStreamResult(trans uint32, stream uint32) error {
	msg := newMessage()
	b, _ := amf.Marshal([]any{"_result", trans, nil, stream})
	msg.alloc(uint32(len(b)))
	_, _ = msg.Write(b)
	_ = msg.prepare(nil)

	return msg.Send(c.rwc)
}
