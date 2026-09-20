package mcp

import (
	"context"
	"fmt"
	"log"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CallVendorRequestInput is the input for reaching a third-party plugin.
type CallVendorRequestInput struct {
	VendorName  string                 `json:"vendor_name" jsonschema:"Name the plugin registered, e.g. AdvancedSceneSwitcher or obs-browser"`
	RequestType string                 `json:"request_type" jsonschema:"The vendor's own request name; see that plugin's documentation"`
	RequestData map[string]interface{} `json:"request_data,omitempty" jsonschema:"Request payload, defined by the vendor. Defaults to an empty object"`
}

// handleCallVendorRequest calls a request registered by a third-party plugin.
//
// A vendor is a name a plugin or script registers with obs-websocket, and this
// is the only way to reach anything obs-websocket does not implement itself.
// Advanced Scene Switcher macros, obs-browser's page events, and any in-OBS
// script that registers a vendor all arrive here.
//
// The payload is passed through untouched in both directions. A vendor's
// request and response shapes are defined by the plugin that registers them, so
// anything this layer did to them would be a guess about someone else's schema.
// (FB-77)
func (s *Server) handleCallVendorRequest(ctx context.Context, request *mcpsdk.CallToolRequest, input CallVendorRequestInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	fail := func(err error) (*mcpsdk.CallToolResult, any, error) {
		s.recordAction("call_vendor_request", "Call vendor request", input, nil, false, time.Since(start))
		return nil, nil, err
	}

	if input.VendorName == "" {
		return fail(fmt.Errorf("vendor_name is required; it is the name the plugin registered with obs-websocket"))
	}
	if input.RequestType == "" {
		return fail(fmt.Errorf("request_type is required; see the vendor plugin's own documentation for its request names"))
	}

	// The protocol says requestData defaults to {}. Sending nil where a vendor
	// expects an object is a difference the vendor decides the meaning of, so
	// it is normalised here rather than passed along.
	data := input.RequestData
	if data == nil {
		data = map[string]interface{}{}
	}

	log.Printf("Calling vendor request %s/%s", input.VendorName, input.RequestType)

	responseData, err := s.obsClient.CallVendorRequest(input.VendorName, input.RequestType, data)
	if err != nil {
		// Vendors cannot be enumerated, so being refused is the only way to
		// learn a plugin is absent. Name it, or the caller cannot tell a
		// missing plugin from a mistyped request type.
		return fail(fmt.Errorf("vendor request %s/%s failed: %w. "+
			"Check the plugin is installed and the request type is one it registers",
			input.VendorName, input.RequestType, err))
	}

	if responseData == nil {
		responseData = map[string]any{}
	}

	result := map[string]interface{}{
		"vendor_name":   input.VendorName,
		"request_type":  input.RequestType,
		"response_data": responseData,
	}
	s.recordAction("call_vendor_request", "Call vendor request", input, result, true, time.Since(start))
	return nil, result, nil
}
