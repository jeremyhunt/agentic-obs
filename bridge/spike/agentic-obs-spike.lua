--[[
agentic-obs bridge spike -- THROWAWAY. Remove it when the questions are answered.

It answers what the bridge design could not settle by reading source: whether a
Lua script can reach obs-websocket's vendor API and hand it an obs_data_t.
Everything else about the transport was proven from obs-websocket's own headers;
this is the part only a running OBS can say.

The probes, in order, because each depends on the one before:

  1. every obslua function this needs exists
  2. the GLOBAL proc handler answers obs_websocket_api_get_ph -- the bootstrap,
     and the only obs-websocket proc registered globally
  3. that handler pointer can be passed BACK IN to proc_handler_call, which is
     already a pointer round trip through SWIG
  4. vendor_register hands back a vendor handle
  5. vendor_event_emit carries an obs_data_t built in Lua  <-- THE question
  6. call_request is reachable (recorded as a dead end, see below)

Step 5 is the experiment. If the payload arrives on the websocket with its
fields intact, a Lua script can answer agentic-obs over the connection it
already holds -- no polling, no second transport. If the event arrives with
empty data, SWIG dropped the pointer and the bridge falls back to
SetInputSettings plus one short GetInputSettings poll on a correlation id.

Read the answer in the Script Log, and on the websocket side by running:

    go test -tags obslive -run TestLiveSpike ./internal/obs/

It writes nothing to your scenes, creates no sources, and runs no commands.
]]

obs = obslua

local VENDOR_NAME = "agentic-obs-spike"
local EVENT_TYPE = "probe"
local EMIT_INTERVAL_MS = 2000

local ws_ph = nil -- obs-websocket's own proc handler, fetched via the global one
local vendor = nil -- opaque obs_websocket_vendor handle, owned by obs-websocket
local seq = 0
local emitting = false

local function log(fmt, ...)
	obs.script_log(obs.LOG_INFO, "[spike] " .. string.format(fmt, ...))
end

-- missing reports which obslua functions this script needs are absent. A spike
-- that dies on a nil call tells you nothing; one that names the missing
-- function tells you exactly how far the binding goes.
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

-- run calls one proc and hands the calldata back for reading; the caller
-- destroys it.
--
-- proc_handler_call is wrapped in pcall because a SWIG type error is a Lua
-- error, not a false return, and that error IS an answer -- it would mean the
-- binding cannot pass these pointers at all. Reporting it beats a traceback.
local function run(handler, proc, fill)
	if handler == nil then
		return nil, "no proc handler"
	end

	local cd = obs.calldata_create()
	if fill then
		local filled, ferr = pcall(fill, cd)
		if not filled then
			obs.calldata_destroy(cd)
			return nil, string.format("SWIG refused an argument to %q: %s", proc, tostring(ferr))
		end
	end

	local called, ok = pcall(obs.proc_handler_call, handler, proc, cd)
	if not called then
		obs.calldata_destroy(cd)
		return nil, string.format("SWIG refused the handler for %q: %s", proc, tostring(ok))
	end
	if not ok then
		obs.calldata_destroy(cd)
		return nil, string.format("proc %q is not registered on this handler", proc)
	end
	return cd
end

-- 2 and 3 -- the bootstrap.
--
-- obs-websocket registers exactly ONE proc on the global handler:
-- obs_websocket_api_get_ph. Everything else -- vendor_register,
-- vendor_event_emit, call_request -- lives on the handler that hands back. So
-- this is two experiments at once: can Lua read a proc_handler_t* out of a
-- calldata, and can it pass that back in where a typed pointer is expected?
local function bootstrap()
	if ws_ph ~= nil then
		return true
	end

	local global = obs.obs_get_proc_handler()
	if global == nil then
		log("FAIL  no global proc handler -- nothing else can work")
		return false
	end

	local cd, err = run(global, "obs_websocket_api_get_ph")
	if cd == nil then
		log("FAIL  bootstrap: %s", err)
		log("       obs-websocket is either not loaded or older than the API it needs.")
		return false
	end

	ws_ph = obs.calldata_ptr(cd, "ph")
	obs.calldata_destroy(cd)

	if ws_ph == nil then
		log("FAIL  bootstrap: the call succeeded but handed back no handler")
		return false
	end
	log("PASS  bootstrap: read obs-websocket's proc handler off the global one")
	return true
