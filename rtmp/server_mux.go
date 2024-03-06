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
	"errors"
	"fmt"
	amf "github.com/gnolizuh/gamf"
	"log"
	"math"
)

type TypeHandler func(*Message) error
type UserHandler func(*Message) error
type CommandHandler func(*Message) error

func init() {
	regTypeHandlers()
	regUserHandlers()
	regCommandHandlers()
}

// DefaultServeMux is the default [ServeMux] used by [Serve].
var DefaultServeMux = &defaultServeMux

var defaultServeMux ServeMux

type ServeMux struct {
	typeHandlers    []TypeHandler
	userHandlers    []UserHandler
	commandHandlers map[string]CommandHandler
}

func (mux *ServeMux) findTypeHandler(typ MessageType) TypeHandler {
	return defaultServeMux.typeHandlers[typ]
}

// ServeMessage dispatches the message to the handler.
func (mux *ServeMux) ServeMessage(msg *Message) error {
	h := mux.findTypeHandler(msg.Header.MessageTypeId)
	if h == nil {
		return errors.New("handler not found")
	}
	return h(msg)
}

func (mux *ServeMux) serveNull(msg *Message) error {
	return nil
}

// ---------------------------------- Type Messages ---------------------------------- //

func regTypeHandlers() {
	for typ := MessageType(0); typ < MessageTypeMax; typ++ {
		switch typ {
		case MessageTypeSetChunkSize:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveSetChunkSize)
		case MessageTypeAbort:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAbort)
		case MessageTypeAck:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAcknowledgement)
		case MessageTypeUserControl:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveUserControl)
		case MessageTypeWindowAckSize:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveWindowAcknowledgementSize)
		case MessageTypeSetPeerBandwidth:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveSetPeerBandwidth)
		case MessageTypeAudio:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAudio)
		case MessageTypeVideo:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveVideo)
		case MessageTypeAMF3Meta:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAMF3Meta)
		case MessageTypeAMF3Shared:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAMF3Shared)
		case MessageTypeAMF3Cmd:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAMF3Cmd)
		case MessageTypeAMF0Meta:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAMF0Meta)
		case MessageTypeAMF0Shared:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAMF0Shared)
		case MessageTypeAMF0Cmd:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAMF0Cmd)
		case MessageTypeAggregate:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveAggregate)
		default:
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveNull)
		}
	}
}

func (mux *ServeMux) serveSetChunkSize(msg *Message) error {
	chunkSize, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	if chunkSize > MaxChunkSize {
		log.Printf("too big RTMP chunk size:%d", chunkSize)
		return errors.New("too big RTMP chunk size")
	}

	msg.ChunkStream.conn.setChunkSize(chunkSize)

	log.Printf("set chunk size, chunk_size: %d", chunkSize)

	return nil
}

func (mux *ServeMux) serveAbort(msg *Message) error {
	csid, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	stm := msg.ChunkStream.conn.chunkStreams[csid]
	stm.abort()

	log.Printf("abort, csid: %d", csid)

	return nil
}

func (mux *ServeMux) serveAcknowledgement(msg *Message) error {
	seq, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	msg.ChunkStream.conn.outLastAck = seq
	log.Printf("acknowledgement, receive ack seq: %d", seq)

	return nil
}

func (mux *ServeMux) serveUserControl(msg *Message) error {
	evt, err := msg.ReadUInt16()
	if err != nil {
		return err
	}

	umt := UserMessageType(evt)
	if umt >= UserMessageTypeMax {
		return errors.New(fmt.Sprintf("user message type out of range: %d", umt))
	}

	return nil
}

func (mux *ServeMux) serveWindowAcknowledgementSize(msg *Message) error {
	size, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	msg.ChunkStream.conn.winAckSize = size
	log.Printf("window_acknowledgement_size, receive size: %d", size)

	return nil
}

func (mux *ServeMux) serveSetPeerBandwidth(msg *Message) error {
	const (
		LimitTypeHard = iota
		LimitTypeSoft
		LimitTypeDynamic
	)

	bandwidth, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	limit, err := msg.ReadUInt8()
	if err != nil {
		return err
	}

	switch limit {
	case LimitTypeHard:
		msg.ChunkStream.conn.bandwidth = bandwidth
	case LimitTypeSoft:
		msg.ChunkStream.conn.bandwidth = uint32(math.Min(float64(bandwidth), float64(msg.ChunkStream.conn.bandwidth)))
	case LimitTypeDynamic:
		if msg.ChunkStream.conn.lastLimitType == LimitTypeHard {
			msg.ChunkStream.conn.bandwidth = bandwidth
		}
	}

	msg.ChunkStream.conn.lastLimitType = limit
	log.Printf("set peer bandwidth, bandwidth: %d, limit: %d", bandwidth, limit)

	return nil
}

