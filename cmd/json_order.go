package cmd

import (
	"bytes"
	"encoding/json"
	"io"
)

// jsonMember is one key and raw value of a JSON object, in file order.
type jsonMember struct {
	key string
	val json.RawMessage
}

// objectMembers are the members of a JSON object in file order.
func objectMembers(data []byte) ([]jsonMember, bool) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	var out []jsonMember
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		out = append(out, jsonMember{key, v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}

func rawJSONEqual(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var x, y any
	da := json.NewDecoder(bytes.NewReader(a))
	da.UseNumber()
	db := json.NewDecoder(bytes.NewReader(b))
	db.UseNumber()
	if da.Decode(&x) != nil || db.Decode(&y) != nil {
		return false
	}
	return jsonEqual(x, y)
}

// orderLikeLocal rewrites out, a JSON object built from a map, in the order
// of this machine's file (local): its keys first in its order, then keys
// new from the bundle in the bundle's order, then the rest. A value equal to
// this machine's keeps this machine's bytes. The servers in mcpServers and
// servers are ordered the same way. out is returned as it is when either
// side is not a JSON object.
func orderLikeLocal(out, local, bundle []byte) []byte {
	om, ok := objectMembers(out)
	if !ok {
		return out
	}
	lm, ok := objectMembers(local)
	if !ok || len(lm) == 0 {
		return out
	}
	bm, _ := objectMembers(bundle)
	var buf bytes.Buffer
	if err := json.Indent(&buf, orderMembers(om, lm, bm, true), "", "  "); err != nil {
		return out
	}
	return buf.Bytes()
}

func orderMembers(om, lm, bm []jsonMember, top bool) []byte {
	ov := map[string]json.RawMessage{}
	for _, m := range om {
		ov[m.key] = m.val
	}
	bv := map[string]json.RawMessage{}
	for _, m := range bm {
		bv[m.key] = m.val
	}
	var b bytes.Buffer
	b.WriteByte('{')
	done := map[string]bool{}
	emit := func(k string, v json.RawMessage) {
		if done[k] {
			return
		}
		done[k] = true
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(k)
		b.Truncate(b.Len() - 1)
		b.WriteByte(':')
		b.Write(v)
	}
	for _, m := range lm {
		v, ok := ov[m.key]
		if !ok || done[m.key] {
			continue
		}
		switch {
		case rawJSONEqual(v, m.val):
			v = m.val
		case top && (m.key == "mcpServers" || m.key == "servers"):
			so, ok1 := objectMembers(v)
			sl, ok2 := objectMembers(m.val)
			if ok1 && ok2 {
				sb, _ := objectMembers(bv[m.key])
				v = orderMembers(so, sl, sb, false)
			}
		}
		emit(m.key, v)
	}
	for _, m := range bm {
		if v, ok := ov[m.key]; ok {
			emit(m.key, v)
		}
	}
	for _, m := range om {
		emit(m.key, m.val)
	}
	b.WriteByte('}')
	return b.Bytes()
}
