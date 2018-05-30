package amf

import (
	"runtime"
	"reflect"
	"bytes"
	"errors"
	"strconv"
	"encoding/binary"
	"encoding/base64"
)

type Number string

func (n Number) String() string { return string(n) }

func (n Number) Float64() (float64, error) {
	return strconv.ParseFloat(string(n), 64)
}

func (n Number) Int64() (int64, error) {
	return strconv.ParseInt(string(n), 10, 64)
}

var numberType = reflect.TypeOf(Number(""))

type DecodeTypeError struct {
	Value  string
	Type   reflect.Type
	Struct string
	Field  string
}

func (e *DecodeTypeError) Error() string {
	if e.Struct != "" || e.Field != "" {
		return "amf: cannot decode " + e.Value + " into Go struct field " + e.Struct + "." + e.Field + " of type " + e.Type.String()
	}
	return "amf: cannot decode " + e.Value + " into Go value of type " + e.Type.String()
}

type decodeState struct {
	r  *bytes.Reader
}

func (d *decodeState) indirect(v reflect.Value) reflect.Value {
	// If v is a named type and is addressable,
	// start with its address, so that if the type has pointer methods,
	// we find them.
	if v.Kind() != reflect.Ptr && v.Type().Name() != "" && v.CanAddr() {
		v = v.Addr()
	}
	for {
		// Load value from interface, but only if the result will be
		// usefully addressable.
		if v.Kind() == reflect.Interface && !v.IsNil() {
			e := v.Elem()
			if e.Kind() == reflect.Ptr && !e.IsNil() {
				v = e
				continue
			}
		}
		if v.Kind() != reflect.Ptr {
			break
		}
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	return v
}

func (d *decodeState) error(err error) {
	panic(err)
}

func (d *decodeState) decodeMarker() (byte, error) {
	return d.r.ReadByte()
}

func (d *decodeState) floatInterface() interface{} {
	var f float64
	err := binary.Read(d.r, binary.BigEndian, &f)
	if err != nil {
		d.error(err)
	}
	return f
}

func (d *decodeState) decodeFloat64(v reflect.Value) error {
	var f float64
	err := binary.Read(d.r, binary.BigEndian, &f)
	if err != nil {
		return err
	}

	v = d.indirect(v)

	switch v.Kind() {
	default:
		if v.Kind() == reflect.String && v.Type() == numberType {
			v.SetString(strconv.FormatFloat(f, 'f', 6, 64))
			break
		}
		return errors.New("unexpected kind (" + v.Type().String() + ") when decoding float64")
	case reflect.Float32, reflect.Float64:
		v.SetFloat(f)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(f))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(f))
	case reflect.Interface:
		v.Set(reflect.ValueOf(f))
	}

	return nil
}

func (d *decodeState) boolInterface() interface{} {
	bt, err := d.r.ReadByte()
	if err != nil {
		d.error(err)
	}

	return bt != 0
}

func (d *decodeState) decodeBool(v reflect.Value) error {
	bt, err := d.r.ReadByte()
	if err != nil {
		return err
	}

	b := bt != 0

	v = d.indirect(v)

	switch v.Kind() {
	default:
		return errors.New("unexpected kind (" + v.Type().String() + ") when decoding bool")
	case reflect.Bool:
		v.SetBool(b)
	case reflect.Interface:
		v.Set(reflect.ValueOf(b))
	}

	return nil
}

func (d *decodeState) decodeUint() (uint16, error) {
	var u uint16
	err := binary.Read(d.r, binary.BigEndian, &u)
	if err != nil {
		return 0, err
	}
	return u, nil
}

func (d *decodeState) decodeUint32() (uint32, error) {
	var u uint32
	err := binary.Read(d.r, binary.BigEndian, &u)
	if err != nil {
		return 0, err
	}
	return u, nil
}

func (d *decodeState) decodeObjectName() (string, error) {
	u, err := d.decodeUint()
	if err != nil {
		return "", err
	}

	s := make([]byte, u)
	_, err = d.r.Read(s)
	if err != nil {
		return "", err
	}

	return string(s), nil
}

