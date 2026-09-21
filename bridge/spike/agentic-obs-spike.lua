--[[
agentic-obs bridge spike -- THROWAWAY. Remove it when the questions are answered.

It answers the one thing the bridge design could not settle by reading source:
whether SWIG marshals a Lua-held obs_data_t* through calldata_set_ptr's void*.
Everything else about the transport was proven from obs-websocket's own headers;
this is the part only a running OBS can say.

What it does:
  1. checks the obs-websocket API is reachable from Lua at all
  2. registers a websocket vendor named "agentic-obs-spike"
  3. every 2 seconds, emits a vendor event carrying an obs_data_t payload

Step 3 is the experiment. If the payload arrives on the websocket with its
fields intact, a Lua script can answer agentic-obs over the connection it
already holds -- no polling, no second transport. If the event arrives with
empty data, SWIG dropped the pointer and the bridge falls back to
SetInputSettings plus one short GetInputSettings poll on a correlation id.

Read the answer in the Script Log, and on the websocket side by running:

    make test-live   (internal/obs -run TestLiveSpike)

It writes nothing to your scenes, creates no sources, and runs no commands.
]]

obs = obslua

local VENDOR_NAME = "agentic-obs-spike"
local EVENT_TYPE = "probe"
local EMIT_INTERVAL_MS = 2000

local vendor = nil -- opaque obs_websocket_vendor handle, owned by obs-websocket
local seq = 0
local emitting = false

local function log(fmt, ...)
	obs.script_log(obs.LOG_INFO, "[spike] " .. string.format(fmt, ...))
end

-- missing reports which of the API functions this script needs are absent from
-- obslua. A spike that dies on a nil call tells you nothing; one that names the
-- missing function tells you exactly how far the binding goes.
local function missing()
	local needed = {
		"obs_get_proc_handler", "proc_handler_call",
		"calldata_create", "calldata_destroy",
		"calldata_set_string", "calldata_set_ptr",
		"calldata_ptr", "calldata_int", "calldata_bool",
		"obs_data_create", "obs_data_set_string", "obs_data_set_int",
		"obs_data_release",
	}
	local absent = {}
	for _, name in ipairs(needed) do
		if obs[name] == nil then
			table.insert(absent, name)
		end
	end
	return absent
end

-- call runs one obs-websocket proc and hands the calldata back for reading.
-- The caller destroys it. Returns nil when the proc is not registered, which is
-- what you get if obs-websocket is not loaded.
local function call(proc, fill)
	local ph = obs.obs_get_proc_handler()
	if ph == nil then
		return nil, "no global proc handler"
	end

	local cd = obs.calldata_create()
	if fill then
		fill(cd)
	end

	local ok = obs.proc_handler_call(ph, proc, cd)
	if not ok then
		obs.calldata_destroy(cd)
		return nil, string.format("proc %q is not registered", proc)
	end
	return cd
end

-- 1 -- is the API there at all?
local function probe_api_version()
	local cd, err = call("get_api_version")
	if cd == nil then
		log("FAIL  api version: %s (is obs-websocket loaded?)", err)
		return false
	end
	local version = obs.calldata_int(cd, "version")
	obs.calldata_destroy(cd)
	log("PASS  api version: %d -- the websocket API is reachable from Lua", version)
	return true
end

-- 2 -- can Lua register a vendor? This is the half the plan predicted works:
-- vendor_register takes a string in and hands a pointer back, and neither
-- crosses a C function pointer.
local function probe_vendor_register()
	local cd, err = call("vendor_register", function(c)
		obs.calldata_set_string(c, "name", VENDOR_NAME)
	end)
	if cd == nil then
		log("FAIL  vendor_register: %s", err)
		return false
	end

	vendor = obs.calldata_ptr(cd, "vendor")
	obs.calldata_destroy(cd)

	if vendor == nil then
		log("FAIL  vendor_register: the call succeeded but handed back no vendor")
		return false
	end
	log("PASS  vendor_register: registered %q", VENDOR_NAME)
	return true
end

