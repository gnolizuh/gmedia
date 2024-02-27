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

// DefaultServeMux is the default [ServeMux] used by [Serve].
var DefaultServeMux = &defaultServeMux

var defaultServeMux ServeMux

type ServeMux struct {
	typeHandlers []TypeHandler
}

func init() {
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
			defaultServeMux.typeHandlers = append(defaultServeMux.typeHandlers, defaultServeMux.serveDefault)
		}
	}
}

type TypeHandler func(*Message) error

func (mux *ServeMux) findHandler(typ MessageType) TypeHandler {
	return defaultServeMux.typeHandlers[typ]
}

// ServeMessage dispatches the message to the handler.
func (mux *ServeMux) ServeMessage(msg *Message) error {
	h := mux.findHandler(msg.Header.MessageTypeId)
	if h == nil {
		return errors.New("handler not found")
	}
	return h(msg)
}

func (mux *ServeMux) serveSetChunkSize(msg *Message) error {
	cs, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	if cs > MaxChunkSize {
		log.Printf("too big RTMP chunk size:%d", cs)
		return errors.New("too big RTMP chunk size")
	}

	msg.conn.setChunkSize(cs)

	log.Printf("set chunk size, chunk_size: %d", cs)

	return nil
}

func (mux *ServeMux) serveAbort(msg *Message) error {
	csid, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	stm := msg.conn.chunkStreams[csid]
	stm.abort()

	log.Printf("abort, csid: %d", csid)

	return nil
}

func (mux *ServeMux) serveAcknowledgement(msg *Message) error {
	seq, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	msg.conn.outLastAck = seq
	log.Printf("acknowledgement, receive ack seq: %d", seq)

	return nil
}

func (mux *ServeMux) serveUserControl(msg *Message) error {
	evt, err := msg.ReadUInt16()
	if err != nil {
		return err
	}

	umt := UserMessageType(evt)
	if umt >= UserMessageMax {
		return errors.New(fmt.Sprintf("user message type out of range: %d", umt))
	}

	return nil
}

func (mux *ServeMux) serveWindowAcknowledgementSize(msg *Message) error {
	size, err := msg.ReadUInt32()
	if err != nil {
		return err
	}

	msg.conn.winAckSize = size
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
		msg.conn.bandwidth = bandwidth
	case LimitTypeSoft:
		msg.conn.bandwidth = uint32(math.Min(float64(bandwidth), float64(msg.conn.bandwidth)))
	case LimitTypeDynamic:
		if msg.conn.lastLimitType == LimitTypeHard {
			msg.conn.bandwidth = bandwidth
		}
	}

	msg.conn.lastLimitType = limit
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
	var name string
	err := amf.NewDecoder().WithReader(msg.conn.bufr).Decode(&name)
	if err != nil {
		log.Println(err)
		return err
	}
	return nil
}

func (mux *ServeMux) serveAggregate(msg *Message) error {
	return nil
}

func (mux *ServeMux) serveDefault(msg *Message) error {
	return nil
}
