package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
)

// mergeJSON applies a partial JSON update to a copy of cur. Every field named
// in the body is replaced as a whole, so a list or map sent without an entry
// loses that entry; fields left out keep their values. cur itself is never
// touched, so it still describes the state before the change.
func mergeJSON[T any](cur *T, r *http.Request) (*T, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(body, &present); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	b, err := json.Marshal(cur)
	if err != nil {
		return nil, err
	}
	out := new(T)
	if err := json.Unmarshal(b, out); err != nil { // a deep copy: nothing is shared with cur
		return nil, err
	}
	v := reflect.ValueOf(out).Elem()
	for i := 0; i < v.NumField(); i++ {
		name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		if _, ok := present[name]; ok && name != "" && name != "-" {
			v.Field(i).SetZero()
		}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	return out, nil
}
