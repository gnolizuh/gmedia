package amf

import (
	"bytes"
	"runtime"
	"reflect"
	"sync"
)

type encodeState struct {
	bytes.Buffer
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

	f = newTypeEncoder(t, true)
	wg.Done()
	encoderCache.Store(t, f)
	return f
}

func newTypeEncoder(t reflect.Type, allowAddr bool) encoderFunc {
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

func boolEncoder(e *encodeState, v reflect.Value) {
}

func intEncoder(e *encodeState, v reflect.Value) {
}

func uintEncoder(e *encodeState, v reflect.Value) {
}

func stringEncoder(e *encodeState, v reflect.Value) {
}

func floatEncoder(e *encodeState, v reflect.Value) {
}

func interfaceEncoder(e *encodeState, v reflect.Value) {
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