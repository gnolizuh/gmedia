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

	switch v.Kind() {
	default:
		if v.Kind() == reflect.String && v.Type() == numberType {
			v.SetString(strconv.FormatFloat(f, 'f', 6, 64))
			break
		}
		return errors.New("invalid type:" + v.Type().String() + " for float64")
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

	switch v.Kind() {
	default:
		return errors.New("invalid type:" + v.Type().String() + " for bool")
	case reflect.Bool:
		v.SetBool(b)
	case reflect.Interface:
		v.Set(reflect.ValueOf(b))
	}

	return nil
}

func (d *decodeState) decodeUint() (uint16, error) {
	var u uint16
	err := binary.Read(d.r, binary.BigEndian, u)
	if err != nil {
		return 0, err
	}
	return u, nil
}

func (d *decodeState) decodeUint32() (uint32, error) {
	var u uint32
	err := binary.Read(d.r, binary.BigEndian, u)
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
		return errors.New("unexpected kind type, found: " + v.Type().String())
	}

	for {
		s, err := d.decodeObjectName()
		if err != nil {
			return err
		}

		if s == "" {
			m, err := d.decodeMarker()
			if err != nil {
				return err
			}
			switch m {
			case ObjectEndMarker:
				break
			default:
				return errors.New("expect object end marker")
			}
		}

		f, ok := d.field(s, v.Type())
		if !ok {
			return errors.New("key: " + s + " not found in struct:" + v.Type().String())
		}

		err = d.decode(v.FieldByName(f.Name))
		if err != nil {
			return err
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
	switch v.Kind() {
	case reflect.Interface, reflect.Slice, reflect.Map, reflect.Ptr:
		v.Set(reflect.Zero(v.Type()))
		return nil
	default:
		return errors.New("invalid type:" + v.Type().String() + " for nil")
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

	switch v.Kind() {
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
	default:
		return errors.New("invalid type:" + v.Type().String() + " for string")
	}

	return nil
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
	case NullMarker:
		return d.decodeNull(v)
	case ArrayNullMarker:
		return d.decodeArrayNull(v)
	case ECMAArrayMarker:
		return d.decodeECMAArray(v)
	case StrictArrayMarker:
		return d.decodeStrictArray(v)
	case DateMarker:
		// return d.decodeDate(v)
	case LongStringMarker:
		return d.decodeLongString(v)
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
		return nil
	case NumberMarker:
		return d.floatInterface()
	case BooleanMarker:
		return d.boolInterface()
	case StringMarker:
		return d.stringInterface()
	case ObjectMarker:
		return d.objectInterface()
	case NullMarker:
		return d.nullInterface()
	case ArrayNullMarker:
		return d.arrayNullInterface()
	case ECMAArrayMarker:
		return d.ecmaArrayInterface()
	case StrictArrayMarker:
		return d.strictArrayInterface()
	case DateMarker:
		// return d.dateInterface()
	case LongStringMarker:
		return d.longStringInterface()
	}

	return nil
}

func Decode(data []byte, v interface{}) error {
	d := decodeState{}
	d.r = bytes.NewReader(data)
	return d.decode(v)
}