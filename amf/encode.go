package amf

import (
	"bytes"
	"runtime"
	"reflect"
	"sync"
	"encoding/binary"
	"sync/atomic"
	"sort"
	"unicode"
	"strings"
	"strconv"
)

type EncoderError struct {
	Type reflect.Type
	Err  error
}

func (e *EncoderError) Error() string {
	return "amf: error calling EncodeAMF for type " + e.Type.String() + ": " + e.Err.Error()
}

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

func (e *encodeState) encodeError(err error) {
	panic(err)
}

func (e *encodeState) reflectValue(v reflect.Value) {
	valueEncoder(v)(e, v)
}

func (e *encodeState) encodeObjectName(s string) {
	e.encodeUint(uint16(len(s)))
	e.Write([]byte(s))
}

func (e *encodeState) encodeMarker(m byte) {
	e.WriteByte(m)
}

type reflectWithString struct {
	v reflect.Value
	s string
}

func (w *reflectWithString) resolve() error {
	if w.v.Kind() == reflect.String {
		w.s = w.v.String()
		return nil
	}
	switch w.v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		w.s = strconv.FormatInt(w.v.Int(), 10)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		w.s = strconv.FormatUint(w.v.Uint(), 10)
		return nil
	}
	panic("unexpected map key type")
}

func (e *encodeState) encodeString(s string, marker bool) {
	l := len(s)
	if l > LongStringSize {
		if marker { e.encodeMarker(LongStringMarker) }
		e.encodeUint(uint32(l))
	} else {
		if marker { e.encodeMarker(StringMarker) }
		e.encodeUint(uint16(l))
	}
	e.Write([]byte(s))
}

func (e *encodeState) encodeUint(u interface{}) {
	binary.Write(e, binary.BigEndian, u)
}

func (e *encodeState) encodeBool(b bool) {
	e.encodeMarker(BooleanMarker)
	if b {
		e.WriteByte(1)
	} else {
		e.WriteByte(0)
	}
}

func (e *encodeState) encodeFloat64(f float64) {
	e.encodeMarker(NumberMarker)
	binary.Write(e, binary.BigEndian, f)
}

func (e *encodeState) encodeNull() {
	e.WriteByte(NullMarker)
}

type encoderFunc func(e *encodeState, v reflect.Value)

var encoderCache sync.Map

func valueEncoder(v reflect.Value) encoderFunc {
	if !v.IsValid() {
		return invalidValueEncoder
	}
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
	case reflect.Struct: // struct & map are type of Object Marker.
		return newStructEncoder(t)
	case reflect.Map:
		return newMapEncoder(t)
	case reflect.Slice:  // slice & array are type of Strict Array Marker.
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
	e.encodeString(v.String(), true)
}

func boolEncoder(e *encodeState, v reflect.Value) {
	e.encodeBool(v.Bool())
}

func invalidValueEncoder(e *encodeState, _ reflect.Value) {
	e.encodeNull()
}

func intEncoder(e *encodeState, v reflect.Value) {
	e.encodeFloat64(float64(v.Int()))
}

func uintEncoder(e *encodeState, v reflect.Value) {
	e.encodeFloat64(float64(v.Uint()))
}

func floatEncoder(e *encodeState, v reflect.Value) {
	e.encodeFloat64(v.Float())
}

func interfaceEncoder(e *encodeState, v reflect.Value) {
	if v.IsNil() {
		e.encodeNull()
		return
	}
	e.reflectValue(v.Elem())
}

func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:<=>?@[]^_{|}~ ", c):
		default:
			if !unicode.IsLetter(c) && !unicode.IsDigit(c) {
				return false
			}
		}
	}
	return true
}

func fieldByIndex(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}

func typeByIndex(t reflect.Type, index []int) reflect.Type {
	for _, i := range index {
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		t = t.Field(i).Type
	}
	return t
}

type structEncoder struct {
	fields    []field
	fieldEncs []encoderFunc
}

