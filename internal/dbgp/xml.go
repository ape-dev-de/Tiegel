package dbgp

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
)

// Raw XML structures of engine packets. Struct tags carry no namespace, so
// they match both plain and "xdebug:"-prefixed elements and attributes.

type xmlInit struct {
	FileURI         string    `xml:"fileuri,attr"`
	IDEKey          string    `xml:"idekey,attr"`
	AppID           string    `xml:"appid,attr"`
	Session         string    `xml:"session,attr"`
	Thread          string    `xml:"thread,attr"`
	Parent          string    `xml:"parent,attr"`
	Language        string    `xml:"language,attr"`
	LanguageVersion string    `xml:"language_version,attr"`
	ProtocolVersion string    `xml:"protocol_version,attr"`
	Engine          xmlEngine `xml:"engine"`
}

type xmlEngine struct {
	Version string `xml:"version,attr"`
	Name    string `xml:",chardata"`
}

type xmlResponse struct {
	Command       string `xml:"command,attr"`
	TransactionID string `xml:"transaction_id,attr"`
	Status        string `xml:"status,attr"`
	Reason        string `xml:"reason,attr"`
	Success       string `xml:"success,attr"`
	Feature       string `xml:"feature,attr"`
	ID            string `xml:"id,attr"`
	Resolved      string `xml:"resolved,attr"`
	State         string `xml:"state,attr"`
	Type          string `xml:"type,attr"`
	Size          string `xml:"size,attr"`
	Encoding      string `xml:"encoding,attr"`
	Text          string `xml:",chardata"`

	Error       *xmlError       `xml:"error"`
	Message     *xmlMessage     `xml:"message"`
	Stack       []xmlStack      `xml:"stack"`
	Contexts    []xmlContext    `xml:"context"`
	Properties  []xmlProperty   `xml:"property"`
	Breakpoints []xmlBreakpoint `xml:"breakpoint"`

	raw []byte
}

type xmlError struct {
	Code    string `xml:"code,attr"`
	Message string `xml:"message"`
}

// xmlMessage is the <xdebug:message> element of continuation responses and
// error notifications.
type xmlMessage struct {
	Filename  string `xml:"filename,attr"`
	Lineno    string `xml:"lineno,attr"`
	Exception string `xml:"exception,attr"`
	Code      string `xml:"code,attr"`
	Type      string `xml:"type,attr"`
	Text      string `xml:",chardata"`
}

type xmlStack struct {
	Where    string `xml:"where,attr"`
	Level    string `xml:"level,attr"`
	Type     string `xml:"type,attr"`
	Filename string `xml:"filename,attr"`
	Lineno   string `xml:"lineno,attr"`
}

type xmlContext struct {
	Name string `xml:"name,attr"`
	ID   string `xml:"id,attr"`
}

type xmlEncoded struct {
	Encoding string `xml:"encoding,attr"`
	Text     string `xml:",chardata"`
}

type xmlProperty struct {
	Name        string `xml:"name,attr"`
	FullName    string `xml:"fullname,attr"`
	Type        string `xml:"type,attr"`
	ClassName   string `xml:"classname,attr"`
	Facet       string `xml:"facet,attr"`
	Size        string `xml:"size,attr"`
	Children    string `xml:"children,attr"`
	NumChildren string `xml:"numchildren,attr"`
	Page        string `xml:"page,attr"`
	PageSize    string `xml:"pagesize,attr"`
	Key         string `xml:"key,attr"`
	Encoding    string `xml:"encoding,attr"`
	Text        string `xml:",chardata"`

	// Elements used with the extended_properties feature.
	NameEl      *xmlEncoded `xml:"name"`
	FullNameEl  *xmlEncoded `xml:"fullname"`
	ClassNameEl *xmlEncoded `xml:"classname"`
	ValueEl     *xmlEncoded `xml:"value"`

	Properties []xmlProperty `xml:"property"`
}

type xmlBreakpoint struct {
	ID           string      `xml:"id,attr"`
	Type         string      `xml:"type,attr"`
	State        string      `xml:"state,attr"`
	Resolved     string      `xml:"resolved,attr"`
	Filename     string      `xml:"filename,attr"`
	Lineno       string      `xml:"lineno,attr"`
	Function     string      `xml:"function,attr"`
	Class        string      `xml:"class,attr"`
	Exception    string      `xml:"exception,attr"`
	HitValue     string      `xml:"hit_value,attr"`
	HitCondition string      `xml:"hit_condition,attr"`
	HitCount     string      `xml:"hit_count,attr"`
	Temporary    string      `xml:"temporary,attr"`
	Expression   *xmlEncoded `xml:"expression"`
}

type xmlNotify struct {
	Name       string         `xml:"name,attr"`
	Breakpoint *xmlBreakpoint `xml:"breakpoint"`
	Message    *xmlMessage    `xml:"message"`
	Text       string         `xml:",chardata"`
}

type xmlStream struct {
	Type     string `xml:"type,attr"`
	Encoding string `xml:"encoding,attr"`
	Text     string `xml:",chardata"`
}

// newDecoder returns an XML decoder that accepts any declared charset.
// Xdebug declares iso-8859-1 but its payload is ASCII/UTF-8 or base64.
func newDecoder(data []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return d
}

// decodePacket determines the root element of a packet and decodes it into
// the matching structure. It returns the root's local name and the value.
func decodePacket(data []byte) (string, any, error) {
	d := newDecoder(data)
	for {
		tok, err := d.Token()
		if err != nil {
			return "", nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		var v any
		switch se.Name.Local {
		case "init":
			v = &xmlInit{}
		case "response":
			v = &xmlResponse{raw: data}
		case "notify":
			v = &xmlNotify{}
		case "stream":
			v = &xmlStream{}
		default:
			return se.Name.Local, nil, errors.New("dbgp: unknown packet type " + se.Name.Local)
		}
		if err := d.DecodeElement(v, &se); err != nil {
			return se.Name.Local, nil, err
		}
		return se.Name.Local, v, nil
	}
}

// decodeText decodes element text according to its encoding attribute.
func decodeText(text, encoding string) string {
	if encoding != "base64" {
		return text
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return text
	}
	return string(b)
}

func (e *xmlEncoded) decode() string {
	if e == nil {
		return ""
	}
	return decodeText(e.Text, e.Encoding)
}

// atoi converts an attribute value to int, returning 0 for empty or invalid
// values.
func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

func trimSpace(s string) string { return strings.TrimSpace(s) }
