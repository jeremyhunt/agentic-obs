package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ironystock/agentic-obs/internal/automation"
	"github.com/ironystock/agentic-obs/internal/bridge"
	"github.com/ironystock/agentic-obs/internal/screenshot"
	"github.com/ironystock/agentic-obs/internal/storage"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// This file is the reservation's backstop, and it exists because a list of
// guarded call sites is not a property -- it is a thing someone has to keep
// remembering.
//
// Twice now that failed. create_audio_input was missed by a review, and
// create_source_filter, remove_source_filter, toggle_source_filter,
// set_source_filter_settings, toggle_input_mute, set_input_volume,
// press_source_properties_button and create_scene were missed by the review
// after that. Every one of them was already refused through call_obs_request
// and allowed through its own typed tool, so the passthrough and the tool
// surface disagreed about the same OBS request.
//
// internal/obs's TestEverySourceAddressingFieldMatchesTheKeyRule does this for
// the passthrough, against goobs' generated surface. This does it for the tool
// surface, against what registerToolHandlers actually serves over MCP: every
// tool through which a caller names a source or a scene either refuses a
// reserved name, or is listed below with the reason it need not. A tool that is
// neither fails this test, so adding tool number eleven forces the decision
// rather than defaulting to unguarded.
//
// It drives the handlers rather than reading their schemas, because behaviour
// is the thing being claimed. A guard that exists but sits after the write
// would pass a structural check and fail this one.

// reservedNameFields are the schema properties through which a caller names an
// OBS source or a scene.
//
// Exactly these four, determined by inspecting every served schema rather than
// guessed: filter_name, preset_name, rule_name, property_name, hotkey_name,
// transition_name, vendor_name, font_name, new_name and old_name all name
// something that is not a source. TestReservedNameFieldsAreTheOnlyOnes keeps
// that true.
var reservedNameFields = map[string]bool{
	"source_name":     true,
	"scene_name":      true,
	"input_name":      true,
	"dest_scene_name": true,
}

// nonSourceNameFields are the remaining *_name properties, listed so the test
// above can assert the split is complete rather than merely current.
var nonSourceNameFields = map[string]string{
	"filter_name":     "a filter on a source, not the source",
	"font_name":       "a font face",
	"hotkey_name":     "an OBS hotkey id",
	"name":            "an automation rule, an agentic-obs screenshot source, or an Advanced Scene Switcher macro or variable -- never an OBS source",
	"new_name":        "a preset or automation rule",
	"old_name":        "a preset",
	"preset_name":     "a scene preset in agentic-obs' own storage",
	"property_name":   "a field in a source's properties dialog",
	"rule_name":       "an automation rule",
	"transition_name": "a transition",
	"vendor_name":     "a third-party plugin's vendor id",
}

// toolsExemptFromTheReservation is every tool that may name the transport
// without refusing, and why.
//
// Three reasons appear, and they are the three the rest of this work settled
// on. A READ exposes nothing get_obs_status's bridge field does not already
// report. A PLACEMENT write goes through a scene item id and cannot reach the
// source -- the same line fieldMask.writesSources draws for apply_scene_spec,
// and the reason apply_scene_preset is not refused either. And a STORAGE write
// touches agentic-obs' own database rather than OBS.
//
// A reason is required, not decorative: writing one is the step at which
// "surely this is fine" has to become a sentence someone else can check.
var toolsExemptFromTheReservation = map[string]string{
	// Reads.
	"get_source_settings":       "read",
	"get_source_transform":      "read",
	"get_source_filter":         "read",
	"list_source_filters":       "read",
	"list_input_property_items": "read: enumerates a properties dropdown",
	"get_input_mute":            "read",
	"get_input_volume":          "read",
	"take_screenshot":           "read: renders the source to a file, writes nothing in OBS",
	"capture_scene_spec":        "read",
	"diff_scene_spec":           "read: compares, never writes",
	"list_scene_presets":        "read",

	// Placement state: a scene item id, never the source behind it. Refusing
	// these would make a scene that contains the transport unmanageable, for
	// the same nothing that refusing apply_scene_preset bought.
	"set_source_transform":     "placement: writes a scene item, not the source",
	"set_source_bounds":        "placement: writes a scene item, not the source",
	"set_source_crop":          "placement: writes a scene item, not the source",
	"set_source_locked":        "placement: writes a scene item, not the source",
	"set_source_order":         "placement: writes a scene item, not the source",
	"toggle_source_visibility": "placement: writes a scene item, not the source",

	// Scene selection. Switching to a scene does not touch a source, and an
	// operator who has a scene under one of these names still has to be able
	// to show it.
	"set_current_scene": "selects a scene; writes no source",
	"set_preview_scene": "selects a scene; writes no source",

	// agentic-obs' own storage.
	"save_scene_preset":        "writes agentic-obs' database, reading the scene",
	"create_screenshot_source": "registers a capture in agentic-obs' database; the OBS source is only read",

	// apply_scene_spec names the scene to apply TO, which is not the spec's
	// contents. The reservation it does carry is on the spec -- see
	// scenespec.Apply and TestApplySceneSpecRefusesASpecNamingTheBridgeTransport
	// -- and it is gated on dry_run and the field mask, so a probe through
	// scene_name alone is not the thing being guarded.
	"apply_scene_spec": "guarded on its spec, not on its target scene; see scenespec.Apply",
}