// marker: 1 byte 0x03
// format:
// - loop encoded string followed by encoded value
// - terminated with empty string followed by 1 byte 0x09
func (se *structEncoder) encode(e *encodeState, v reflect.Value) {
	e.encodeMarker(ObjectMarker)
	for i, f := range se.fields {
		fv := fieldByIndex(v, f.index)
		if !fv.IsValid() {
			continue
		}
		e.encodeString(f.name, false)
		se.fieldEncs[i](e, fv)
	}
	e.encodeString("", false)
	e.encodeMarker(ObjectEndMarker)
}

func newStructEncoder(t reflect.Type) encoderFunc {
	fields := cachedTypeFields(t)
	se := &structEncoder{
		fields:    fields,
		fieldEncs: make([]encoderFunc, len(fields)),
	}
	for i, f := range fields {
		se.fieldEncs[i] = typeEncoder(typeByIndex(t, f.index))
	}
	return se.encode
}

type mapEncoder struct {
	elemEnc encoderFunc
}

func (mae *mapEncoder) encode(e *encodeState, v reflect.Value) {
	if v.IsNil() {
		e.encodeNull()
		return
	}

	keys := v.MapKeys()
	sv := make([]reflectWithString, len(keys))
	for i, v := range keys {
		sv[i].v = v
		if err := sv[i].resolve(); err != nil {
			e.encodeError(&EncoderError{v.Type(), err})
		}
	}
	sort.Slice(sv, func(i, j int) bool { return sv[i].s < sv[j].s })

	e.encodeMarker(ObjectMarker)
	for _, kv := range sv {
		e.encodeString(kv.s, false)
		mae.elemEnc(e, v.MapIndex(kv.v))
	}
	e.encodeString("", false)
	e.encodeMarker(ObjectEndMarker)
}

func newMapEncoder(t reflect.Type) encoderFunc {
	switch t.Key().Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
	default:
		return unsupportedTypeEncoder
	}
	mae := &mapEncoder{typeEncoder(t.Elem())}
	return mae.encode
}

type sliceEncoder struct {
	arrayEnc encoderFunc
}

func (se *sliceEncoder) encode(e *encodeState, v reflect.Value) {
	if v.IsNil() {
		e.encodeNull()
		return
	}
	se.arrayEnc(e, v)
}

func newSliceEncoder(t reflect.Type) encoderFunc {
	enc := &sliceEncoder{newArrayEncoder(t)}
	return enc.encode
}

type arrayEncoder struct {
	elemEnc encoderFunc
}

// marker: 1 byte 0x0a
// format:
// - 4 byte big endian uint32 to determine length of associative array
// - n (length) encoded values
func (ae *arrayEncoder) encode(e *encodeState, v reflect.Value) {
	n := v.Len()
	if n == 0 {
		return
	}

	e.encodeMarker(StrictArrayMarker)
	e.encodeUint(uint32(n))
	for i := 0; i < n; i++ {
		ae.elemEnc(e, v.Index(i))
	}
}

func newArrayEncoder(t reflect.Type) encoderFunc {
	enc := &arrayEncoder{typeEncoder(t.Elem())}
	return enc.encode
}

type ptrEncoder struct {
	elemEnc encoderFunc
}

func (pe *ptrEncoder) encode(e *encodeState, v reflect.Value) {
	if v.IsNil() {
		e.encodeNull()
		return
	}
	pe.elemEnc(e, v.Elem())
}

func newPtrEncoder(t reflect.Type) encoderFunc {
	enc := &ptrEncoder{typeEncoder(t.Elem())}
	return enc.encode
}

func unsupportedTypeEncoder(e *encodeState, v reflect.Value) {
	e.encodeError(&UnsupportedTypeError{v.Type()})
}

type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return "amf: unsupported type: " + e.Type.String()
}

func Encode(v interface{}) ([]byte, error) {
	e := &encodeState{}
	err := e.encode(v)
	if err != nil {
		return nil, err
	}
	return e.Bytes(), nil
}

