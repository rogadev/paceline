package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// member is one top-level key of settings.json with its raw value.
type member struct {
	key   string
	value json.RawMessage
}

// object is a JSON object that keeps its keys in file order. Unmarshalling
// into a Go map would re-sort every key in the user's settings on write;
// this keeps the file's order and leaves each value's bytes untouched.
type object struct{ members []member }

var errNotObject = errors.New("expected a JSON object at the top level")

func parseObject(data []byte) (*object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return &object{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil {
		return nil, err
	} else if tok != json.Delim('{') {
		return nil, errNotObject
	}
	o := &object{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.members = append(o.members, member{key, raw})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after the top-level object")
	}
	return o, nil
}

func (o *object) get(key string) (json.RawMessage, bool) {
	for _, m := range o.members {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// set replaces key in place, or appends it if absent.
func (o *object) set(key string, value json.RawMessage) {
	for i, m := range o.members {
		if m.key == key {
			o.members[i].value = value
			return
		}
	}
	o.members = append(o.members, member{key, value})
}

func (o *object) remove(key string) {
	kept := o.members[:0]
	for _, m := range o.members {
		if m.key != key {
			kept = append(kept, m)
		}
	}
	o.members = kept
}

// marshal writes the object with two-space indentation and a final newline.
func (o *object) marshal() ([]byte, error) {
	var compact bytes.Buffer
	compact.WriteByte('{')
	for i, m := range o.members {
		if i > 0 {
			compact.WriteByte(',')
		}
		key, err := marshalNoEscape(m.key)
		if err != nil {
			return nil, err
		}
		compact.Write(key)
		compact.WriteByte(':')
		if err := json.Compact(&compact, m.value); err != nil {
			return nil, err
		}
	}
	compact.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// marshalNoEscape encodes v without escaping <, >, and & (json.Marshal turns
// them into < and friends, which would needlessly rewrite user values).
func marshalNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
