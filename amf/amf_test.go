package amf

import (
	"github.com/mitchellh/mapstructure"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestInt(t *testing.T) {
	in := 1
	b, err := Marshal(in)
	if err != nil {
		t.Error(err)
		return
	}

	var out int
	err = Unmarshal(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	require.Equal(t, in, out)
}

func TestUInt(t *testing.T) {
	in := uint(1)
	b, err := Marshal(in)
	if err != nil {
		t.Error(err)
		return
	}

	var out uint
	err = Unmarshal(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	require.Equal(t, in, out)
}

func TestFloat(t *testing.T) {
	in := 1.0
	b, err := Marshal(in)
	if err != nil {
		t.Error(err)
		return
	}

	var out float64
	err = Unmarshal(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	require.Equal(t, in, out)
}

func TestString(t *testing.T) {
	in := "1"
	b, err := Marshal(in)
	if err != nil {
		t.Error(err)
		return
	}

	var out string
	err = Unmarshal(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	require.Equal(t, in, out)
}

func TestBool(t *testing.T) {
	in := true
	b, err := Marshal(&in)
	if err != nil {
		t.Error(err)
		return
	}

	out := false
	err = Unmarshal(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	require.Equal(t, in, out)
}

// TestStruct1 make struct as input and output type.
func TestStruct2Struct(t *testing.T) {
	type Struct struct {
		Int    int    `amf:"tag_int"`
		String string `amf:"tag_string"`
		Bool   bool   `amf:"tag_bool"`
		Object struct {
			Int    int    `amf:"tag_int"`
			String string `amf:"tag_string"`
			Bool   bool   `amf:"tag_bool"`
		} `amf:"tag_object"`
	}

	in := Struct{Int: 1, String: "1", Bool: true}
	in.Object.Int = 1
	in.Object.String = "1"
	in.Object.Bool = true

	b, err := Marshal(in)
	if err != nil {
		t.Error(err)
		return
	}

	out := Struct{}
	err = Unmarshal(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	require.Equal(t, in, out)
}

// TestStruct2 make struct as input type and map as output type.
func TestStruct2Map(t *testing.T) {
	type Struct struct {
		Int    int    `amf:"tag_int"`
		String string `amf:"tag_string"`
		Bool   bool   `amf:"tag_bool"`
		Object struct {
			Int    int    `amf:"tag_int"`
			String string `amf:"tag_string"`
			Bool   bool   `amf:"tag_bool"`
		} `amf:"tag_object"`
	}

	in := Struct{Int: 1, String: "1", Bool: true}
	in.Object.Int = 1
	in.Object.String = "1"
	in.Object.Bool = true

	b, err := Marshal(in)
	if err != nil {
		t.Error(err)
		return
	}

	m := make(map[string]interface{})
	err = Unmarshal(b, &m)
	if err != nil {
		t.Error(err)
		return
	}

	out := Struct{}
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{TagName: "amf", Result: &out})
	if err != nil {
		t.Error(err)
		return
	}

	err = decoder.Decode(m)
	if err != nil {
		t.Error(err)
		return
	}

	require.Equal(t, in, out)
}

// TestStruct3 make map as input type and struct as output type.
func TestMap2Struct(t *testing.T) {
	type Struct struct {
		Int    int    `amf:"tag_int"`
		String string `amf:"tag_string"`
		Bool   bool   `amf:"tag_bool"`
		Object struct {
			Int    int    `amf:"tag_int"`
			String string `amf:"tag_string"`
			Bool   bool   `amf:"tag_bool"`
		} `amf:"tag_object"`
	}

	m := map[string]interface{}{
		"tag_int":    1,
		"tag_string": "1",
		"tag_bool":   true,
		"tag_object": map[string]interface{}{
			"tag_int":    1,
			"tag_string": "1",
			"tag_bool":   true,
		},
	}
	b, err := Marshal(m)
	if err != nil {
		t.Error(err)
		return
	}

	out := Struct{}
	err = Unmarshal(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	in := Struct{}
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{TagName: "amf", Result: &in})
	if err != nil {
		t.Error(err)
		return
	}

	err = decoder.Decode(m)
	if err != nil {
		t.Error(err)
		return
	}

	require.Equal(t, in, out)
}

func TestSlice(t *testing.T) {
	in := []interface{}{1.0, "1", true, map[string]interface{}{"Int": 1.0, "String": "1", "Bool": true}}
	b, err := Marshal(in)
	if err != nil {
		t.Error(err)
		return
	}

	out := []interface{}{0.0, "0", false, map[string]interface{}{}}
	err = Unmarshal(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	require.Equal(t, in, out)
}
