package amf

const (
	NumberMarker      = 0x00
	BooleanMarker     = 0x01
	StringMarker      = 0x02
	ObjectMarker      = 0x03
	MovieClipMarker   = 0x04
	NullMarker        = 0x05
	ArrayNullMarker   = 0x06
	ReferenceMarker   = 0x07
	ECMAArrayMarker   = 0x08
	ObjectEndMarker   = 0x09
	StrictArrayMarker = 0x0a
	DateMarker        = 0x0b
	LongStringMarker  = 0x0c
	UnsupportedMarker = 0x0d
	RecordSetMarker   = 0x0e
	XMLDocumentMarker = 0x0f
	TypedObjectMarker = 0x10
)

const (
	LongStringSize = 0xffff
)