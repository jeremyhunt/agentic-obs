package obs

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/andreykaipov/goobs"
)

// This file makes every obs-websocket request reachable without a typed wrapper
// for each one.
//
// obs-websocket 5.7.4 advertises 151 requests. This package wraps roughly 65 of
// them as typed methods, chosen because a workflow wanted them; the remaining
// eighty-odd are not unreachable by design, only unwritten. Writing them all
// would be eighty near-identical files that go stale the next time OBS adds a
// request, and Bitfocus Companion -- the most widely deployed OBS control
// surface there is -- concluded the same thing and shipped a "Custom Command"
// escape hatch next to its curated actions.
//
// So the registry is derived rather than declared, in the same spirit as the
// tool metadata in internal/mcp. goobs generates, per request, a params type
// carrying GetRequestName() and a response type embedding api.ResponseCommon
// whose exported GetRaw() holds the server's raw responseData. Both shapes are
// exported, so reflection over the category subclients reconstructs the whole
// request table using nothing private.
//
// What this does NOT reach: the four private-settings requests
// (Get/SetSourcePrivateSettings, Get/SetSceneItemPrivateSettings). OBS offers
// them and goobs does not generate them, so they need a goobs contribution or
// the Lua bridge -- and the live test names them, so the gap stays measured
// rather than remembered.

// requestEntry is one generated goobs request, ready to be called.
type requestEntry struct {
	// category is the subclient field name ("Scenes", "Inputs", ...), kept for
	// error messages and for reporting coverage per category.
	category string
	method   reflect.Method
	// params is the XxxParams struct type, not the pointer to it.
	params reflect.Type
}

// requestRegistry maps an obs-websocket request name onto the goobs method that
// issues it.
type requestRegistry struct {
	entries map[string]requestEntry
	names   []string
}

var (
	registryOnce sync.Once
	registry     *requestRegistry
)

// sharedRequestRegistry builds the registry once. It depends only on goobs'
// types, never on a connection, so it is safe to build before connecting and
// cheap to reuse.
func sharedRequestRegistry() *requestRegistry {
	registryOnce.Do(func() { registry = newRequestRegistry() })
	return registry
}

func newRequestRegistry() *requestRegistry {
	reg := &requestRegistry{entries: map[string]requestEntry{}}

	// Categories is an exported struct of exported subclient fields, so the
	// type is walkable without an instance.
	categories := reflect.TypeOf(goobs.Client{}).Field(0)
	if categories.Name != "Categories" {
		// Field order is not part of goobs' contract; find it by name.
		f, ok := reflect.TypeOf(goobs.Client{}).FieldByName("Categories")
		if !ok {
			return reg
		}
		categories = f
	}

	for i := 0; i < categories.Type.NumField(); i++ {
		field := categories.Type.Field(i)
		subclient := field.Type // *scenes.Client and friends

		for m := 0; m < subclient.NumMethod(); m++ {
			method := subclient.Method(m)
			entry, name, ok := describeRequestMethod(field.Name, method)
			if !ok {
				continue
			}
			reg.entries[name] = entry
		}
	}

	reg.names = make([]string, 0, len(reg.entries))
	for name := range reg.entries {
		reg.names = append(reg.names, name)
	}
	sort.Strings(reg.names)
	return reg
}

// describeRequestMethod recognises a generated request method and reads the
// request name out of its params type.
//
// goobs emits two shapes, and both must be accepted: a request with required
// fields takes `params *XxxParams`, while one whose fields are all optional
// takes `params ...*XxxParams`. Matching only the variadic form finds 78 of
// 147 requests and looks like a working registry, which is why the shape is
// stated here rather than inferred at the call site.
func describeRequestMethod(category string, method reflect.Method) (requestEntry, string, bool) {
	mt := method.Type
	if mt.NumIn() != 2 || mt.NumOut() != 2 {
		return requestEntry{}, "", false
	}

	paramsPtr := mt.In(1)
	if mt.IsVariadic() {
		paramsPtr = paramsPtr.Elem() // []*XxxParams -> *XxxParams
	}
	if paramsPtr.Kind() != reflect.Ptr || paramsPtr.Elem().Kind() != reflect.Struct {
		return requestEntry{}, "", false
	}

	named, ok := reflect.New(paramsPtr.Elem()).Interface().(interface{ GetRequestName() string })
	if !ok {
		return requestEntry{}, "", false
	}

	// The response must be able to hand back the raw server payload; without it
	// there is nothing to return to the caller.
	if _, ok := mt.Out(0).MethodByName("GetRaw"); !ok {
		return requestEntry{}, "", false
	}

	name := named.GetRequestName()
	if name == "" {
		return requestEntry{}, "", false
	}
	return requestEntry{category: category, method: method, params: paramsPtr.Elem()}, name, true
}

