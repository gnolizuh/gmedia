package rtmp

type Handler interface {

}

type ServerHandler struct {

}

// default message/amf handler on server side.
var defaultServerHandler = ServerHandler{}
