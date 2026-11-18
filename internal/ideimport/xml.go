package ideimport

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// maxFileSize bounds the XML files read (configuration files are small;
// a huge file is not a settings file).
const maxFileSize = 8 << 20

// Element is a parsed XML element: name, attributes, children and the
// character data directly inside it. Attribute values are kept verbatim
// (macros are resolved by the reader with Macros.Expand).
type Element struct {
	Name     string
	Attrs    []xml.Attr
	Children []*Element
	Text     string
}

// Attr returns the value of attribute name ("" if missing).
func (e *Element) Attr(name string) string {
	v, _ := e.LookupAttr(name)
	return v
}

// LookupAttr returns the value of attribute name and whether it exists.
func (e *Element) LookupAttr(name string) (string, bool) {
	if e == nil {
		return "", false
	}
	for _, a := range e.Attrs {
		if a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

// Child returns the first child element called name, or nil.
func (e *Element) Child(name string) *Element {
	if e == nil {
		return nil
	}
	for _, c := range e.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// All returns the child elements called name.
func (e *Element) All(name string) []*Element {
	if e == nil {
		return nil
	}
	var out []*Element
	for _, c := range e.Children {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// Find returns the descendants (at any depth, document order, e
// excluded) called name.
func (e *Element) Find(name string) []*Element {
	var out []*Element
	e.Walk(func(x *Element) bool {
		if x != e && x.Name == name {
			out = append(out, x)
		}
		return true
	})
	return out
}

// Walk calls fn for e and its descendants in document order; returning
// false skips the children of that element.
func (e *Element) Walk(fn func(*Element) bool) {
	if e == nil || !fn(e) {
		return
	}
	for _, c := range e.Children {
		c.Walk(fn)
	}
}

// Option returns the value of the child <option name="NAME" value="…"/>
// (the common key/value form of the reference IDE's files). The second
// result reports whether the option exists.
func (e *Element) Option(name string) (string, bool) {
	if e == nil {
		return "", false
	}
	for _, c := range e.Children {
		if c.Name == "option" && c.Attr("name") == name {
			if v, ok := c.LookupAttr("value"); ok {
				return v, true
			}
			return strings.TrimSpace(c.Text), true
		}
	}
	return "", false
}

// OptionElement returns the child <option name="NAME"> element itself
// (for options with nested values such as maps and lists), or nil.
func (e *Element) OptionElement(name string) *Element {
	if e == nil {
		return nil
	}
	for _, c := range e.Children {
		if c.Name == "option" && c.Attr("name") == name {
			return c
		}
	}
	return nil
}

// Components returns the <component name="…"> children of a file's root
// element (application and project files have this form).
func (e *Element) Components() []*Element { return e.All("component") }

// Component returns the component called name, or nil.
func (e *Element) Component(name string) *Element {
	for _, c := range e.Components() {
		if c.Attr("name") == name {
			return c
		}
	}
	return nil
}

// Parse reads an XML document and returns its root element.
func Parse(r io.Reader) (*Element, error) {
	d := xml.NewDecoder(r)
	d.Strict = false
	d.CharsetReader = func(charset string, in io.Reader) (io.Reader, error) {
		// The files are UTF-8; other declared charsets are read as is.
		return in, nil
	}
	var stack []*Element
	var root *Element
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ideimport: xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			el := &Element{Name: t.Name.Local, Attrs: append([]xml.Attr(nil), t.Attr...)}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, el)
			} else if root == nil {
				root = el
			}
			stack = append(stack, el)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		}
	}
	if root == nil {
		return nil, errors.New("ideimport: xml: no root element")
	}
	return root, nil
}

// ParseFile reads and parses an XML file (at most 8 MiB).
func ParseFile(path string) (*Element, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("ideimport: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("ideimport: %s: %w", path, err)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("ideimport: %s: larger than %d bytes", path, maxFileSize)
	}
	root, err := Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return root, nil
}
