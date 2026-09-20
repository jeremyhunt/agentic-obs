package testutil

import (
	"fmt"
	"sort"
)

// The raw obs-websocket passthrough, as the mock sees it.
//
// The real client derives its request table by reflection over goobs' generated
// types, so it knows all 147 requests goobs can issue whether or not anything
// has wrapped them. The mock cannot reflect over a connection it does not have,
// so it offers a small honest subset plus whatever a test seeds -- enough to
// exercise handler behaviour, and deliberately not enough to be mistaken for
// coverage of the dispatch itself. That lives in internal/obs's own tests and
// in the live suite, which compares against the server's own request list.

// RawCall is one recorded call_obs_request.
type RawCall struct {
	RequestType string
	RequestData map[string]interface{}
}

// CallRequest issues an obs-websocket request by name.
func (m *MockOBSClient) CallRequest(requestType string, requestData map[string]interface{}) (map[string]interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnCallRequest != nil {
		return nil, m.ErrorOnCallRequest
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	m.rawCalls = append(m.rawCalls, RawCall{RequestType: requestType, RequestData: requestData})

	if resp, ok := m.rawResponses[requestType]; ok {
		return resp, nil
	}
	if !m.knowsRequest(requestType) {
		return nil, fmt.Errorf("unknown request type %q", requestType)
	}
	return map[string]interface{}{}, nil
}

// AvailableRequests lists the requests this mock will answer.
func (m *MockOBSClient) AvailableRequests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := map[string]bool{}
	for _, name := range defaultMockRequests {
		seen[name] = true
	}
	for name := range m.rawResponses {
		seen[name] = true
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (m *MockOBSClient) knowsRequest(requestType string) bool {
	for _, name := range defaultMockRequests {
		if name == requestType {
			return true
		}
	}
	_, ok := m.rawResponses[requestType]
	return ok
}

// defaultMockRequests is a representative slice of the protocol rather than all
// of it: a few wrapped requests, and a few that exist only through the
// passthrough.
var defaultMockRequests = []string{
	"GetVersion",
	"GetStats",
	"GetSceneList",
	"GetSceneItemList",
	"SetCurrentProgramScene",
	"TriggerMediaInputAction",
	"GetMediaInputStatus",
	"GetProfileList",
	"SetCurrentProfile",
	"GetOutputList",
	"OpenInputPropertiesDialog",
	"SplitRecordFile",
	"GetCanvasList",
	"TriggerHotkeyByKeySequence",
}

// SetRawResponse seeds what a raw request returns, and makes that request known.
func (m *MockOBSClient) SetRawResponse(requestType string, response map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.rawResponses == nil {
		m.rawResponses = map[string]map[string]interface{}{}
	}
	m.rawResponses[requestType] = response
}

// RawCalls returns the raw requests made, in order.
func (m *MockOBSClient) RawCalls() []RawCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]RawCall(nil), m.rawCalls...)
}