func (mux *ServeMux) serveAudio(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveVideo(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveAMF3Meta(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveAMF3Shared(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveAMF3Cmd(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveAMF0Meta(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveAMF0Shared(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveAMF0Cmd(msg *Message) error {
	var cmd string
	if err := amf.NewDecoder().WithReader(msg).Decode(&cmd); err != nil {
		return err
	}

	if ch, ok := mux.commandHandlers[cmd]; ok {
		return ch(msg)
	}

	return nil
}

func (mux *ServeMux) serveAggregate(msg *Message) error {
	return nil
}

// ---------------------------------- User Control Messages ---------------------------------- //

func regUserHandlers() {
	for typ := UserMessageType(0); typ < UserMessageTypeMax; typ++ {
		switch typ {
		case UserMessageTypeStreamBegin:
			defaultServeMux.userHandlers = append(defaultServeMux.userHandlers, defaultServeMux.serveUserStreamBegin)
		case UserMessageTypeStreamEOF:
			defaultServeMux.userHandlers = append(defaultServeMux.userHandlers, defaultServeMux.serveUserStreamEOF)
		case UserMessageTypeStreamDry:
			defaultServeMux.userHandlers = append(defaultServeMux.userHandlers, defaultServeMux.serveUserStreamEOF)
		case UserMessageTypeStreamSetBufLen:
			defaultServeMux.userHandlers = append(defaultServeMux.userHandlers, defaultServeMux.serveUserSetBufLen)
		case UserMessageTypeStreamIsRecorded:
			defaultServeMux.userHandlers = append(defaultServeMux.userHandlers, defaultServeMux.serveUserIsRecorded)
		case UserMessageTypePingRequest:
			defaultServeMux.userHandlers = append(defaultServeMux.userHandlers, defaultServeMux.serveUserPingRequest)
		case UserMessageTypePingResponse:
			defaultServeMux.userHandlers = append(defaultServeMux.userHandlers, defaultServeMux.serveUserPingResponse)
		default:
			defaultServeMux.userHandlers = append(defaultServeMux.userHandlers, defaultServeMux.serveNull)
		}
	}
}

// serveUserStreamBegin handle UserControlMessage UserMessageTypeStreamBegin
func (mux *ServeMux) serveUserStreamBegin(msg *Message) error {
	msid, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	log.Printf("receive: stream_begin msid=%d", msid)

	return nil
}

// serveUserStreamEOF handle UserControlMessage UserMessageTypeStreamEOF
func (mux *ServeMux) serveUserStreamEOF(msg *Message) error {
	msid, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	log.Printf("receive: stream_eof msid=%d", msid)

	return nil
}

// serveUserStreamDry handle UserControlMessage UserMessageTypeStreamDry
func (mux *ServeMux) serveUserStreamDry(msg *Message) error {
	msid, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	log.Printf("receive: stream_dry msid=%d", msid)

	return nil
}

// serveUserSetBufLen handle UserControlMessage UserMessageTypeStreamSetBufLen
func (mux *ServeMux) serveUserSetBufLen(msg *Message) error {
	msid, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	buflen, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	log.Printf("receive: set_buflen msid=%d buflen=%d", msid, buflen)

	return nil
}

func (mux *ServeMux) serveUserIsRecorded(msg *Message) error {
	msid, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	log.Printf("receive: recorded msid=%d", msid)

	return nil
}

func (mux *ServeMux) serveUserPingRequest(msg *Message) error {
	timestamp, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	log.Printf("receive: ping request timestamp=%d", timestamp)

	// TODO: send ping response.

	return nil
}

func (mux *ServeMux) serveUserPingResponse(msg *Message) error {
	timestamp, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	log.Printf("receive: ping response timestamp=%d", timestamp)

	// TODO: reset next ping request.

	return nil
}

// ---------------------------------- Command Messages ---------------------------------- //

func regCommandHandlers() {
	defaultServeMux.commandHandlers = map[string]CommandHandler{
		"connect":      defaultServeMux.serveConnect,
		"call":         defaultServeMux.serveCall,
		"close":        defaultServeMux.serveClose,
		"createStream": defaultServeMux.serveCreateStream,
		"play":         defaultServeMux.servePlay,
		"play2":        defaultServeMux.servePlay2,
		"deleteStream": defaultServeMux.serveDeleteStream,
		"closeStream":  defaultServeMux.serveCloseStream,
		"receiveAudio": defaultServeMux.serveReceiveAudio,
		"receiveVideo": defaultServeMux.serveReceiveVideo,
		"publish":      defaultServeMux.servePublish,
		"seek":         defaultServeMux.serveSeek,
		"pause":        defaultServeMux.servePause,
	}
}

func (mux *ServeMux) serveConnect(msg *Message) error {
	type Object struct {
		App            string
		FlashVer       string
		SwfURL         string
		TcURL          string
		AudioCodecs    uint32
		VideoCodecs    uint32
		PageUrl        string
		ObjectEncoding uint32
	}

	var transactionID uint32
	err := amf.NewDecoder().WithReader(msg).Decode(&transactionID)
	if err != nil {
		return err
	}

	if transactionID != 1 {
		return errors.New(fmt.Sprintf("unexpected transaction ID: %d", transactionID))
	}

	var obj Object
	err = amf.NewDecoder().WithReader(msg).Decode(&obj)
	if err != nil {
		log.Println(err)
		return err
	}

	if err := msg.ChunkStream.conn.SendAckWinSize(DefaultAckWindowSize); err != nil {
		return err
	}
	if err := msg.ChunkStream.conn.SendSetPeerBandwidth(DefaultAckWindowSize, DefaultLimitDynamic); err != nil {
		return err
	}
	if err := msg.ChunkStream.conn.SendSetChunkSize(DefaultChunkSize); err != nil {
		return err
	}
	if err := msg.ChunkStream.conn.SendOnBWDone(); err != nil {
		return err
	}
	if err := msg.ChunkStream.conn.SendConnectResult(transactionID, obj.ObjectEncoding); err != nil {
		return err
	}

	return nil
}

func (mux *ServeMux) serveCall(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveClose(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveCreateStream(msg *Message) error {
	var transactionID uint32
	err := amf.NewDecoder().WithReader(msg).Decode(&transactionID)
	if err != nil {
		return err
	}

	if err := msg.ChunkStream.conn.SendCreateStreamResult(transactionID, DefaultMessageStreamID); err != nil {
		return err
	}

	return nil
}

func (mux *ServeMux) serveCloseStream(msg *Message) error {
	var stream float64
	err := amf.NewDecoder().WithReader(msg).Decode(&stream)
	if err != nil {
		return err
	}

	log.Println(stream)

	return nil
}

func (mux *ServeMux) serveReceiveAudio(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveReceiveVideo(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveDeleteStream(msg *Message) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(msg).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var stream float64
	err = amf.NewDecoder().WithReader(msg).Decode(&stream)
	if err != nil {
		return err
	}

	log.Println(stream)

	return nil
}

func (mux *ServeMux) servePublish(msg *Message) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(msg).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var name, typo string
	err = amf.NewDecoder().WithReader(msg).Decode(&name, &typo)
	if err != nil {
		return err
	}

	log.Println(name, typo)

	return nil
}

func (mux *ServeMux) servePlay(msg *Message) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(msg).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var name string
	err = amf.NewDecoder().WithReader(msg).Decode(&name)
	if err != nil {
		return err
	}

	log.Println(name)

	var start, duration float64
	var reset bool
	err = amf.NewDecoder().WithReader(msg).Decode(&start, &duration, &reset)
	if err != nil {
		return err
	}

	return nil
}

func (mux *ServeMux) servePlay2(msg *Message) error {
	type Object struct {
		Start      float64
		StreamName string
	}

	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(msg).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var obj Object
	err = amf.NewDecoder().WithReader(msg).Decode(&obj)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(obj)

	return nil
}

func (mux *ServeMux) serveSeek(msg *Message) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(msg).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var offset float64
	err = amf.NewDecoder().WithReader(msg).Decode(&offset)
	if err != nil {
		return err
	}

	log.Println(offset)

	return nil
}

func (mux *ServeMux) servePause(msg *Message) error {
	var transactionID uint32
	var null []uint32
	err := amf.NewDecoder().WithReader(msg).Decode(&transactionID, &null)
	if err != nil {
		return err
	}

	var pause bool
	var position float64
	err = amf.NewDecoder().WithReader(msg).Decode(&pause, &position)
	if err != nil {
		return err
	}

	return nil
}