end

-- 2b -- prove the handler pointer survives being passed back in, before
-- anything depends on it.
local function probe_api_version()
	local cd, err = run(ws_ph, "get_api_version")
	if cd == nil then
		log("FAIL  get_api_version: %s", err)
		log("       This is the pointer round trip failing, not the vendor API.")
		return false
	end
	local version = obs.calldata_int(cd, "version")
	obs.calldata_destroy(cd)
	log("PASS  get_api_version: %d -- a handler pointer round-trips through SWIG", version)
	return true
end

-- 4 -- can Lua register a vendor? Predicted to work: strings in, pointer out,
-- no C function pointer anywhere.
local function probe_vendor_register()
	local cd, err = run(ws_ph, "vendor_register", function(c)
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

-- 5 -- THE question. An obs_data_t built in Lua, passed as the void* the proc
-- expects. Whether the payload survives decides the bridge's reply path, and
-- only the receiving end can say: the emit returns success either way.
local function emit()
	if vendor == nil then
		return
	end
	seq = seq + 1

	local data = obs.obs_data_create()
	obs.obs_data_set_int(data, "seq", seq)
	obs.obs_data_set_string(data, "marshalled", "yes")
	obs.obs_data_set_string(data, "note", "if you can read this, calldata_set_ptr carried an obs_data_t from Lua")

	local cd, err = run(ws_ph, "vendor_event_emit", function(c)
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

	-- Logged for the first three only. A run that never succeeds should say so
	-- more than once; one that works should not fill the log.
	if seq <= 3 then
		log("emit #%d: success=%s", seq, tostring(success))
		if seq == 1 then
			log("       Now check the websocket side. An event with EMPTY data means")
			log("       SWIG dropped the obs_data_t, and the bridge needs the")
			log("       SetInputSettings fallback instead.")
		end
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

-- 6 -- call_request is reachable, and is recorded here as a dead end rather
-- than a capability. It hands back a struct obs_websocket_request_response*,
-- which is obs-websocket's own type and not part of libobs, so obslua has no
-- binding to read it -- and no way to reach obs_websocket_request_response_free
-- either, so every call leaks the struct and its two strings. Probed once so
-- the note is evidence rather than an assumption.
local function probe_call_request()
	local cd, err = run(ws_ph, "call_request", function(c)
		obs.calldata_set_string(c, "request_type", "GetVersion")
		obs.calldata_set_string(c, "request_data", "{}")
	end)
	if cd == nil then
		log("NOTE  call_request: %s", err)
		return false
	end

	local response = obs.calldata_ptr(cd, "response")
	obs.calldata_destroy(cd)

	if response == nil then
		log("NOTE  call_request: callable, but handed back no response pointer")
		return false
	end
	log("NOTE  call_request: callable, and returns an opaque pointer Lua cannot read")
	log("       or free. Fire-and-forget only, and it leaks. Not for the bridge.")
	return true
end

function script_description()
	return [[<h2>agentic-obs bridge spike</h2>
<p>A <b>throwaway probe</b>, not the bridge. It answers whether a Lua script can
reach obs-websocket's vendor API and hand it an <code>obs_data_t</code>, which
decides how an in-OBS script answers agentic-obs.</p>
<p>It registers a vendor named <code>agentic-obs-spike</code> and emits a small
event every two seconds. <b>It touches no scenes, creates no sources and runs no
commands.</b></p>
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

	if not bootstrap() then
		return
	end
	if not probe_api_version() then
		return
	end
	probe_call_request()
	start()
end

function script_load(settings)
	log("loaded. Probing obs-websocket's vendor API from Lua.")
	run_probes()
end

function script_unload()
	stop()
	vendor = nil
	ws_ph = nil
end
