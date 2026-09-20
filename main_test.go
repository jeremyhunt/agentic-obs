package main

import (
	"reflect"
	"testing"

	"github.com/ironystock/agentic-obs/config"
)

// TestToolGroupsFromConfigCopiesEveryField asserts that every bool field of
// config.ToolGroupConfig reaches the MCP server's ToolGroupConfig.
//
// This is a structural test rather than a field-by-field one: adding a group to
// config.ToolGroupConfig without adding it to toolGroupsFromConfig fails here
// instead of silently shipping a binary that never registers that group's tools.
func TestToolGroupsFromConfigCopiesEveryField(t *testing.T) {
	// Set every source field to true so a dropped field shows up as a false.
	in := config.ToolGroupConfig{}
	inV := reflect.ValueOf(&in).Elem()
	for i := 0; i < inV.NumField(); i++ {
		if inV.Field(i).Kind() == reflect.Bool {
			inV.Field(i).SetBool(true)
		}
	}

	got := reflect.ValueOf(toolGroupsFromConfig(in))
	gotT := got.Type()

	for i := 0; i < got.NumField(); i++ {
		field := gotT.Field(i)
		if got.Field(i).Kind() != reflect.Bool {
			continue
		}
		if !got.Field(i).Bool() {
			t.Errorf("toolGroupsFromConfig dropped %q: every group enabled in "+
				"config.ToolGroupConfig must be enabled in mcp.ToolGroupConfig", field.Name)
		}
	}
}

// TestToolGroupConfigsHaveMatchingFields asserts the two structs stay in step,
// so a group added to one side is noticed on the other.
func TestToolGroupConfigsHaveMatchingFields(t *testing.T) {
	names := func(v any) map[string]bool {
		t := reflect.TypeOf(v)
		out := make(map[string]bool, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			out[t.Field(i).Name] = true
		}
		return out
	}

	src := names(config.ToolGroupConfig{})
	dst := names(toolGroupsFromConfig(config.ToolGroupConfig{}))

	for name := range src {
		if !dst[name] {
			t.Errorf("config.ToolGroupConfig has %q but mcp.ToolGroupConfig does not", name)
		}
	}
	for name := range dst {
		if !src[name] {
			t.Errorf("mcp.ToolGroupConfig has %q but config.ToolGroupConfig does not", name)
		}
	}
}
