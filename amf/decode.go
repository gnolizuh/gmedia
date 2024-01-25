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

package amf

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
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

// Unmarshaler is the interface implemented by types
// that can unmarshal an AMF description of themselves.
// The input can be assumed to be a valid encoding of
// an AMF value. UnmarshalAMF must copy the AMF data
// if it wishes to retain the data after returning.
//
// By convention, to approximate the behavior of Unmarshal itself,
// Unmarshalers implement UnmarshalAMF([]byte("null")) as a no-op.
type Unmarshaler interface {
	UnmarshalAMF([]byte) error
}

// An UnmarshalTypeError describes an AMF value that was
// not appropriate for a value of a specific Go type.
type UnmarshalTypeError struct {
	Value  string       // description of AMF value - "bool", "array", "number -5"
	Type   reflect.Type // type of Go value it could not be assigned to
	Offset int64        // error occurred after reading Offset bytes
	Struct string       // name of the struct type containing the field
	Field  string       // the full path from root node to the field
}

func (e *UnmarshalTypeError) Error() string {
	if e.Struct != "" || e.Field != "" {
		return "amf: cannot unmarshal " + e.Value + " into Go struct field " + e.Struct + "." + e.Field + " of type " + e.Type.String()
	}
	return "amf: cannot unmarshal " + e.Value + " into Go value of type " + e.Type.String()
}

// An errorContext provides context for type errors during decoding.
type errorContext struct {
	Struct     reflect.Type
	FieldStack []string
}

type decodeState struct {
	data         []byte
	reader       *bytes.Reader
	errorContext *errorContext
	savedError   error
}

func (d *decodeState) init(data []byte) *decodeState {
	d.data = data
	d.reader = bytes.NewReader(data)
	return d
}

// saveError saves the first err it is called with,
// for reporting at the end of the unmarshal.
func (d *decodeState) saveError(err error) {
	if d.savedError == nil {
		d.savedError = d.addErrorContext(err)
	}
}

// addErrorContext returns a new error enhanced with information from d.errorContext
func (d *decodeState) addErrorContext(err error) error {
	if d.errorContext != nil && (d.errorContext.Struct != nil || len(d.errorContext.FieldStack) > 0) {
		var err *UnmarshalTypeError
		switch {
		case errors.As(err, &err):
			err.Struct = d.errorContext.Struct.Name()
			err.Field = strings.Join(d.errorContext.FieldStack, ".")
		}
	}
	return err
}

// indirect walks down v allocating pointers as needed,
// until it gets to a non-pointer.
// If it encounters an Unmarshaler, indirect stops and returns that.
func indirect(v reflect.Value) (Unmarshaler, reflect.Value) {
	v0 := v
	haveAddr := false

	// If v is a named type and is addressable,
	// start with its address, so that if the type has pointer methods,
	// we find them.
	if v.Kind() != reflect.Pointer && v.CanAddr() {
		haveAddr = true
		v = v.Addr()
	}
	for {
		// Load value from interface, but only if the result will be
		// usefully addressable.
		if v.Kind() == reflect.Interface && !v.IsNil() {
			e := v.Elem()
			if e.Kind() == reflect.Pointer && !e.IsNil() {
				haveAddr = false
				v = e
				continue
			}
		}

		if v.Kind() != reflect.Pointer {
			break
		}

		// Prevent infinite loop if v is an interface pointing to its own address:
		//     var v interface{}
		//     v = &v
		if v.Elem().Kind() == reflect.Interface && v.Elem().Elem() == v {
			v = v.Elem()
			break
		}
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		if v.Type().NumMethod() > 0 && v.CanInterface() {
			if u, ok := v.Interface().(Unmarshaler); ok {
				return u, reflect.Value{}
			}
		}

		if haveAddr {
			v = v0 // restore original value after round-trip Value.Addr().Elem()
			haveAddr = false
		} else {
			v = v.Elem()
		}
	}
	return nil, v
}

func (d *decodeState) error(err error) {
	panic(err)
}

func (d *decodeState) marker() (byte, error) {
	return d.reader.ReadByte()
}

func (d *decodeState) floatInterface() interface{} {
	var f float64
	err := binary.Read(d.reader, binary.BigEndian, &f)
	if err != nil {
		d.error(err)
	}
	return f
}

func (d *decodeState) float64(v reflect.Value) error {
	var f float64
	err := binary.Read(d.reader, binary.BigEndian, &f)
	if err != nil {
		return err
	}

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
	bt, err := d.reader.ReadByte()
	if err != nil {
		d.error(err)
	}

	return bt != 0
}

