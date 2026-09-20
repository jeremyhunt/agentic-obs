package mcp

import (
	"context"
	"fmt"
	"log"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The gateway tools. Each wraps one obs-websocket request that scripts in this
// workspace currently open their own connection to reach: writing a source's
// settings, pressing a properties button, discovering a kind's defaults, and
// enumerating a property's options. (FB-67)

// SetSourceSettingsInput is the input for writing a source's settings.
type SetSourceSettingsInput struct {
	SourceName string                 `json:"source_name"`
	Settings   map[string]interface{} `json:"settings"`
	// Overlay merges into the existing settings when true or absent, and
	// replaces them outright when false. It is a pointer so that "not supplied"
	// is distinguishable from "false": defaulting a missing value to false would
	// silently reset every key the caller did not mention.
	Overlay *bool `json:"overlay,omitempty"`
}

// PressSourcePropertiesButtonInput is the input for pressing a properties button.
type PressSourcePropertiesButtonInput struct {
	SourceName   string `json:"source_name"`
	PropertyName string `json:"property_name"`
}

// InputKindInput is the input for kind-level queries.
type InputKindInput struct {
	InputKind string `json:"input_kind"`
}

// InputPropertyInput is the input for enumerating a property's items.
type InputPropertyInput struct {
	SourceName   string `json:"source_name"`
	PropertyName string `json:"property_name"`
}

func (s *Server) handleSetSourceSettings(ctx context.Context, request *mcpsdk.CallToolRequest, input SetSourceSettingsInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	// Absent means merge. A caller changing one field should not have to know
	// every other field to avoid clearing it.
	overlay := true
	if input.Overlay != nil {
		overlay = *input.Overlay
	}

	log.Printf("Setting settings for source '%s' (overlay=%v)", input.SourceName, overlay)

	if err := s.obsClient.SetSourceSettings(input.SourceName, input.Settings, overlay); err != nil {
		s.recordAction("set_source_settings", "Set source settings", input, nil, false, time.Since(start))
		return nil, nil, fmt.Errorf("failed to set source settings: %w", err)
	}

	result := map[string]interface{}{
		"source_name": input.SourceName,
		"overlay":     overlay,
		"message":     fmt.Sprintf("Settings applied to '%s'", input.SourceName),
	}
	s.recordAction("set_source_settings", "Set source settings", input, result, true, time.Since(start))
	return nil, result, nil
}

func (s *Server) handlePressSourcePropertiesButton(ctx context.Context, request *mcpsdk.CallToolRequest, input PressSourcePropertiesButtonInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()
	log.Printf("Pressing button '%s' on source '%s'", input.PropertyName, input.SourceName)

	if err := s.obsClient.PressInputPropertiesButton(input.SourceName, input.PropertyName); err != nil {
		s.recordAction("press_source_properties_button", "Press source properties button", input, nil, false, time.Since(start))
		return nil, nil, fmt.Errorf("failed to press properties button: %w", err)
	}

	result := map[string]interface{}{
		"source_name":   input.SourceName,
		"property_name": input.PropertyName,
		"message":       fmt.Sprintf("Pressed '%s' on '%s'", input.PropertyName, input.SourceName),
	}
	s.recordAction("press_source_properties_button", "Press source properties button", input, result, true, time.Since(start))
	return nil, result, nil
}

func (s *Server) handleGetInputDefaultSettings(ctx context.Context, request *mcpsdk.CallToolRequest, input InputKindInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()
	log.Printf("Getting default settings for input kind: %s", input.InputKind)

	defaults, err := s.obsClient.GetInputDefaultSettings(input.InputKind)
	if err != nil {
		s.recordAction("get_input_default_settings", "Get input default settings", input, nil, false, time.Since(start))
		return nil, nil, fmt.Errorf("failed to get default settings: %w", err)
	}

	s.recordAction("get_input_default_settings", "Get input default settings", input, defaults, true, time.Since(start))
	return nil, defaults, nil
}

func (s *Server) handleListInputPropertyItems(ctx context.Context, request *mcpsdk.CallToolRequest, input InputPropertyInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()
	log.Printf("Listing items of property '%s' on source '%s'", input.PropertyName, input.SourceName)

	items, err := s.obsClient.GetInputPropertiesItems(input.SourceName, input.PropertyName)
	if err != nil {
		s.recordAction("list_input_property_items", "List input property items", input, nil, false, time.Since(start))
		return nil, nil, fmt.Errorf("failed to list property items: %w", err)
	}

	// Name/value pairs rather than the audio-device shape the underlying wrapper
	// is named for: the request is generic, and a window list is not a device.
	listed := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		listed = append(listed, map[string]interface{}{"name": item.Name, "value": item.Value})
	}

	result := map[string]interface{}{
		"source_name":   input.SourceName,
		"property_name": input.PropertyName,
		"items":         listed,
		"count":         len(listed),
	}
	s.recordAction("list_input_property_items", "List input property items", input, result, true, time.Since(start))
	return nil, result, nil
}
