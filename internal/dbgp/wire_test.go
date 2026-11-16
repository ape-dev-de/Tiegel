package dbgp

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func TestReadPacket(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("5\x00<a/> \x00 3\x00<b>\x00"))
	p, err := readPacket(r)
	if err != nil || string(p) != "<a/> " {
		t.Fatalf("first = %q, %v", p, err)
	}
	if p, err = readPacket(r); err != nil || string(p) != "<b>" {
		t.Fatalf("second = %q, %v", p, err)
	}
	if _, err = readPacket(r); err != io.EOF {
		t.Fatalf("eof = %v", err)
	}
	for _, in := range []string{"x\x00", "-1\x00", "999999999999\x00", "2\x00abc", "2\x00a"} {
		if _, err := readPacket(bufio.NewReader(strings.NewReader(in))); err == nil {
			t.Errorf("%q: want error", in)
		}
	}
}

func TestEncodeCommand(t *testing.T) {
	got, err := encodeCommand("property_get", 7, []string{"-n", `$a["x y"]`, "-d", "0", "-e", ""}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `property_get -i 7 -n "$a[\"x y\"]" -d 0 -e ""` + "\x00"
	if string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
	c, err := parseFakeCmd(strings.TrimSuffix(string(got), "\x00"))
	if err != nil || c.Args["-n"] != `$a["x y"]` || c.TxID != "7" {
		t.Errorf("roundtrip = %+v, %v", c, err)
	}

	got, _ = encodeCommand("eval", 1, nil, []byte("1+1"))
	if string(got) != "eval -i 1 -- MSsx\x00" {
		t.Errorf("data = %q", got)
	}
	if q := quoteArg(`C:\dir`); q != `"C:\\dir"` {
		t.Errorf("quote = %s", q)
	}

	bad := []struct {
		name string
		args []string
	}{
		{"", nil},
		{"a b", nil},
		{"run", []string{"-n"}},
		{"run", []string{"-i", "3"}},
		{"run", []string{"name", "3"}},
		{"run", []string{"--", "3"}},
		{"run", []string{"-n", "a\x00b"}},
	}
	for _, b := range bad {
		if _, err := encodeCommand(b.name, 1, b.args, nil); err == nil {
			t.Errorf("%q %v: want error", b.name, b.args)
		}
	}
}

func TestDecodePacketVariants(t *testing.T) {
	if _, _, err := decodePacket([]byte("")); err == nil {
		t.Error("empty: want error")
	}
	name, _, err := decodePacket([]byte(`<?xml version="1.0" encoding="iso-8859-1"?><other/>`))
	if err == nil || name != "other" {
		t.Errorf("other = %q, %v", name, err)
	}
	if decodeText("!!!", "base64") != "!!!" {
		t.Error("invalid base64 should be returned raw")
	}
	if atoi(" 12 ") != 12 || atoi("x") != 0 {
		t.Error("atoi")
	}
	var nilEnc *xmlEncoded
	if nilEnc.decode() != "" {
		t.Error("nil decode")
	}
	// property_value wrapped in a property element.
	_, v, err := decodePacket([]byte(`<response command="property_value" transaction_id="1"><property type="int"><![CDATA[5]]></property></response>`))
	if err != nil {
		t.Fatal(err)
	}
	r := v.(*xmlResponse)
	if p := convertProperty(&r.Properties[0]); p.Value != "5" {
		t.Errorf("value = %q", p.Value)
	}
	// Extended property with a <value> element.
	_, v, _ = decodePacket([]byte(`<response><property type="string"><name encoding="base64"><![CDATA[JHg=]]></name><value encoding="base64"><![CDATA[w6Q=]]></value></property></response>`))
	if p := convertProperty(&v.(*xmlResponse).Properties[0]); p.Name != "$x" || p.Value != "ä" {
		t.Errorf("extended = %+v", p)
	}
	var nilProp *Property
	if nilProp.HasMorePages() {
		t.Error("nil HasMorePages")
	}
}

func TestErrorString(t *testing.T) {
	e := &Error{Command: "run", Code: 5}
	if e.Error() != "dbgp: run failed with error 5" {
		t.Error(e.Error())
	}
}
