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

	DefaultReadChunkSize  = 128
	DefaultWriteChunkSize = 4096

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
}

func (cs *ChunkStream) abort() {
	cs.hdr = nil
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

func (conn *conn) setState(nc net.Conn, state ConnState) {
	if state > StateServerDone || state < StateServerRecvChallenge {
		panic("internal error")
	}
	conn.state.Store(uint32(state))
	if hook := conn.server.ConnState; hook != nil {
		hook(nc, state)
	}
}

// serve a new connection.
func (conn *conn) serve() {
	if ra := conn.rwc.RemoteAddr(); ra != nil {
		conn.remoteAddr = ra.String()
	}

	err := conn.handshake()
	if err != nil {
		panic(err)
		return
	}

	for {
		msg, err := conn.readMessage()
		if err != nil {
			panic(err)
			return
		}

		sh := serverHandler{conn.server}
		if err = sh.ServeMessage(msg); err != nil {
			panic(err)
			return
		}
	}
}

func (conn *conn) setChunkSize(chunkSize uint32) {
	if conn.chunkSize != chunkSize {
		conn.chunkSize = chunkSize
	}
}

func (conn *conn) readMessage() (*Message, error) {
	msg := conn.NewMessage()
	for {
		err := msg.readChunk()
		if err != nil {
			return nil, err
		}

		if msg.completed() {
			return msg, nil
		}
	}
}

func (conn *conn) NewMessage() *Message {
	if v := messagePool.Get(); v != nil {
		msg := v.(*Message)
		msg.has = 0
		msg.conn = conn
		msg.Chunks = msg.NewChunks()
		return msg
	}
	msg := &Message{
		conn: conn,
	}
	msg.Chunks = msg.NewChunks()
	return msg
}

func (conn *conn) Read(p []byte) (int, error) {
	n, err := conn.bufr.Read(p)

	conn.inBytes += uint32(n)

	if conn.inBytes >= 0xf0000000 {
		conn.inBytes = 0
		conn.inLastAck = 0
	}

	if conn.winAckSize > 0 && conn.inBytes-conn.inLastAck >= conn.winAckSize {
		conn.inLastAck = conn.inBytes
		if err = conn.SendAck(conn.inBytes); err != nil {
			return n, err
		}
	}

	return n, err
}

func (conn *conn) ReadFull(buf []byte) (n int, err error) {
	n, err = io.ReadFull(conn, buf)
	if err != nil || n != len(buf) {
		if err == nil {
			err = errors.New("insufficient bytes were read")
		}
		return n, err
	}
	return n, nil
}

func (conn *conn) SendAck(seq uint32) error {
	msg := conn.NewMessage()
	_ = binary.Write(msg, binary.BigEndian, seq)
	return msg.Send()
}

func (conn *conn) SendAckWinSize(win uint32) error {
	msg := conn.NewMessage()
	_ = binary.Write(msg, binary.BigEndian, win)
	return msg.Send()
}

func (conn *conn) SendSetPeerBandwidth(win uint32, limit uint8) error {
	msg := conn.NewMessage()
	_ = binary.Write(msg, binary.BigEndian, win)
	_ = msg.WriteByte(limit)
	return msg.Send()
}

func (conn *conn) SendSetChunkSize(cs uint32) error {
	msg := conn.NewMessage()
	_ = binary.Write(msg, binary.BigEndian, cs)
	return msg.Send()
}

func (conn *conn) SendOnBWDone() error {
	msg := conn.NewMessage()
	b, _ := amf.Marshal([]any{"onBWDone", 0, nil})
	_, _ = msg.Write(b)
	return msg.Send()
}

func (conn *conn) SendConnectResult(trans uint32, encoding uint32) error {
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
	msg := conn.NewMessage()
	_, _ = msg.Write(b)
	return msg.Send()
}

func (conn *conn) SendReleaseStreamResult(trans uint32) error {
	msg := conn.NewMessage()
	var nullArray []uint32
	b, _ := amf.Marshal([]any{"_result", trans, nil, nullArray})
	_, _ = msg.Write(b)
	return msg.Send()
}

func (conn *conn) SendOnFCPublish(trans uint32) error {
	msg := conn.NewMessage()
	b, _ := amf.Marshal([]any{"onFCPublish", trans, nil})
	_, _ = msg.Write(b)
	return msg.Send()
}

func (conn *conn) SendFCPublishResult(trans uint32) error {
	msg := conn.NewMessage()
	var nullArray []uint32
	b, _ := amf.Marshal([]any{"_result", trans, nil, nullArray})
	_, _ = msg.Write(b)
	return msg.Send()
}

func (conn *conn) SendCreateStreamResult(trans uint32, stream uint32) error {
	msg := conn.NewMessage()
	b, _ := amf.Marshal([]any{"_result", trans, nil, stream})
	_, _ = msg.Write(b)
	return msg.Send()
}