func (d *decodeState) bool(v reflect.Value) error {
	bt, err := d.reader.ReadByte()
	if err != nil {
		return err
	}

	b := bt != 0

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

func (d *decodeState) uint16() (uint16, error) {
	var u uint16
	err := binary.Read(d.reader, binary.BigEndian, &u)
	if err != nil {
		return 0, err
	}
	return u, nil
}

func (d *decodeState) uint32() (uint32, error) {
	var u uint32
	err := binary.Read(d.reader, binary.BigEndian, &u)
	if err != nil {
		return 0, err
	}
	return u, nil
}

func (d *decodeState) objectName() ([]byte, error) {
	// read length.
	n, err := d.uint16()
	if err != nil {
		return nil, err
	}

	// MUST be ObjectEndMarker that follows
	if n == 0 {
		return []byte{}, nil
	}

	on := make([]byte, n)
	_, err = d.reader.Read(on)
	if err != nil {
		return nil, err
	}

	return on, nil
}

func (d *decodeState) stringInterface() interface{} {
	u, err := d.uint16()
	if err != nil {
		d.error(err)
		return nil
	}

	s := make([]byte, u)
	_, err = d.reader.Read(s)
	if err != nil {
		d.error(err)
		return nil
	}

	return string(s)
}

func (d *decodeState) string(v reflect.Value) error {
	u, err := d.uint16()
	if err != nil {
		return err
	}

	s := make([]byte, u)
	_, err = d.reader.Read(s)
	if err != nil {
		return err
	}

	switch v.Kind() {
	default:
		return errors.New("unexpected type: " + v.Type().String() + " decoding string")
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.Uint8 {
			return &UnmarshalTypeError{Value: "string", Type: v.Type()}
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
		if f.Tag.Get("amf") == s {
			return f, true
		}
		if strings.EqualFold(f.Name, s) {
			return f, true
		}
	}
	return *new(reflect.StructField), false
}

// arrayInterface is like array but returns []interface{}.
func (d *decodeState) arrayInterface(n uint32) []any {
	var v = make([]any, 0)
	for i := uint32(0); i < n; i++ {
		v = append(v, d.valueInterface())
	}
	return v
}

func (d *decodeState) objectInterface() map[string]interface{} {
	m := make(map[string]interface{})
	for {
		on, err := d.objectName()
		if err != nil {
			d.error(err)
		}

		if len(on) > 0 {
			// read value.
			m[string(on)] = d.valueInterface()
		} else {
			// MUST be ObjectEndMarker.
			end, err := d.marker()
			if err != nil {
				d.error(err)
				break
			}
			switch end {
			case ObjectEndMarker:
				return m
			default:
				d.error(errors.New("unexpected marker, must be ObjectEndMarker"))
				return nil
			}
		}
	}
	return m
}

// marker: 1 byte 0x03
// format:
// - loop encoded string followed by encoded value
// - terminated with empty string followed by 1 byte 0x09
func (d *decodeState) object(v reflect.Value) error {
	t := v.Type()

	// Decoding into nil interface? Switch to non-reflect code.
	if v.Kind() == reflect.Interface && v.NumMethod() == 0 {
		oi := d.objectInterface()
		v.Set(reflect.ValueOf(oi))
		return nil
	}

	var fields structFields

	// Check type of target:
	//   struct or
	//   map[T1]T2 where T1 is string, an integer type,
	//             or an encoding.TextUnmarshaler
	switch v.Kind() {
	case reflect.Map:
		// Map key must either have string kind, have an integer kind,
		// or be an encoding.TextUnmarshaler.
		switch t.Key().Kind() {
		case reflect.String,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		default:
			d.saveError(&UnmarshalTypeError{Value: "object", Type: t, Offset: int64(d.reader.Len())})
			return nil
		}
		if v.IsNil() {
			v.Set(reflect.MakeMap(t))
		}
	case reflect.Struct:
		fields = cachedTypeFields(t)
		// ok
	default:
		d.saveError(&UnmarshalTypeError{Value: "object", Type: t, Offset: int64(d.reader.Len())})
		return nil
	}

	var mapElem reflect.Value
	var origErrorContext errorContext
	if d.errorContext != nil {
		origErrorContext = *d.errorContext
	}

	for {
		on, err := d.objectName()
		if err != nil {
			return err
		}

		if len(on) == 0 {
			// MUST be ObjectEndMarker.
			end, err := d.marker()
			if err != nil {
				d.error(err)
				break
			}
			switch end {
			case ObjectEndMarker:
				return nil
			default:
				e := errors.New("unexpected marker, must be ObjectEndMarker")
				d.error(e)
				return e
			}
		}

		// Figure out field corresponding to key.
		var subv reflect.Value

		if v.Kind() == reflect.Map {
			elemType := t.Elem()
			if !mapElem.IsValid() {
				mapElem = reflect.New(elemType).Elem()
			} else {
				mapElem.SetZero()
			}
			subv = mapElem
		} else {
			f := fields.byExactName[string(on)]
			if f == nil {
				f = fields.byFoldedName[string(foldName(on))]
			}
			if f != nil {
				subv = v
				for _, i := range f.index {
					if subv.Kind() == reflect.Pointer {
						if subv.IsNil() {
							// If a struct embeds a pointer to an unexported type,
							// it is not possible to set a newly allocated value
							// since the field is unexported.
							//
							// See https://golang.org/issue/21357
							if !subv.CanSet() {
								d.saveError(fmt.Errorf("json: cannot set embedded pointer to unexported struct: %v", subv.Type().Elem()))
								// Invalidate subv to ensure d.value(subv) skips over
								// the JSON value without assigning it to subv.
								subv = reflect.Value{}
								break
							}
							subv.Set(reflect.New(subv.Type().Elem()))
						}
						subv = subv.Elem()
					}
					subv = subv.Field(i)
				}
				if d.errorContext == nil {
					d.errorContext = new(errorContext)
				}
				d.errorContext.FieldStack = append(d.errorContext.FieldStack, f.name)
				d.errorContext.Struct = t
			}
		}

		err = d.value(subv)
		if err != nil {
			return err
		}

		// Write value back to map;
		// if using struct, subv points into struct already.
		if v.Kind() == reflect.Map {
			kt := t.Key()
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
						d.saveError(&UnmarshalTypeError{Value: "number " + s, Type: kt, Offset: int64(d.reader.Len() + 1)})
						break
					}
					kv = reflect.ValueOf(n).Convert(kt)
				case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
					s := string(on)
					n, err := strconv.ParseUint(s, 10, 64)
					if err != nil || reflect.Zero(kt).OverflowUint(n) {
						d.saveError(&UnmarshalTypeError{Value: "number " + s, Type: kt, Offset: int64(d.reader.Len() + 1)})
						break
					}
					kv = reflect.ValueOf(n).Convert(kt)
				default:
					panic("amf: Unexpected ObjectName type") // should never occur
				}
			}
			if kv.IsValid() {
				v.SetMapIndex(kv, subv)
			}
		}

		if d.errorContext != nil {
			// Reset errorContext to its original state.
			// Keep the same underlying array for FieldStack, to reuse the
			// space and avoid unnecessary allocs.
			d.errorContext.FieldStack = d.errorContext.FieldStack[:len(origErrorContext.FieldStack)]
			d.errorContext.Struct = origErrorContext.Struct
		}
	}

	return nil
}