func (r *requestRegistry) lookup(requestType string) (requestEntry, error) {
	entry, ok := r.entries[requestType]
	if !ok {
		return requestEntry{}, fmt.Errorf(
			"unknown request type %q: obs-websocket request names are case-sensitive "+
				"and spelled like %q; call list_obs_requests to see the %d this build supports",
			requestType, "GetSceneItemList", len(r.entries))
	}
	return entry, nil
}

// deniedRequests are requests the passthrough refuses because a typed tool
// already covers them and asks first.
//
// The passthrough reaches everything, which includes the handful of requests
// whose tools exist specifically so a destructive act is confirmable. Routing
// around those while looking like a feature is the one failure mode this tool
// can introduce that the rest of the surface cannot, so the list is narrow and
// each entry names the tool to use instead.
var deniedRequests = map[string]string{
	"RemoveScene":        "remove_scene",
	"RemoveInput":        "remove_source",
	"RemoveSceneItem":    "remove_scene_item",
	"RemoveSourceFilter": "remove_source_filter",
	// No wrapper, and no way to undo from here: switching collections tears
	// down and rebuilds every source in OBS.
	"SetCurrentSceneCollection": "",
	"RemoveProfile":             "",
}

func checkRequestAllowed(requestType string) error {
	wrapper, denied := deniedRequests[requestType]
	if !denied {
		return nil
	}
	if wrapper == "" {
		return fmt.Errorf(
			"%s is not available through call_obs_request: it rebuilds or discards "+
				"state that cannot be recovered from here, so it is left to the operator",
			requestType)
	}
	return fmt.Errorf(
		"%s is not available through call_obs_request: use the %s tool, which confirms "+
			"before removing",
		requestType, wrapper)
}

// AvailableRequests lists every obs-websocket request this build can issue.
func (c *Client) AvailableRequests() []string {
	return append([]string(nil), sharedRequestRegistry().names...)
}

// CallRequest issues any obs-websocket request by name and returns its response
// as decoded JSON.
//
// This is the escape hatch that makes the tool surface complete rather than
// merely large: a request OBS gained yesterday is reachable today, without a
// release. The typed wrappers remain the ergonomic path -- they validate,
// convert and are named for what an operator wants -- and this is what covers
// everything they do not.
func (c *Client) CallRequest(requestType string, requestData map[string]interface{}) (map[string]interface{}, error) {
	if strings.TrimSpace(requestType) == "" {
		return nil, fmt.Errorf("request_type is required")
	}
	if err := checkRequestAllowed(requestType); err != nil {
		return nil, err
	}

	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	entry, err := sharedRequestRegistry().lookup(requestType)
	if err != nil {
		return nil, err
	}

	params := reflect.New(entry.params)
	if len(requestData) > 0 {
		encoded, err := json.Marshal(requestData)
		if err != nil {
			return nil, fmt.Errorf("request_data for %s: %w", requestType, err)
		}
		// DisallowUnknownFields is deliberately not used: obs-websocket ignores
		// fields it does not know, and a strict decode here would reject a
		// request newer than the goobs types describe -- the exact case this
		// tool exists to serve.
		if err := json.Unmarshal(encoded, params.Interface()); err != nil {
			return nil, fmt.Errorf(
				"request_data for %s does not fit that request: %w", requestType, err)
		}
	}

	subclient := reflect.ValueOf(client).Elem().FieldByName("Categories").FieldByName(entry.category)
	results := entry.method.Func.Call([]reflect.Value{subclient, params})
	if errVal := results[1]; !errVal.IsNil() {
		// goobs already formats the server's status code and comment.
		return nil, errVal.Interface().(error)
	}

	raw, ok := results[0].MethodByName("GetRaw").Call(nil)[0].Interface().(json.RawMessage)
	if !ok || len(raw) == 0 {
		return map[string]interface{}{}, nil
	}

	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decoding the response to %s: %w", requestType, err)
	}
	return out, nil
}
