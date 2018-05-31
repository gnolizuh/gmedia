package amf

import (
	"testing"
	"fmt"
)

func TestEncodeInt(t *testing.T) {
	in := uint32(20)
	b, err := Encode(in)
	if err != nil {
		t.Error(err)
		return
	}

	var out uint32
	err = Decode(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	if in == out {
		fmt.Printf("passed: %d\n", out)
	} else {
		t.Error("failed!")
	}
}

func TestEncodeString(t *testing.T) {
	in := "20"
	b, err := Encode(in)
	if err != nil {
		t.Error(err)
		return
	}

	var out string
	err = Decode(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	if in == out {
		fmt.Printf("passed: %s\n", out)
	} else {
		t.Error("failed!")
	}
}

func TestEncodeBool(t *testing.T) {
	in := true
	b, err := Encode(in)
	if err != nil {
		t.Error(err)
		return
	}

	var out bool
	err = Decode(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	if out {
		fmt.Printf("passed: %t\n", out)
	} else {
		t.Error("failed!")
	}
}

type AMFStruct struct {
	Int    int
	String string
	Bool   bool
}

func TestEncodeStruct(t *testing.T) {
	in := AMFStruct{
		Int: 10,
		String: "1",
		Bool: true,
	}

	b, err := Encode(in)
	if err != nil {
		t.Error(err)
		return
	}

	out := AMFStruct{}
	err = Decode(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	if in == out {
		fmt.Printf("passed: %v\n", out)
	} else {
		t.Error("failed!")
	}
}

func TestEncodeStruct2Map(t *testing.T) {
	in := AMFStruct{
		Int: 10,
		String: "1",
		Bool: true,
	}

	b, err := Encode(in)
	if err != nil {
		t.Error(err)
		return
	}

	out := make(map[string]interface{}, 4)
	err = Decode(b, &out)
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Printf("passed: %v\n", out)
}

func TestEncodeVars(t *testing.T) {
	in_name := "publish"
	in_num := 10
	in_obj := AMFStruct{
		Int: 10,
		String: "1",
		Bool: true,
	}

	b, err := Encode(&in_name, &in_num, &in_obj)
	if err != nil {
		t.Error(err)
		return
	}

	var out_name string
	var out_num int
	out_obj := make(map[string]interface{}, 4)
	err = Decode(b, &out_name, &out_num, &out_obj)
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Printf("passed: %s %d %v\n", out_name, out_num, out_obj)
}