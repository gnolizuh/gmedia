package rtmp

import (
	"fmt"
	"log"
	"errors"
	"encoding/binary"
	"io"
	"github.com/gnolizuh/rtmp/amf"
)

type MyHandler interface {
	Handle (*Stream) error
}

func readUint32(r io.Reader) (uint32, error) {
	var b uint32
	err := binary.Read(r, binary.BigEndian, &b)
	if err != nil {
		return 0, err
	}

	return b, nil
}

func readUint16(r io.Reader) (uint16, error) {
	var b uint16
	err := binary.Read(r, binary.BigEndian, &b)
	if err != nil {
		return 0, err
	}

	return b, nil
}

func readUint8(r io.ByteReader) (uint8, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}

	return uint8(b), nil
}

type ServerHandler struct {
	conn *Conn

	messageHandlers     [MessageMax]MessageHandler
	userMessageHandlers [UserMessageMax]UserMessageHandler
	amfHandlers         map[string]AMFCommandHandler
}

func newServerHandler(conn *Conn) MyHandler {
	h := &ServerHandler{
		conn: conn,
	}

	h.messageHandlers[MessageSetChunkSize] = h.onSetChunkSize
	h.messageHandlers[MessageAbort] = h.onAbort
	h.messageHandlers[MessageAck] = h.onAck
	h.messageHandlers[MessageUserControl] = h.onUserControl
	h.messageHandlers[MessageWindowAckSize] = h.onWinAckSize
	h.messageHandlers[MessageSetPeerBandwidth] = h.onSetPeerBandwidth
	h.messageHandlers[MessageEdge] = h.onEdge
	h.messageHandlers[MessageAudio] = h.onAudio
	h.messageHandlers[MessageVideo] = h.onVideo
	h.messageHandlers[MessageAmf3Meta] = h.onAmf3Meta
	h.messageHandlers[MessageAmf3Shared] = h.onAmf3Shared
	h.messageHandlers[MessageAmf3Cmd] = h.onAmf3Cmd
	h.messageHandlers[MessageAmf0Meta] = h.onAmf0Meta
	h.messageHandlers[MessageAmf0Shared] = h.onAmf0Shared
	h.messageHandlers[MessageAmf0Cmd] = h.onAmf0Cmd
	h.messageHandlers[MessageAggregate] = h.onAggregate

	h.userMessageHandlers[UserMessageStreamBegin] = h.onUserStreamBegin
	h.userMessageHandlers[UserMessageStreamEOF] = h.onUserStreamEOF
	h.userMessageHandlers[UserMessageStreamDry] = h.onUserStreamDry
	h.userMessageHandlers[UserMessageStreamSetBufLen] = h.onUserSetBufLen
	h.userMessageHandlers[UserMessageStreamIsRecorded] = h.onUserIsRecorded
	h.userMessageHandlers[UserMessagePingRequest] = h.onUserPingRequest
	h.userMessageHandlers[UserMessagePingResponse] = h.onUserPingResponse

	h.amfHandlers = make(map[string]AMFCommandHandler)
	h.amfHandlers["connect"] = h.onCmdConnect
	h.amfHandlers["releaseStream"] = h.onCmdReleaseStream
	h.amfHandlers["createStream"] = h.onCmdCreateStream
	h.amfHandlers["closeStream"] = h.onCmdCloseStream
	h.amfHandlers["deleteStream"] = h.onCmdDeleteStream
	h.amfHandlers["FCPublish"] = h.onCmdFCPublish
	h.amfHandlers["publish"] = h.onCmdPublish
	h.amfHandlers["play"] = h.onCmdPlay
	h.amfHandlers["play2"] = h.onCmdPlay2
	h.amfHandlers["seek"] = h.onCmdSeek
	h.amfHandlers["pause"] = h.onCmdPause
	h.amfHandlers["pauseraw"] = h.onCmdPause

	return h
}

func (sh *ServerHandler) Handle(stm *Stream) error {
	h := sh.messageHandlers[stm.hdr.typo]
	if h != nil {
		return h(stm.msg)
	}

	return errors.New(fmt.Sprintf("RTMP message type %d unknown", stm.hdr.typo))
}

