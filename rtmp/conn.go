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
	// MaxBasicHeaderSize : Max Basic Header (1 to 3 bytes)
	MaxBasicHeaderSize = 3

	// MaxMessageHeaderSize : Max Message Header (0, 3, 7, or 11 bytes)
	MaxMessageHeaderSize = 11

	// MaxExtendHeaderSize : Max Extended Timestamp (0 or 4 bytes)
	MaxExtendHeaderSize = 4

	// MaxHeaderSize :
	//   max 3  basic header
	// + max 11 message header
	// + max 4  extended header (timestamp)
	MaxHeaderSize = MaxBasicHeaderSize + MaxMessageHeaderSize + MaxExtendHeaderSize

	DefaultRecvChunkSize = 128
	DefaultSendChunkSize = 4096

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
	conn    *conn
	prevhdr *Header
	hdr     Header
}

func (cs *ChunkStream) abort() {
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
	chunkStreams []*ChunkStream

	// recv and send chunk size
	recvChunkSize uint32
	sendChunkSize uint32

	// ack window size
	winAckSize uint32

	inBytes    uint32
	inLastAck  uint32
	outLastAck uint32

	lastLimitType uint8
	bandwidth     uint32

	silent bool

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

func (conn *conn) setRecvChunkSize(recvChunkSize uint32) {
	if conn.recvChunkSize != recvChunkSize {
		conn.recvChunkSize = recvChunkSize
	}
}

func (conn *conn) setSendChunkSize(sendChunkSize uint32) {
	if conn.sendChunkSize != sendChunkSize {
		conn.sendChunkSize = sendChunkSize
	}
}

func (conn *conn) readMessage() (*Message, error) {
	msg := conn.NewMessage(conn.recvChunkSize)
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

func (conn *conn) NewMessage(chunkSize uint32) *Message {
	if v := messagePool.Get(); v != nil {
		msg := v.(*Message)
		msg.has = 0
		msg.conn = conn
		msg.Chunks = msg.NewChunks(chunkSize)
		return msg
	}
	msg := &Message{
		conn: conn,
	}
	msg.Chunks = msg.NewChunks(chunkSize)
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
	msg := conn.NewMessage(conn.sendChunkSize)
	_ = binary.Write(msg, binary.BigEndian, seq)
	return msg.Send()
}

func (conn *conn) SendAckWinSize(win uint32) error {
	msg := conn.NewMessage(conn.sendChunkSize)
	msg.hdr.MessageTypeId = MessageTypeWinAckSize
	msg.hdr.ChunkStreamId = ChunkStreamIdDefault
	_ = binary.Write(msg, binary.BigEndian, win)
	return msg.Send()
}

func (conn *conn) SendSetPeerBandwidth(win uint32, limit uint8) error {
	msg := conn.NewMessage(conn.sendChunkSize)
	msg.hdr.MessageTypeId = MessageTypeSetPeerBandwidth
	msg.hdr.ChunkStreamId = ChunkStreamIdDefault
	_ = binary.Write(msg, binary.BigEndian, win)
	_ = msg.WriteByte(limit)
	return msg.Send()
}

func (conn *conn) SendSetChunkSize(cs uint32) error {
	msg := conn.NewMessage(conn.sendChunkSize)
	msg.hdr.MessageTypeId = MessageTypeSetChunkSize
	msg.hdr.ChunkStreamId = ChunkStreamIdDefault
	conn.setSendChunkSize(cs)
	_ = binary.Write(msg, binary.BigEndian, cs)
	return msg.Send()
}

func (conn *conn) SendConnectResult(tid uint32, encoding uint32) error {
	properties := make(map[string]any)
	properties["fmsVer"] = DefaultFMSVersion
	properties["capabilities"] = DefaultCapabilities

	information := make(map[string]any)
	information["level"] = "status"
	information["code"] = "NetConnection.Connect.Success"
	information["description"] = "Connection succeeded."
	information["objectEncoding"] = encoding

	b, _ := amf.Marshal("_result", tid, properties, information)
	msg := conn.NewMessage(conn.sendChunkSize)
	msg.hdr.MessageTypeId = MessageTypeAMF0Command
	msg.hdr.ChunkStreamId = ChunkStreamIdAMFInitial
	_, _ = msg.Write(b)
	return msg.Send()
}

func (conn *conn) SendCreateStreamResult(tid uint32, stream uint32) error {
	msg := conn.NewMessage(conn.sendChunkSize)
	msg.hdr.MessageTypeId = MessageTypeAMF0Command
	msg.hdr.ChunkStreamId = ChunkStreamIdAMFInitial
	b, _ := amf.Marshal("_result", tid, nil, stream)
	_, _ = msg.Write(b)
	return msg.Send()
}

func (conn *conn) SendStatus(code, level, desc string) error {
	description := make(map[string]any)
	description["code"] = code
	description["level"] = level
	description["description"] = desc

	msg := conn.NewMessage(conn.sendChunkSize)
	msg.hdr.MessageTypeId = MessageTypeAMF0Command
	msg.hdr.ChunkStreamId = ChunkStreamIdAMFInitial
	b, _ := amf.Marshal("onStatus", 0, nil, description)
	_, _ = msg.Write(b)
	return msg.Send()
}