func (d *decodeState) stringInterface() interface{} {
	u, err := d.decodeUint()
	if err != nil {
		d.error(err)
		return nil
	}

	s := make([]byte, u)
	_, err = d.r.Read(s)
	if err != nil {
		d.error(err)
		return nil
	}

	return string(s)
}

func (d *decodeState) decodeString(v reflect.Value) error {
	u, err := d.decodeUint()
	if err != nil {
		return err
	}

	s := make([]byte, u)
	_, err = d.r.Read(s)
	if err != nil {
		return err
	}

	v = d.indirect(v)

	switch v.Kind() {
	default:
		return errors.New("unexpected type: " + v.Type().String() + " decoding string")
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.Uint8 {
			return &DecodeTypeError{Value: "string", Type: v.Type()}
		}
		b := make([]byte, base64.StdEncoding.DecodedLen(len(s)))
		n, err := base64.StdEncoding.Decode(b, s)
		if err != nil {
			return err
		}
		v.SetBytes(b[:n])
	case reflect.String:
		v.SetString(string(s))
	case reflect.Interface:
		v.Set(reflect.ValueOf(string(s)))
	}

	return nil
}

func (d *decodeState) field(s string, t reflect.Type) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Name == s {
			return f, true
		}
		if f.Tag.Get("amf.name") == s {
			return f, true
		}
	}
	return *new(reflect.StructField), false
}

func (d *decodeState) objectInterface() map[string]interface{} {
	m := make(map[string]interface{})
	for {
		k, err := d.decodeObjectName()
		if err != nil {
			d.error(err)
		}

		if k == "" {
			m, err := d.decodeMarker()
			if err != nil {
				d.error(err)
				break
			}
			switch m {
			case ObjectEndMarker:
				break
			default:
				d.error(errors.New("unexpected marker, must be ObjectEndMarker"))
				return nil
			}
		}

		m[k] = d.valueInterface()
	}

	return m
}

// marker: 1 byte 0x03
// format:
// - loop encoded string followed by encoded value
// - terminated with empty string followed by 1 byte 0x09
func (d *decodeState) decodeObject(v reflect.Value) error {
	if v.Kind() == reflect.Interface && v.NumMethod() == 0 {
		v.Set(reflect.ValueOf(d.objectInterface()))
		return nil
	}

	v = d.indirect(v)

	switch v.Kind() {
	case reflect.Map:
		t := v.Type()
		switch t.Key().Kind() {
		case reflect.String,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		default:
			return errors.New("unexpected map key type, found: " + t.Key().String())
		}
		if v.IsNil() {
			v.Set(reflect.MakeMap(t))
		}
	case reflect.Struct:
		// ok
	default:
		return &DecodeTypeError{Value: "object", Type: v.Type()}
	}

	var mapElem reflect.Value

	for {
		on, err := d.decodeObjectName()
		if err != nil {
			return err
		}

		if on == "" {
			m, err := d.decodeMarker()
			if err != nil {
				return err
			}
			switch m {
			case ObjectEndMarker:
				return nil
			default:
				return errors.New("expect object end marker")
			}
		}

		var subv reflect.Value

		if v.Kind() == reflect.Map {
			elemType := v.Type().Elem()
			if !mapElem.IsValid() {
				mapElem = reflect.New(elemType).Elem()
			} else {
				mapElem.Set(reflect.Zero(elemType))
			}
			subv = mapElem
		} else {
			f, ok := d.field(on, v.Type())
			if !ok {
				return errors.New("object name (" + on + ") not found in " + v.Type().String())
			}

			subv = v.FieldByName(f.Name)
		}

		err = d.decodeValue(subv)
		if err != nil {
			return err
		}

		if v.Kind() == reflect.Map {
			kt := v.Type().Key()
			var kv reflect.Value
			switch {
			case kt.Kind() == reflect.String:
				kv = reflect.ValueOf(on).Convert(kt)
			default:
				switch kt.Kind() {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					s := string(on)
					n, err := strconv.ParseInt(s, 10, 64)
					if err != nil || reflect.Zero(kt).OverflowInt(n) {
						return &DecodeTypeError{Value: "number " + s, Type: kt}
					}
					kv = reflect.ValueOf(n).Convert(kt)
				case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
					s := string(on)
					n, err := strconv.ParseUint(s, 10, 64)
					if err != nil || reflect.Zero(kt).OverflowUint(n) {
						return &DecodeTypeError{Value: "number " + s, Type: kt}
					}
					kv = reflect.ValueOf(n).Convert(kt)
				default:
					panic("amf: Unexpected key type") // should never occur
				}
			}
			v.SetMapIndex(kv, subv)
		}
	}
}