// bridgeRefusal is the marker every reservation error carries.
const bridgeRefusal = "Lua bridge's transport"

func allGroupsOn() ToolGroupConfig {
	cfg := DefaultToolGroupConfig()
	v := reflect.ValueOf(&cfg).Elem()
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() == reflect.Bool {
			v.Field(i).SetBool(true)
		}
	}
	return cfg
}

// toolSchema is the part of a served tool this file reads.
type toolSchema struct {
	Properties map[string]struct {
		// A pointer field is served as ["boolean","null"] rather than
		// "boolean", so the type arrives as a string or as a list of them.
		Type json.RawMessage `json:"type"`
	} `json:"properties"`
	Required []string `json:"required"`
}

// jsonType reads a schema type that may be a string or a list, and returns the
// first one that is not "null".
func jsonType(raw json.RawMessage) string {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return one
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		for _, t := range many {
			if t != "null" {
				return t
			}
		}
	}
	return ""
}

// servedTools connects an in-memory client to a fully configured server and
// returns both the schemas and a live session to call them through.
//
// Storage is a real temp database and every group is on, so no tool escapes by
// being unregistered or by panicking on a nil dependency.
func servedTools(t *testing.T) (map[string]toolSchema, *mcpsdk.ClientSession) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "surface.db")
	db, err := storage.New(context.Background(), storage.Config{Path: dbPath})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	s := newRegisteredServerWith(t, func(s *Server) {
		s.toolGroups = allGroupsOn()
		s.storage = db
		// Real dependencies, so a tool cannot dodge this test by panicking on a
		// nil one before it reaches its guard.
		s.screenshotMgr = screenshot.NewManager(s.obsClient, db, screenshot.DefaultConfig())
		s.automationEngine = automation.NewAutomationEngine(db, s.obsClient)
	})

	ctx := context.Background()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := s.mcpServer.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { serverSession.Close() })

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "surface-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { clientSession.Close() })

	schemas := map[string]toolSchema{}
	for tool, err := range clientSession.Tools(ctx, nil) {
		require.NoError(t, err)
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema toolSchema
		require.NoError(t, json.Unmarshal(raw, &schema))
		schemas[tool.Name] = schema
	}
	require.NotEmpty(t, schemas, "no tools were served, so this test would pass by testing nothing")
	return schemas, clientSession
}

// probeArguments builds a call that names the transport wherever the tool lets
// a caller name a source, and fills every other required field with something
// of the right type.
//
// The filler values are deliberately uninteresting. A tool that refuses for the
// right reason refuses before it looks at them; a tool that fails on them
// instead produces an error that is not the reservation's, which is exactly
// what the assertions distinguish.
func probeArguments(schema toolSchema, reserved string) map[string]interface{} {
	args := map[string]interface{}{}

	for name := range schema.Properties {
		if reservedNameFields[name] {
			args[name] = reserved
		}
	}

	for _, name := range schema.Required {
		if _, done := args[name]; done {
			continue
		}
		switch jsonType(schema.Properties[name].Type) {
		case "string":
			args[name] = "probe"
		case "integer", "number":
			args[name] = 1
		case "boolean":
			args[name] = false
		case "array":
			args[name] = []interface{}{}
		default:
			args[name] = map[string]interface{}{}
		}
	}
	return args
}