func (d *decodeState) nullInterface() interface{} {
	return nil
}

func (d *decodeState) null(v reflect.Value) error {
	if v.IsNil() {
		return nil
	}

	switch v.Kind() {
	default:
		return errors.New("unexpected kind (" + v.Type().String() + ") when decoding null")
	case reflect.Interface, reflect.Slice, reflect.Map, reflect.Ptr:
		v.Set(reflect.Zero(v.Type()))
		return nil
	}
}

func (d *decodeState) arrayNullInterface() interface{} {
	return nil
}

func (d *decodeState) arrayNull(_ reflect.Value) error {
	return nil
}

func (d *decodeState) ecmaArrayInterface() interface{} {
	_, err := d.uint32()
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
func (d *decodeState) ecmaArray(v reflect.Value) error {
	_, err := d.uint32()
	if err != nil {
		return err
	}

	return d.object(v)
}

func (d *decodeState) strictArrayInterface() interface{} {
	u, err := d.uint32()
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

func (d *decodeState) strictArray(v reflect.Value) error {
	n, err := d.uint32()
	if err != nil {
		return err
	}

	// Check type of target.
	switch v.Kind() {
	case reflect.Interface:
		if v.NumMethod() == 0 {
			// Decoding into nil interface? Switch to non-reflect code.
			ai := d.arrayInterface(n)
			v.Set(reflect.ValueOf(ai))
			return nil
		}
		// Otherwise it's invalid.
		fallthrough
	default:
		d.saveError(&UnmarshalTypeError{Value: "array", Type: v.Type(), Offset: int64(d.reader.Len())})
		return nil
	case reflect.Array, reflect.Slice:
		break
	}

	i := 0
	for ; i < int(n); i++ {
		// Expand slice length, growing the slice if necessary.
		if v.Kind() == reflect.Slice {
			if i >= v.Cap() {
				v.Grow(1)
			}
			if i >= v.Len() {
				v.SetLen(i + 1)
			}
		}

		if i < v.Len() {
			// Decode into element.
			if err := d.value(v.Index(i)); err != nil {
				return err
			}
		} else {
			// Ran out of fixed array: skip.
			if err := d.value(reflect.Value{}); err != nil {
				return err
			}
		}
	}

	if i < v.Len() {
		if v.Kind() == reflect.Array {
			for ; i < v.Len(); i++ {
				v.Index(i).SetZero() // zero remainder of array
			}
		} else {
			v.SetLen(i) // truncate the slice
		}
	}
	if i == 0 && v.Kind() == reflect.Slice {
		v.Set(reflect.MakeSlice(v.Type(), 0, 0))
	}
	return nil
}

func (d *decodeState) dateInterface() interface{} {
	var f float64
	err := binary.Read(d.reader, binary.BigEndian, &f)
	if err != nil {
		d.error(err)
		return nil
	}

	s := make([]byte, 2)
	_, err = d.reader.Read(s)
	if err != nil {
		d.error(err)
		return nil
	}

	return nil
}

func (d *decodeState) date(_ reflect.Value) error {
	var f float64
	err := binary.Read(d.reader, binary.BigEndian, &f)
	if err != nil {
		return err
	}

	s := make([]byte, 2)
	_, err = d.reader.Read(s)
	if err != nil {
		return nil
	}

	return nil
}

func (d *decodeState) longStringInterface() interface{} {
	u, err := d.uint32()
	if err != nil {
		d.error(err)
		return nil
	}

	s := make([]byte, u)
	_, err = d.reader.Read(s)
	if err != nil {
		d.error(err)
		return nil
	}

	return string(s)
}

func (d *decodeState) longString(v reflect.Value) error {
	u, err := d.uint32()
	if err != nil {
		return err
	}

	s := make([]byte, u)
	_, err = d.reader.Read(s)
	if err != nil {
		return err
	}

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

func (d *decodeState) unsupported(_ reflect.Value) error {
	return nil
}

func (d *decodeState) xmlDocumentInterface() interface{} {
	return d.longStringInterface()
}

func (d *decodeState) xmlDocument(v reflect.Value) error {
	return d.longString(v)
}

// value consumes an AMF value from d.data[d.off-1:], decoding into v, and
// reads the following byte ahead. If v is invalid, the value is discarded.
// The first byte of the value has been read already.
func (d *decodeState) value(v reflect.Value) error {
	u, pv := indirect(v)
	if u != nil {
		return u.UnmarshalAMF(d.data[d.reader.Len():])
	}
	v = pv

	m, err := d.marker()
	if err != nil {
		return errors.New("read marker failed")
	}

	switch m {
	default:
		return errors.New("unexpected marker type")
	case NumberMarker:
		return d.float64(v)
	case BooleanMarker:
		return d.bool(v)
	case StringMarker:
		return d.string(v)
	case ObjectMarker:
		return d.object(v)
	case MovieClipMarker:
		return errors.New("decode amf0: unsupported type movie clip")
	case NullMarker:
		return d.null(v)
	case UndefinedMarker:
		return d.arrayNull(v)
	case ReferenceMarker:
		return errors.New("decode amf0: unsupported type reference")
	case ECMAArrayMarker:
		return d.ecmaArray(v)
	case StrictArrayMarker:
		return d.strictArray(v)
	case DateMarker:
		return d.date(v)
	case LongStringMarker:
		return d.longString(v)
	case UnsupportedMarker:
		return d.unsupported(v)
	case RecordSetMarker:
		return errors.New("decode amf0: unsupported type recordset")
	case XMLDocumentMarker:
		return d.xmlDocument(v)
	case TypedObjectMarker:
		return errors.New("decode amf0: unsupported type typed object")
	}
}

// An InvalidUnmarshalError describes an invalid argument passed to Unmarshal.
// (The argument to Unmarshal must be a non-nil pointer.)
type InvalidUnmarshalError struct {
	Type reflect.Type
}

func (e *InvalidUnmarshalError) Error() string {
	if e.Type == nil {
		return "amf: Unmarshal(nil)"
	}

	if e.Type.Kind() != reflect.Pointer {
		return "amf: Unmarshal(non-pointer " + e.Type.String() + ")"
	}
	return "amf: Unmarshal(nil " + e.Type.String() + ")"
}

func (d *decodeState) unmarshal(v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &InvalidUnmarshalError{reflect.TypeOf(v)}
	}

	// We decode rv not rv.Elem because the Unmarshaler interface
	// test must be applied at the top level of the value.
	err := d.value(rv)
	if err != nil {
		return d.addErrorContext(err)
	}
	return d.savedError
}

func (d *decodeState) valueInterface() interface{} {
	m, err := d.marker()
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
		d.error(errors.New("decode amf0: unsupported type movie clip"))
	case NullMarker:
		return d.nullInterface()
	case UndefinedMarker:
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
		d.error(errors.New("decode amf0: unsupported type typed object"))
	}

	return nil
}

func Unmarshal(data []byte, v interface{}) error {
	var d decodeState
	d.init(data)
	return d.unmarshal(v)
}

func DecodeWithReader(reader *bytes.Reader, v interface{}) error {
	var d decodeState
	d.reader = reader
	return d.unmarshal(v)
}