-- 3 -- THE question. An obs_data_t is built in Lua and passed as the void* the
-- proc expects. Whether the payload survives that decides the bridge's reply
-- path, and it is only answerable from the receiving end.
local function emit()
	if vendor == nil then
		return
	end
	seq = seq + 1

	local data = obs.obs_data_create()
	obs.obs_data_set_int(data, "seq", seq)
	obs.obs_data_set_string(data, "marshalled", "yes")
	obs.obs_data_set_string(data, "note", "if you can read this, calldata_set_ptr carried an obs_data_t from Lua")

	local cd, err = call("vendor_event_emit", function(c)
		obs.calldata_set_ptr(c, "vendor", vendor)
		obs.calldata_set_string(c, "type", EVENT_TYPE)
		obs.calldata_set_ptr(c, "data", data)
	end)

	obs.obs_data_release(data)

	if cd == nil then
		log("FAIL  vendor_event_emit: %s", err)
		stop()
		return
	end

	local success = obs.calldata_bool(cd, "success")
	obs.calldata_destroy(cd)

	if seq == 1 then
		log("emit #1 returned success=%s -- now check the websocket side: an empty", tostring(success))
		log("       eventData means SWIG dropped the pointer, and the bridge needs the")
		log("       SetInputSettings fallback instead.")
	end
end

function stop()
	if emitting then
		obs.timer_remove(emit)
		emitting = false
		log("stopped emitting after %d events", seq)
	end
end

local function start()
	if emitting then
		return
	end
	if vendor == nil and not probe_vendor_register() then
		return
	end
	obs.timer_add(emit, EMIT_INTERVAL_MS)
	emitting = true
	log("emitting %q every %dms on vendor %q", EVENT_TYPE, EMIT_INTERVAL_MS, VENDOR_NAME)
end

-- 4 -- call_request is reachable, and is recorded here as a dead end rather
-- than a capability. It hands back a struct obs_websocket_request_response*,
-- which is obs-websocket's own type and not part of libobs, so obslua has no
-- binding to read it -- and no way to call obs_websocket_request_response_free
-- either, so every call leaks the struct and its two strings. It is probed once
-- so the note is evidence rather than an assumption.
local function probe_call_request()
	local cd, err = call("call_request", function(c)
		obs.calldata_set_string(c, "request_type", "GetVersion")
		obs.calldata_set_string(c, "request_data", "{}")
	end)
	if cd == nil then
		log("FAIL  call_request: %s", err)
		return false
	end

	local response = obs.calldata_ptr(cd, "response")
	obs.calldata_destroy(cd)

	if response == nil then
		log("NOTE  call_request: callable, but handed back no response pointer")
		return false
	end
	log("NOTE  call_request: callable, and returns an opaque pointer Lua cannot read")
	log("       or free. Usable only fire-and-forget, and it leaks. Not for the bridge.")
	return true
end

function script_description()
	return [[<h2>agentic-obs bridge spike</h2>
<p>A <b>throwaway probe</b>, not the bridge. It answers whether a Lua script can
hand an <code>obs_data_t</code> to obs-websocket's <code>vendor_event_emit</code>,
which decides how an in-OBS script answers agentic-obs.</p>
<p>It reads the obs-websocket API, registers a vendor named
<code>agentic-obs-spike</code>, and emits a small event every two seconds.
<b>It touches no scenes, creates no sources and runs no commands.</b></p>
<p>Results are in the <b>Script Log</b>. Remove this script when you are done.</p>]]
end

function script_properties()
	local props = obs.obs_properties_create()
	obs.obs_properties_add_button(props, "rerun", "Re-run the probes", function()
		run_probes()
		return false
	end)
	obs.obs_properties_add_button(props, "stop", "Stop emitting", function()
		stop()
		return false
	end)
	obs.obs_properties_add_button(props, "start", "Start emitting", function()
		start()
		return false
	end)
	return props
end

function run_probes()
	local absent = missing()
	if #absent > 0 then
		log("FAIL  obslua is missing: %s", table.concat(absent, ", "))
		log("       That is the answer by itself: the binding does not reach far enough.")
		return
	end
	log("PASS  every obslua function this needs is present")

	if not probe_api_version() then
		return
	end
	probe_call_request()
	start()
end

function script_load(settings)
	log("loaded. Probing the obs-websocket API from Lua.")
	run_probes()
end

function script_unload()
	stop()
	vendor = nil
end