func (d *decodeState) nullInterface() interface{} {
	return nil
}

func (d *decodeState) decodeNull(v reflect.Value) error {
	if v.IsNil() {
		return nil
	}

	v = d.indirect(v)

	switch v.Kind() {
	default:
		return errors.New("unexpected kind (" + v.Type().String() + ") when decoding null")
	case reflect.Interface, reflect.Slice, reflect.Map, reflect.Ptr:
		v.Set(reflect.Zero(v.Type()))
		return nil
	}
	return nil
}

func (d *decodeState) arrayNullInterface() interface{} {
	return nil
}

func (d *decodeState) decodeArrayNull(v reflect.Value) error {
	return nil
}

func (d *decodeState) ecmaArrayInterface() interface{} {
	_, err := d.decodeUint32()
	if err != nil {
		d.error(err)
		return nil
	}

	return d.objectInterface()
}

// marker: 1 byte 0x08
// format:
// - 4 byte big endian uint32 with length of associative array
// - normal object format:
//   - loop encoded string followed by encoded value
//   - terminated with empty string followed by 1 byte 0x09
func (d *decodeState) decodeECMAArray(v reflect.Value) error {
	_, err := d.decodeUint32()
	if err != nil {
		return err
	}

	return d.decodeObject(v)
}

func (d *decodeState) strictArrayInterface() interface{} {
	u, err := d.decodeUint32()
	if err != nil {
		d.error(err)
		return nil
	}

	var v = make([]interface{}, 0)
	for i := uint32(0); i < u; i++ {
		v = append(v, d.valueInterface())
	}

	return v
}

