package amf

import (
	"bytes"
	"runtime"
	"reflect"
	"sync"
)

type encodeState struct {
	bytes.Buffer
	cache        map[string]int
	scratch      [64]byte
}

func (e *encodeState) encode(v interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(runtime.Error); ok {
				panic(r)
			}
			if s, ok := r.(string); ok {
				panic(s)
			}
			err = r.(error)
		}
	}()
	e.reflectValue(reflect.ValueOf(v))
	return nil
}

func (e *encodeState) reflectValue(v reflect.Value) {
	valueEncoder(v)(e, v)
}

func (e *encodeState) u29(u uint32) {
	b := make([]byte, 0, 4)

	switch {
	case u < 0x80:
		b = append(b, byte(u))
	case u < 0x4000:
		b = append(b, byte((u>>7)|0x80))
		b = append(b, byte(u&0x7f))
	case u < 0x200000:
		b = append(b, byte((u>>14)|0x80))
		b = append(b, byte((u>>7)|0x80))
		b = append(b, byte(u&0x7f))
	case u < 0x20000000:
		b = append(b, byte((u>>22)|0x80))
		b = append(b, byte((u>>15)|0x80))
		b = append(b, byte((u>>7)|0x80))
		b = append(b, byte(u&0xff))
	}

	e.Write(b)
}

func (e *encodeState) marker(m byte) {
	e.WriteByte(m)
}

func (e *encodeState) bool(b bool) {
	if b {
		e.WriteByte(1)
	} else {
		e.WriteByte(0)
	}
}

func (e *encodeState) string(s string) {
	i, ok := e.cache[s]
	if ok {
		e.u29(uint32(i << 1))
		return
	}

	e.u29(uint32((len(s) << 1) | 0x01))

	if s != "" {
		e.cache[s] = len(e.cache)
	}

	e.WriteString(s)
}

func (e *encodeState) null() {
	e.WriteByte(AMFNull)
}

type encoderFunc func(e *encodeState, v reflect.Value)

var encoderCache sync.Map

func valueEncoder(v reflect.Value) encoderFunc {
	return typeEncoder(v.Type())
}

func typeEncoder(t reflect.Type) encoderFunc {
	if fi, ok := encoderCache.Load(t); ok {
		return fi.(encoderFunc)
	}

	var (
		wg sync.WaitGroup
		f  encoderFunc
	)
	wg.Add(1)
	fi, loaded := encoderCache.LoadOrStore(t, encoderFunc(func(e *encodeState, v reflect.Value) {
		wg.Wait()
		f(e, v)
	}))
	if loaded {
		return fi.(encoderFunc)
	}

	f = newTypeEncoder(t)
	wg.Done()
	encoderCache.Store(t, f)
	return f
}

func newTypeEncoder(t reflect.Type) encoderFunc {
	switch t.Kind() {
	case reflect.String:
		return stringEncoder
	case reflect.Bool:
		return boolEncoder
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intEncoder
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return uintEncoder
	case reflect.Float32, reflect.Float64:
		return floatEncoder
	case reflect.Interface:
		return interfaceEncoder
	case reflect.Struct:
		return newStructEncoder(t)
	case reflect.Map:
		return newMapEncoder(t)
	case reflect.Slice:
		return newSliceEncoder(t)
	case reflect.Array:
		return newArrayEncoder(t)
	case reflect.Ptr:
		return newPtrEncoder(t)
	default:
		return unsupportedTypeEncoder
	}
}

func stringEncoder(e *encodeState, v reflect.Value) {
	e.marker(AMFString)
	e.string(v.String())
}

func boolEncoder(e *encodeState, v reflect.Value) {
	e.marker(AMFBoolean)
	e.bool(v.Bool())
}

func intEncoder(e *encodeState, v reflect.Value) {
	e.marker(AMFNumber)
	e.u29(uint32(v.Int()))
}

func uintEncoder(e *encodeState, v reflect.Value) {
	e.marker(AMFNumber)
	e.u29(uint32(v.Uint()))
}

func floatEncoder(e *encodeState, v reflect.Value) {
	e.marker(AMFNumber)
	e.u29(uint32(v.Float()))
}

func interfaceEncoder(e *encodeState, v reflect.Value) {
	if v.IsNil() {
		e.null()
		return
	}
	e.reflectValue(v.Elem())
}

func newStructEncoder(t reflect.Type) encoderFunc {
}

func newMapEncoder(t reflect.Type) encoderFunc {
}

func newSliceEncoder(t reflect.Type) encoderFunc {
}

func newArrayEncoder(t reflect.Type) encoderFunc {
}

func newPtrEncoder(t reflect.Type) encoderFunc {
}

func unsupportedTypeEncoder(e *encodeState, v reflect.Value) {
}

func Encode(v interface{}) ([]byte, error) {
	e := &encodeState{}
	err := e.encode(v)
	if err != nil {
		return nil, err
	}
	return e.Bytes(), nil
}