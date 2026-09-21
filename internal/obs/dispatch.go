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

// checkRequestAllowed decides whether one passthrough call may go out.
//
// Two rules, and they are keyed on different things. deniedRequests keys on the
// request TYPE, which is enough for a destructive act whose whole identity is
// its verb. The bridge's transport is not that: SetInputSettings is exactly the
// request set_source_settings wraps and denying it wholesale would cost the
// passthrough a legitimate use, so the second rule keys on the TARGET instead.
//
// transportUUIDs resolves the reserved names to the uuids OBS currently holds
// for them. It is a function rather than a value because it costs a round trip
// and is only ever needed for the rare payload that addresses a source by uuid;
// it is not called at all otherwise.
func checkRequestAllowed(requestType string, requestData map[string]interface{}, transportUUIDs func() (map[string]string, error)) error {
	if wrapper, denied := deniedRequests[requestType]; denied {
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
	return checkBridgeTransportTarget(requestType, requestData, transportUUIDs)
}

// checkBridgeTransportTarget refuses a request that aims at the Lua bridge's
// transport.
//
// The requirement is that no call_obs_request may write, create, rename, remove
// or place a reserved transport source. Reads stay open, and "read" is decided
// structurally rather than from a table: obs-websocket names every read Get*
// and nothing else, so a request that is not a Get* is treated as a write. The
// failure direction is deliberate -- a request this build has never seen falls
// on the refusing side.
//
// What is checked is every source-addressing value in the payload, found by the
// shape of the key rather than by a list of the three field names that matter
// today (inputName, newInputName, sourceName). Scanning every string value
// instead would refuse SetInputSettings on a text source whose text happens to
// be "agentic-obs-inbox"; keying on *Name/*Uuid refuses nothing a caller would
// plausibly send, and covers sceneName, newInputName, destinationSceneName and
// whatever the next OBS release adds without this function being edited.
//
// This closes addressing the transport through this tool. It is not a boundary
// against whoever holds the obs-websocket password, who can issue the same
// request directly.
func checkBridgeTransportTarget(requestType string, requestData map[string]interface{}, transportUUIDs func() (map[string]string, error)) error {
	if len(requestData) == 0 || strings.HasPrefix(requestType, "Get") {
		return nil
	}

	refs := sourceRefsIn(requestData)

	// Names first: they need no round trip, and they are how a caller would
	// actually reach the transport.
	for _, ref := range refs {
		if !ref.uuid && IsBridgeTransport(ref.value) {
			return errBridgeTransportRequest(requestType, ref.field, ref.value)
		}
	}

	// A uuid is the bypass a name-only guard leaves open: GetInputList is a
	// read, it stays open, and it hands back the inbox's inputUuid. So the
	// names are resolved to whatever uuids OBS holds for them right now.
	// Resolving on demand rather than caching keeps this correct across a
	// bridge reinstall, which gives the sources new uuids.
	wanted := false
	for _, ref := range refs {
		if ref.uuid {
			wanted = true
			break
		}
	}
	if !wanted {
		return nil
	}

	byUUID, err := transportUUIDs()
	if err != nil {
		// Fail closed. Not knowing the transport's uuids means not knowing that
		// this request misses them, and the request would almost certainly have
		// failed anyway -- the lookup only fails when OBS is unreachable.
		return fmt.Errorf(
			"%s addresses a source by uuid and the Lua bridge's transport uuids could not be "+
				"resolved to check it against them, so the request is refused rather than guessed: %w",
			requestType, err)
	}
	for _, ref := range refs {
		if !ref.uuid {
			continue
		}
		if name, ok := byUUID[strings.ToLower(ref.value)]; ok {
			return errBridgeTransportRequest(requestType, ref.field, name)
		}
	}
	return nil
}

func errBridgeTransportRequest(requestType, field, name string) error {
	return fmt.Errorf(
		"%s is not available through call_obs_request with %s addressing %q: that is the Lua bridge's "+
			"transport, and a request that writes it runs code inside the OBS process -- which is what "+
			"the scripting channel's build tag, AGENTIC_OBS_SCRIPTING and per-call confirmation exist to "+
			"gate -- while one that renames, removes or places it breaks the bridge with no error "+
			"anywhere. Use run_lua_in_obs to run Lua in OBS, and 'agentic-obs uninstall-bridge' to take "+
			"the bridge out. Get* requests against the transport are not refused",
		requestType, field, name)
}

// sourceRef is one value in a request payload that addresses a source.
type sourceRef struct {
	// field is the payload key it came from, carried so a refusal can say
	// which part of the request was the problem.
	field string
	value string
	// uuid is true when the key addresses by uuid rather than by name.
	uuid bool
}

// sourceRefsIn collects the payload values that address a source, by key shape.
//
// It descends into nested objects and arrays. Nothing in obs-websocket 5.x
// addresses a source from inside a nested object today -- request payloads are
// flat where addressing is concerned -- so this buys nothing against the
// protocol as it stands and costs nothing either: a nested key would have to
// both end in Name or Uuid and hold a reserved value to be caught.
//
// Results are sorted so a payload with two matching refs refuses with the same
// message every time; Go's map iteration order is otherwise random.
func sourceRefsIn(requestData map[string]interface{}) []sourceRef {
	var refs []sourceRef

	var walk func(m map[string]interface{})
	walk = func(m map[string]interface{}) {
		for key, value := range m {
			switch typed := value.(type) {
			case string:
				lower := strings.ToLower(key)
				switch {
				case strings.HasSuffix(lower, "name"):
					refs = append(refs, sourceRef{field: key, value: typed})
				case strings.HasSuffix(lower, "uuid"):
					refs = append(refs, sourceRef{field: key, value: typed, uuid: true})
				}
			case map[string]interface{}:
				walk(typed)
			case []interface{}:
				for _, element := range typed {
					if nested, ok := element.(map[string]interface{}); ok {
						walk(nested)
					}
				}
			}
		}
	}
	walk(requestData)

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].field != refs[j].field {
			return refs[i].field < refs[j].field
		}
		return refs[i].value < refs[j].value
	})
	return refs
}

// bridgeTransportUUIDs maps the uuid OBS currently holds for each reserved
// transport source back to its name. Empty when the bridge is not installed.
func (c *Client) bridgeTransportUUIDs() (map[string]string, error) {
	inputs, err := c.ListSources()
	if err != nil {
		return nil, err
	}

	byUUID := map[string]string{}
	for _, in := range inputs {
		if in == nil || in.InputUuid == "" || !IsBridgeTransport(in.InputName) {
			continue
		}
		// Lower-cased on both sides. OBS writes uuids lower-case and looks them
		// up exactly, so a mixed-case uuid reaches nothing -- but matching it
		// here errs towards refusing rather than towards missing.
		byUUID[strings.ToLower(in.InputUuid)] = in.InputName
	}
	return byUUID, nil
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
	if err := checkRequestAllowed(requestType, requestData, c.bridgeTransportUUIDs); err != nil {
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
