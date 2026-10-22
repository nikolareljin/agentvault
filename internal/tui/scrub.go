package tui

import (
	"reflect"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// scrubESC returns a deep copy of v with every ESC in its strings replaced
// by '?'. View renders from such a copy, so after rendering the only
// escape sequences left are the ones the styles wrote: an SGR code from a
// bundle (black on black, conceal) cannot hide or fake what a field says.
func scrubESC[T any](v T) T {
	rv := reflect.ValueOf(&v).Elem()
	out := scrubValue(rv, 0)
	return out.Interface().(T)
}

func scrubValue(v reflect.Value, depth int) reflect.Value {
	if depth > 64 {
		return v
	}
	switch v.Kind() {
	case reflect.String:
		if !strings.Contains(v.String(), "\x1b") {
			return v
		}
		return reflect.ValueOf(strings.ReplaceAll(v.String(), "\x1b", "?")).Convert(v.Type())
	case reflect.Struct:
		n := reflect.New(v.Type()).Elem()
		n.Set(v)
		for i := 0; i < n.NumField(); i++ {
			if f := n.Field(i); f.CanSet() {
				f.Set(scrubValue(v.Field(i), depth+1))
			}
		}
		return n
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		n := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			n.Index(i).Set(scrubValue(v.Index(i), depth+1))
		}
		return n
	case reflect.Array:
		n := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			n.Index(i).Set(scrubValue(v.Index(i), depth+1))
		}
		return n
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		n := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			n.SetMapIndex(scrubValue(iter.Key(), depth+1), scrubValue(iter.Value(), depth+1))
		}
		return n
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		n := reflect.New(v.Type().Elem())
		n.Elem().Set(scrubValue(v.Elem(), depth+1))
		return n
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		n := reflect.New(v.Type()).Elem()
		n.Set(scrubValue(v.Elem(), depth+1))
		return n
	}
	return v
}

// displayCopy is m with every field that can hold vault, bundle, agent or
// command output text passed through scrubESC.
func (m model) displayCopy() model {
	m.agents = scrubESC(m.agents)
	m.shared = scrubESC(m.shared)
	m.instructions = scrubESC(m.instructions)
	m.detected = scrubESC(m.detected)
	m.providerCfgs = scrubESC(m.providerCfgs)
	m.statusReport = scrubESC(m.statusReport)
	m.localInst = scrubESC(m.localInst)
	m.quickCommands = scrubESC(m.quickCommands)
	for _, s := range []*string{&m.statusErr, &m.searchQuery, &m.commandQuery,
		&m.lastCommand, &m.gatewayPrompt, &m.gatewayWorkspace,
		&m.gatewayWorkSource, &m.gatewayEffective, &m.gatewayProfile,
		&m.gatewayResponse, &m.gatewayErr, &m.deleteTarget, &m.statusMsg, &m.cwd} {
		*s = scrubESC(*s)
	}
	return m
}

// displaySessions is the vault's sessions for rendering only.
func (m model) displaySessions() agent.SessionConfig {
	return scrubESC(m.vault.Sessions())
}