// A field represents a single field found in a struct.
type field struct {
	name      string
	nameBytes []byte                 // []byte(name)

	tag       bool
	index     []int
	typo      reflect.Type
}

func fillField(f field) field {
	f.nameBytes = []byte(f.name)
	return f
}

// byIndex sorts field by index sequence.
type byIndex []field

func (x byIndex) Len() int { return len(x) }

func (x byIndex) Swap(i, j int) { x[i], x[j] = x[j], x[i] }

func (x byIndex) Less(i, j int) bool {
	for k, xik := range x[i].index {
		if k >= len(x[j].index) {
			return false
		}
		if xik != x[j].index[k] {
			return xik < x[j].index[k]
		}
	}
	return len(x[i].index) < len(x[j].index)
}

func typeFields(t reflect.Type) []field {
	current := []field{}
	next := []field{{typo: t}}

	count := map[reflect.Type]int{}
	nextCount := map[reflect.Type]int{}

	visited := map[reflect.Type]bool{}

	var fields []field

	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}

		for _, f := range current {
			if visited[f.typo] {
				continue
			}
			visited[f.typo] = true

			for i := 0; i < f.typo.NumField(); i++ {
				sf := f.typo.Field(i)
				if sf.PkgPath != "" {
					continue
				}
				tag := sf.Tag.Get("amf")
				if tag == "-" {
					continue
				}
				name := tag
				if !isValidTag(name) {
					name = ""
				}
				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i

				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Ptr {
					ft = ft.Elem()
				}

				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = sf.Name
					}
					fields = append(fields, fillField(field{
						name:      name,
						tag:       tagged,
						index:     index,
						typo:      ft,
					}))
					if count[f.typo] > 1 {
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}

				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, fillField(field{name: ft.Name(), index: index, typo: ft}))
				}
			}
		}
	}

	sort.Slice(fields, func(i, j int) bool {
		x := fields
		if x[i].name != x[j].name {
			return x[i].name < x[j].name
		}
		if len(x[i].index) != len(x[j].index) {
			return len(x[i].index) < len(x[j].index)
		}
		if x[i].tag != x[j].tag {
			return x[i].tag
		}
		return byIndex(x).Less(i, j)
	})

	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		fi := fields[i]
		name := fi.name
		for advance = 1; i+advance < len(fields); advance++ {
			fj := fields[i+advance]
			if fj.name != name {
				break
			}
		}
		if advance == 1 { // Only one field with this name
			out = append(out, fi)
			continue
		}
		dominant, ok := dominantField(fields[i : i+advance])
		if ok {
			out = append(out, dominant)
		}
	}

	fields = out
	sort.Sort(byIndex(fields))

	return fields
}

func dominantField(fields []field) (field, bool) {
	length := len(fields[0].index)
	tagged := -1 // Index of first tagged field.
	for i, f := range fields {
		if len(f.index) > length {
			fields = fields[:i]
			break
		}
		if f.tag {
			if tagged >= 0 {
				return field{}, false
			}
			tagged = i
		}
	}

	if tagged >= 0 {
		return fields[tagged], true
	}

	if len(fields) > 1 {
		return field{}, false
	}
	return fields[0], true
}

var fieldCache struct {
	value atomic.Value // map[reflect.Type][]field
	mu    sync.Mutex   // used only by writers
}

func cachedTypeFields(t reflect.Type) []field {
	m, _ := fieldCache.value.Load().(map[reflect.Type][]field)
	f := m[t]
	if f != nil {
		return f
	}

	f = typeFields(t)
	if f == nil {
		f = []field{}
	}

	fieldCache.mu.Lock()
	m, _ = fieldCache.value.Load().(map[reflect.Type][]field)
	newM := make(map[reflect.Type][]field, len(m)+1)
	for k, v := range m {
		newM[k] = v
	}
	newM[t] = f
	fieldCache.value.Store(newM)
	fieldCache.mu.Unlock()
	return f
}
