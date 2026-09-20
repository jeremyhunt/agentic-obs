package mcp

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CallOBSRequestInput is the input for the raw obs-websocket passthrough.
type CallOBSRequestInput struct {
	RequestType string                 `json:"request_type" jsonschema:"The obs-websocket request name, exactly as the protocol spells it, e.g. GetStats or TriggerMediaInputAction. Case-sensitive. Use list_obs_requests to discover what this build supports"`
	RequestData map[string]interface{} `json:"request_data,omitempty" jsonschema:"The request's own parameters, using the protocol's camelCase field names, e.g. {\"sceneName\": \"Game\"}. Omit for requests that take none"`
}

// handleCallOBSRequest issues any obs-websocket request by name.
//
// The typed tools cover the surfaces a workflow has actually asked for, with
// validation, friendlier names and confirmation where it matters. This covers
// the rest, which is most of the protocol: 151 requests exist and the typed
// tools wrap about 65.
//
// Bitfocus Companion, the most widely deployed OBS control surface there is,
// reached the same conclusion and ships a "Custom Command" action beside its
// curated ones. The reasoning is stronger here: a human clicking buttons cannot
// read the protocol reference mid-task, and a model can. (FB-81)
func (s *Server) handleCallOBSRequest(ctx context.Context, request *mcpsdk.CallToolRequest, input CallOBSRequestInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	fail := func(err error) (*mcpsdk.CallToolResult, any, error) {
		s.recordAction("call_obs_request", "Call OBS request", input, nil, false, time.Since(start))
		return nil, nil, err
	}

	if strings.TrimSpace(input.RequestType) == "" {
		return fail(fmt.Errorf("request_type is required; call list_obs_requests to see what this OBS build supports"))
	}

	data := input.RequestData
	if data == nil {
		data = map[string]interface{}{}
	}

	log.Printf("Calling OBS request %s", input.RequestType)

	responseData, err := s.obsClient.CallRequest(input.RequestType, data)
	if err != nil {
		// The client already carries obs-websocket's status code and comment,
		// which is the part that tells an agent what to do next: a missing
		// resource, a wrong field and a refused state need different responses.
		return fail(fmt.Errorf("obs request %s failed: %w", input.RequestType, err))
	}

	if responseData == nil {
		responseData = map[string]interface{}{}
	}

	result := map[string]interface{}{
		"request_type":  input.RequestType,
		"response_data": responseData,
	}
	s.recordAction("call_obs_request", "Call OBS request", input, result, true, time.Since(start))
	return nil, result, nil
}

// ListOBSRequestsInput filters the request listing.
type ListOBSRequestsInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"Case-insensitive substring to match against request names, e.g. 'media' or 'profile'. Omit to list all of them"`
}

// handleListOBSRequests reports every request call_obs_request can issue.
//
// Without this, the passthrough is only usable by someone who already knows the
// protocol by heart, and a mistyped name is indistinguishable from an
// unsupported one. The list comes from the connected build rather than from a
// table in this repo, so it is right for the OBS actually running. (FB-81)
func (s *Server) handleListOBSRequests(ctx context.Context, request *mcpsdk.CallToolRequest, input ListOBSRequestsInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	all := s.obsClient.AvailableRequests()

	matched := all
	if f := strings.TrimSpace(input.Filter); f != "" {
		needle := strings.ToLower(f)
		matched = matched[:0:0]
		for _, name := range all {
			if strings.Contains(strings.ToLower(name), needle) {
				matched = append(matched, name)
			}
		}
	}
	sort.Strings(matched)

	result := map[string]interface{}{
		"requests": matched,
		"count":    len(matched),
		"total":    len(all),
		"note": "Pass any of these to call_obs_request as request_type. Parameter names " +
			"and response shapes are in the obs-websocket protocol reference; they use " +
			"camelCase. A few destructive requests are refused in favour of the tools " +
			"that confirm first.",
	}
	s.recordAction("list_obs_requests", "List OBS requests", input, result, true, time.Since(start))
	return nil, result, nil
}