func (d *decodeState) decodeStrictArray(v reflect.Value) error {
	u, err := d.decodeUint32()
	if err != nil {
		return err
	}

	for i := uint32(0); i < u; i++ {
		err := d.decode(v)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *decodeState) dateInterface() interface{} {
	var f float64
	err := binary.Read(d.r, binary.BigEndian, &f)
	if err != nil {
		d.error(err)
		return nil
	}

	s := make([]byte, 2)
	_, err = d.r.Read(s)
	if err != nil {
		d.error(err)
		return nil
	}

	return nil
}

func (d *decodeState) decodeDate(v reflect.Value) error {
	var f float64
	err := binary.Read(d.r, binary.BigEndian, &f)
	if err != nil {
		return err
	}

	s := make([]byte, 2)
	_, err = d.r.Read(s)
	if err != nil {
		return nil
	}

	return nil
}

func (d *decodeState) longStringInterface() interface{} {
	u, err := d.decodeUint32()
	if err != nil {
		d.error(err)
		return nil
	}

	s := make([]byte, u)
	_, err = d.r.Read(s)
	if err != nil {
		d.error(err)
		return nil
	}

	return string(s)
}

func (d *decodeState) decodeLongString(v reflect.Value) error {
	u, err := d.decodeUint32()
	if err != nil {
		return err
	}

	s := make([]byte, u)
	_, err = d.r.Read(s)
	if err != nil {
		return err
	}

	v = d.indirect(v)

	switch v.Kind() {
	default:
		return errors.New("unexpected kind (" + v.Type().String() + ") when decoding string")
	case reflect.Int, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(string(s), 10, 0)
		if err != nil {
			return err
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(string(s), 10, 0)
		if err != nil {
			return err
		}
		v.SetUint(n)
	case reflect.String:
		v.SetString(string(s))
	case reflect.Interface:
		v.Set(reflect.ValueOf(string(s)))
	}

	return nil
}

func (d *decodeState) unsupportedInterface() interface{} {
	return nil
}

func (d *decodeState) decodeUnsupported(v reflect.Value) error {
	return nil
}

func (d *decodeState) xmlDocumentInterface() interface{} {
	return d.longStringInterface()
}

func (d *decodeState) decodeXMLDocument(v reflect.Value) error {
	return d.decodeLongString(v)
}

func (d *decodeState) decodeValue(v reflect.Value) error {
	m, err := d.decodeMarker()
	if err != nil {
		return errors.New("read marker failed")
	}

	switch m {
	default:
		return errors.New("unexpected marker type")
	case NumberMarker:
		return d.decodeFloat64(v)
	case BooleanMarker:
		return d.decodeBool(v)
	case StringMarker:
		return d.decodeString(v)
	case ObjectMarker:
		return d.decodeObject(v)
	case MovieClipMarker:
		return errors.New("decode amf0: unsupported type movieclip")
	case NullMarker:
		return d.decodeNull(v)
	case ArrayNullMarker:
		return d.decodeArrayNull(v)
	case ReferenceMarker:
		return errors.New("decode amf0: unsupported type reference")
	case ECMAArrayMarker:
		return d.decodeECMAArray(v)
	case StrictArrayMarker:
		return d.decodeStrictArray(v)
	case DateMarker:
		return d.decodeDate(v)
	case LongStringMarker:
		return d.decodeLongString(v)
	case UnsupportedMarker:
		return d.decodeUnsupported(v)
	case RecordSetMarker:
		return errors.New("decode amf0: unsupported type recordset")
	case XMLDocumentMarker:
		return d.decodeXMLDocument(v)
	case TypedObjectMarker:
		return errors.New("decode amf0: unsupported type typedobject")
	}
}

type InvalidDecodeError struct {
	Type reflect.Type
}

func (e *InvalidDecodeError) Error() string {
	if e.Type == nil {
		return "amf: Decode(nil)"
	}

	if e.Type.Kind() != reflect.Ptr {
		return "amf: Decode(non-pointer " + e.Type.String() + ")"
	}
	return "amf: Decode(nil " + e.Type.String() + ")"
}

func (d *decodeState) decode(v interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(runtime.Error); ok {
				panic(r)
			}
			err = r.(error)
		}
	}()

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return &InvalidDecodeError{reflect.TypeOf(v)}
	}

	return d.decodeValue(rv)
}

func (d *decodeState) valueInterface() interface{} {
	m, err := d.decodeMarker()
	if err != nil {
		d.error(errors.New("failed to read marker"))
		return nil
	}

	switch m {
	default:
		d.error(errors.New("unexpected marker type"))
	case NumberMarker:
		return d.floatInterface()
	case BooleanMarker:
		return d.boolInterface()
	case StringMarker:
		return d.stringInterface()
	case ObjectMarker:
		return d.objectInterface()
	case MovieClipMarker:
		d.error(errors.New("decode amf0: unsupported type movieclip"))
	case NullMarker:
		return d.nullInterface()
	case ArrayNullMarker:
		return d.arrayNullInterface()
	case ReferenceMarker:
		d.error(errors.New("decode amf0: unsupported type reference"))
	case ECMAArrayMarker:
		return d.ecmaArrayInterface()
	case StrictArrayMarker:
		return d.strictArrayInterface()
	case DateMarker:
		return d.dateInterface()
	case LongStringMarker:
		return d.longStringInterface()
	case UnsupportedMarker:
		return d.unsupportedInterface()
	case RecordSetMarker:
		d.error(errors.New("decode amf0: unsupported type recordset"))
	case XMLDocumentMarker:
		return d.xmlDocumentInterface()
	case TypedObjectMarker:
		d.error(errors.New("decode amf0: unsupported type typedobject"))
	}

	return nil
}

func Decode(data []byte, v interface{}) error {
	d := decodeState{}
	d.r = bytes.NewReader(data)
	return d.decode(v)
}