// callText runs one tool and flattens whatever came back into a single string,
// because a handler error arrives as an error from CallTool on some paths and
// as an IsError result carrying text on others.
func callText(t *testing.T, session *mcpsdk.ClientSession, name string, args map[string]interface{}) string {
	t.Helper()

	result, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return err.Error()
	}
	var sb strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcpsdk.TextContent); ok {
			sb.WriteString(text.Text)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func TestEveryToolThatNamesASourceRefusesTheBridgeTransport(t *testing.T) {
	schemas, session := servedTools(t)

	var refused, exempt, unclassified []string

	for _, name := range sortedToolNames(schemas) {
		schema := schemas[name]

		names := false
		for property := range schema.Properties {
			if reservedNameFields[property] {
				names = true
				break
			}
		}
		if !names {
			continue
		}

		if reason, ok := toolsExemptFromTheReservation[name]; ok {
			require.NotEmpty(t, reason, "%s is exempt with no reason given", name)

			// Exempt tools are called too, and must NOT refuse. This is what
			// stops the guard over-reaching into the reads and placement
			// writes that ADR-013 deliberately leaves open.
			got := callText(t, session, name, probeArguments(schema, bridge.InboxSource))
			if strings.Contains(got, bridgeRefusal) {
				t.Errorf("%s is exempt (%s) but refused the transport:\n\t%s", name, reason, got)
			}
			exempt = append(exempt, name)
			continue
		}

		// Not exempt: it must refuse, for both names.
		ok := true
		for _, reserved := range []string{bridge.InboxSource, bridge.MailboxSource} {
			got := callText(t, session, name, probeArguments(schema, reserved))
			if !strings.Contains(got, bridgeRefusal) {
				ok = false
				t.Errorf("%s names a source or scene, is not in toolsExemptFromTheReservation, "+
					"and did not refuse %q. Either guard it with isBridgeTransport, or add it to "+
					"that table with the reason it is safe. It said:\n\t%s",
					name, reserved, strings.TrimSpace(got))
			}
		}
		if ok {
			refused = append(refused, name)
			unclassified = unclassified[:0:0]
		}
	}

	t.Logf("%d tools refuse the transport, %d are exempt", len(refused), len(exempt))

	// Sharp lower bounds, for the reason the registry tests have them: a
	// version of this that stopped finding tools -- a schema shape change, a
	// group that failed to register -- would otherwise pass by exercising
	// nothing at all.
	if len(refused) < 19 {
		t.Errorf("only %d tools were driven to a refusal (%v); the reservation covers more than "+
			"that, so this test is not exercising what it claims to", len(refused), refused)
	}
	if len(exempt) < 15 {
		t.Errorf("only %d exempt tools were checked (%v); this test is not seeing the whole surface",
			len(exempt), exempt)
	}
}

// TestReservedNameFieldsAreTheOnlyOnes keeps the field list above honest.
//
// The refusal test only looks at tools carrying one of the four fields, so a
// tool added with a source named through some fifth property -- target_source,
// input_names -- would be skipped silently, which is the same quiet miss this
// file exists to prevent. Every *name* property that is served must therefore
// be classified as naming a source or as naming something else.
func TestReservedNameFieldsAreTheOnlyOnes(t *testing.T) {
	schemas, _ := servedTools(t)

	seen := map[string]bool{}
	for _, schema := range schemas {
		for property := range schema.Properties {
			if !strings.Contains(strings.ToLower(property), "name") {
				continue
			}
			seen[property] = true
			if reservedNameFields[property] || nonSourceNameFields[property] != "" {
				continue
			}
			t.Errorf("%q is a new name-shaped tool input and is classified nowhere. If it names an "+
				"OBS source or scene, add it to reservedNameFields so the reservation test drives "+
				"it; if it names something else, add it to nonSourceNameFields with what it names",
				property)
		}
	}

	// And the reverse: a field that stopped being served should not linger in
	// either table, pretending to cover something.
	for property := range reservedNameFields {
		if !seen[property] {
			t.Errorf("reservedNameFields lists %q, which no tool serves any more", property)
		}
	}
	for property := range nonSourceNameFields {
		if !seen[property] {
			t.Errorf("nonSourceNameFields lists %q, which no tool serves any more", property)
		}
	}
}

// TestExemptToolsAreAllStillServed stops the exempt table from silently
// covering a tool that was renamed or removed -- an entry for a name nothing
// serves is an exemption that has quietly stopped meaning anything.
func TestExemptToolsAreAllStillServed(t *testing.T) {
	schemas, _ := servedTools(t)

	for name, reason := range toolsExemptFromTheReservation {
		schema, served := schemas[name]
		if !served {
			t.Errorf("toolsExemptFromTheReservation lists %q (%s), which is not served", name, reason)
			continue
		}
		names := false
		for property := range schema.Properties {
			if reservedNameFields[property] {
				names = true
			}
		}
		if !names {
			t.Errorf("%q is exempt from the reservation but no longer names a source or scene, "+
				"so the entry is dead", name)
		}
	}
}

func sortedToolNames(schemas map[string]toolSchema) []string {
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
