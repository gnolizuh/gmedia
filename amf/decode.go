package amf

import (
	"runtime"
	"reflect"
	"bytes"
	"errors"
	"encoding/binary"
	"strconv"
)

type decodeState struct {
	r  *bytes.Reader
}

func (d *decodeState) decodeMarker() (byte, error) {
	return d.r.ReadByte()
}

func (d *decodeState) decodeFloat64(v reflect.Value) error {
	var f float64
	err := binary.Read(d.r, binary.BigEndian, &f)
	if err != nil {
		return err
	}

	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		v.SetFloat(f)
	case reflect.Int32, reflect.Int, reflect.Int64:
		v.SetInt(int64(f))
	case reflect.Uint32, reflect.Uint, reflect.Uint64:
		v.SetUint(uint64(f))
	case reflect.Interface:
		v.Set(reflect.ValueOf(f))
	default:
		return errors.New("invalid type:" + v.Type().String() + " for float64")
	}

	return nil
}


func (d *decodeState) decodeBool(v reflect.Value) error {
	bt, err := d.r.ReadByte()
	if err != nil {
		return err
	}

	b := bt != 0

	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(b)
	case reflect.Interface:
		v.Set(reflect.ValueOf(b))
	default:
		return errors.New("invalid type:" + v.Type().String() + " for bool")
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

// marker: 1 byte 0x03
// format:
// - loop encoded string followed by encoded value
// - terminated with empty string followed by 1 byte 0x09
func (d *decodeState) decodeObject(v reflect.Value) error {
	if v.Kind() == reflect.Interface {
		var dummy map[string]interface{}
		vt := reflect.MakeMap(reflect.TypeOf(dummy))
		v.Set(vt)
		v = vt
	}

	if v.Kind() == reflect.Map {
		if v.IsNil() {
			vt := reflect.MakeMap(v.Type())
			v.Set(vt)
			v = vt
		}
	}

	if v.Kind() != reflect.Struct {
		return errors.New("struct type expected, found:" + v.Type().String())
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

func (d *decodeState) decodeArrayNull(v reflect.Value) error {
	return nil
}

func (d *decodeState) decodeECMAArray(v reflect.Value) error {
	u, err := d.decodeUint32()
	if err != nil {
		return err
	}

	if v.IsNil() {
		var vt reflect.Value
		if v.Type().Kind() == reflect.Slice {
			vt = reflect.MakeSlice(v.Type(), int(u), int(u))
		} else if v.Type().Kind() == reflect.Interface {
			vt = reflect.ValueOf(make([]interface{},int(u), int(u)))
		} else {
			return errors.New("invalid type:" + v.Type().String() + " for array")
		}
		v.Set(vt)
		v = vt
	}

	for i := 0; i < int(u); i++ {
		err := d.decode(v.Index(i))
		if err != nil {
			return err
		}
	}

	return nil
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
	case DateMarker:
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

func Decode(data []byte, v interface{}) error {
	d := decodeState{}
	d.r = bytes.NewReader(data)
	return d.decode(v)
}