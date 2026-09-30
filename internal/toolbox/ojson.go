package toolbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// object is a JSON object that remembers key order, so editing a user's
// agent config changes only the keys Relay touches and the diff stays
// minimal. Values are *object, []any, string, json.Number, bool or nil.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *object) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) bool {
	if _, ok := o.vals[k]; !ok {
		return false
	}
	delete(o.vals, k)
	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
	return true
}

// child returns the object at k, creating it when missing. It fails when
// k holds something that is not an object.
func (o *object) child(k string) (*object, error) {
	v, ok := o.vals[k]
	if !ok || v == nil {
		c := newObject()
		o.set(k, c)
		return c, nil
	}
	c, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("%q is not an object", k)
	}
	return c, nil
}

// parseOrdered decodes a JSON document whose top level must be an object.
// Empty input yields an empty object.
func parseOrdered(b []byte) (*object, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return newObject(), nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON document")
	}
	o, ok := v.(*object)
	if !ok {
		return nil, errors.New("top level is not a JSON object")
	}
	return o, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := newObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("object key is not a string")
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.set(k, v)
			}
			if _, err := dec.Token(); err != nil { // '}'
				return nil, err
			}
			return o, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil { // ']'
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	default:
		return t, nil
	}
}

// encodeOrdered writes v as indented JSON (two spaces) with a trailing
// newline.
func encodeOrdered(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := writeValue(&b, v, 0); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func writeValue(b *bytes.Buffer, v any, depth int) error {
	switch t := v.(type) {
	case *object:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{\n")
		for i, k := range t.keys {
			indent(b, depth+1)
			writeString(b, k)
			b.WriteString(": ")
			if err := writeValue(b, t.vals[k], depth+1); err != nil {
				return err
			}
			if i < len(t.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(b, depth)
		b.WriteByte('}')
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[\n")
		for i, e := range t {
			indent(b, depth+1)
			if err := writeValue(b, e, depth+1); err != nil {
				return err
			}
			if i < len(t)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(b, depth)
		b.WriteByte(']')
	case []string:
		arr := make([]any, len(t))
		for i, s := range t {
			arr[i] = s
		}
		return writeValue(b, arr, depth)
	case map[string]string:
		o := newObject()
		for _, k := range sortedKeys(t) {
			o.set(k, t[k])
		}
		return writeValue(b, o, depth)
	case string:
		writeString(b, t)
	case json.Number:
		b.WriteString(t.String())
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	default:
		return fmt.Errorf("unsupported JSON value %T", v)
	}
	return nil
}

func writeString(b *bytes.Buffer, s string) {
	var sb bytes.Buffer
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b.Write(bytes.TrimRight(sb.Bytes(), "\n"))
}

func indent(b *bytes.Buffer, depth int) { b.WriteString(strings.Repeat("  ", depth)) }

// stripJSONComments removes // and /* */ comments outside strings (JSONC).
// It reports whether any comment was found.
func stripJSONComments(b []byte) ([]byte, bool) {
	var out bytes.Buffer
	found := false
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out.WriteByte(c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '/' {
			found = true
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out.WriteByte('\n')
			}
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '*' {
			found = true
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++ // skip '/'
			continue
		}
		out.WriteByte(c)
	}
	return out.Bytes(), found
}