func (sh *ServerHandler) onUserStreamBegin(msg *Message) error {
	msid, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("receive: stream_begin msid=%d", msid)

	return nil
}

func (sh *ServerHandler) onUserStreamEOF(msg *Message) error {
	msid, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("receive: stream_eof msid=%d", msid)

	return nil
}

func (sh *ServerHandler) onUserStreamDry(msg *Message) error {
	msid, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("receive: stream_dry msid=%d", msid)

	return nil
}

func (sh *ServerHandler) onUserSetBufLen(msg *Message) error {
	msid, err := readUint32(msg)
	if err != nil {
		return err
	}

	buflen, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("receive: set_buflen msid=%d buflen=%d", msid, buflen)

	return nil
}

func (sh *ServerHandler) onUserIsRecorded(msg *Message) error {
	msid, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("receive: recorded msid=%d", msid)

	return nil
}

func (sh *ServerHandler) onUserPingRequest(msg *Message) error {
	timestamp, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("receive: ping request timestamp=%d", timestamp)

	// TODO: send ping response.

	return nil
}

func (sh *ServerHandler) onUserPingResponse(msg *Message) error {
	timestamp, err := readUint32(msg)
	if err != nil {
		return err
	}

	log.Printf("receive: ping response timestamp=%d", timestamp)

	// TODO: reset next ping request.

	return nil
}

func (sh *ServerHandler) onCmdConnect(msg *Message) error {
	var cmd ConnectCommand
	err := amf.DecodeWithReader(msg, &cmd.Trans, &cmd.Object)
	if err != nil {
		log.Println(err)
		return err
	}

	// TODO: send AckSize and _result
	log.Println(cmd)

	return nil
}

func (sh *ServerHandler) onCmdReleaseStream(msg *Message) error {
	var cmd ReleaseStreamCommand
	var null int
	err := amf.DecodeWithReader(msg, &cmd.Trans, &null, &cmd.Name)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}

func (sh *ServerHandler) onCmdCreateStream(msg *Message) error {
	var cmd CreateStreamCommand
	err := amf.DecodeWithReader(msg, &cmd.Trans)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}

func (sh *ServerHandler) onCmdCloseStream(msg *Message) error {
	var cmd CloseStreamCommand
	err := amf.DecodeWithReader(msg, &cmd.Stream)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}

func (sh *ServerHandler) onCmdDeleteStream(msg *Message) error {
	var cmd DeleteStreamCommand
	var null int
	err := amf.DecodeWithReader(msg, &null, &null, &cmd.Stream)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}

func (sh *ServerHandler) onCmdFCPublish(msg *Message) error {
	var cmd FCPublishCommand
	var null int
	err := amf.DecodeWithReader(msg, &cmd.Trans, &null, &cmd.Name)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}

func (sh *ServerHandler) onCmdPublish(msg *Message) error {
	var cmd PublishCommand
	var null int
	err := amf.DecodeWithReader(msg, &null, &null, &cmd.Name, &cmd.Type)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}

func (sh *ServerHandler) onCmdPlay(msg *Message) error {
	var cmd PlayCommand
	var null int
	err := amf.DecodeWithReader(msg, &null, &null, &cmd.Name, &cmd.Start, &cmd.Duration, &cmd.Reset)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}

func (sh *ServerHandler) onCmdPlay2(msg *Message) error {
	var cmd Play2Command
	var null int
	err := amf.DecodeWithReader(msg, &null, &null, &cmd)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}

func (sh *ServerHandler) onCmdSeek(msg *Message) error {
	var cmd SeekCommand
	var null int
	err := amf.DecodeWithReader(msg, &null, &null, &cmd.Offset)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}

func (sh *ServerHandler) onCmdPause(msg *Message) error {
	var cmd PauseCommand
	var null int
	err := amf.DecodeWithReader(msg, &null, &null, &cmd.Pause, &cmd.Position)
	if err != nil {
		log.Println(err)
		return err
	}

	log.Println(cmd)
	return nil
}
