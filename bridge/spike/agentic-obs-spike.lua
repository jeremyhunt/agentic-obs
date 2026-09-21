--[[
agentic-obs bridge spike -- THROWAWAY. Remove it when ADR-013 is written.

It started as one question: can a Lua script reach obs-websocket's vendor API
and hand it an obs_data_t? The answer is no, and the probes below are what
settle it. What it grew into is the measured prototype of the transport that
answer forced.

The probes, in order, because each depends on the one before:

  1. every obslua function this needs exists
  2. the GLOBAL proc handler answers obs_websocket_api_get_ph -- the bootstrap,
     and the only obs-websocket proc registered globally
  3. that handler pointer can be passed BACK IN to proc_handler_call  <-- FAILS
     3a. a control proving obslua calls procs fine on a handler that arrives
         typed, and an inventory of every cast helper it offers, so the failure
         is read as the narrow fact it is
  4. vendor_register, 5. vendor_event_emit -- unreachable, blocked at 3
  6. call_request is reachable and is a dead end (see below)
  7. the reply path: a Lua settings write leaves as InputSettingsChanged
  8. the command path: a settings write from outside raises "update" here, and
     what arrives is run with loadstring

3 fails because calldata_ptr hands back a bare void* and SWIG will not retype
it. obslua ships exactly two casting helpers, calldata_source and
calldata_sceneitem, and neither is for proc_handler_t. So the vendor API is
closed to Lua -- not because the binding is weak, which probe 3a disproves, but
because that one pointer cannot be named.

7 and 8 are the design that replaces it, and both are measured rather than
assumed. Two sources, one per direction: sharing one would echo every write
back to whoever made it, which is the identity problem ADR-010 already solved
once for automation rules.

Read the answers in the Script Log, and on the websocket side by running:

    go test -tags obslive -run TestLiveSpike ./internal/obs/

It touches no scenes and runs no OBS commands. It DOES create two sources,
"agentic-obs-spike-inbox" and "agentic-obs-spike-mailbox", which belong to no
scene, render nowhere, and are released when the script unloads. Probe 8 runs
Lua handed to it over the inbox, which is the whole point and also the reason
this is not something to leave loaded.
]]
obs = obslua

local VENDOR_NAME = "agentic-obs-spike"
local EVENT_TYPE = "probe"
local EMIT_INTERVAL_MS = 2000
local MAILBOX_SOURCE = "agentic-obs-spike-mailbox"
local INBOX_SOURCE = "agentic-obs-spike-inbox"

local ws_ph = nil -- obs-websocket's own proc handler, fetched via the global one
local vendor = nil -- opaque obs_websocket_vendor handle, owned by obs-websocket
local mailbox = nil -- the reply path's source, owned by this script
local inbox = nil -- the command path's source, owned by this script
local last_id = nil -- the last command id run, so a repeated signal is not a repeated command
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
		-- the chosen design's transport: two sources, a signal in, a settings
		-- write out, and loadstring to run what arrives.
		"obs_source_create", "obs_source_update", "obs_source_release",
		"obs_source_get_settings", "obs_source_get_signal_handler",
		"signal_handler_connect", "calldata_source", "obs_data_get_string",
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

-- 2c -- the inventory, run only when the round trip fails, because then the
-- question stops being "does the payload survive" and becomes "can Lua reach
-- this API at all".
--
-- The scripting reference names exactly two casting helpers, calldata_source
-- and calldata_sceneitem, written by hand because SWIG will not convert a void*
-- to a typed pointer on its own. If obslua carried a third for proc_handler_t,
-- the vendor API would be reachable after all and the failure above would be a
-- missing line in this script rather than a wall. So ask the module instead of
-- the documentation: the module is what runs.
local function probe_cast_inventory()
	log("---- inventory: every proc/calldata symbol obslua exposes ----")
	log("  the pointer we hold prints as: %s", tostring(ws_ph))

	local names = {}
	for name, value in pairs(obs) do
		if type(name) == "string" and (name:find("proc", 1, true) or name:find("calldata", 1, true)) then
			table.insert(names, string.format("%s (%s)", name, type(value)))
		end
	end
	table.sort(names)
	for _, entry in ipairs(names) do
		log("  %s", entry)
	end
	log("---- %d symbols. A caster for proc_handler_t would be among them ----", #names)

	-- Two facts read off the OBS source, confirmed here rather than trusted.
	--
	-- obslua.i includes the libobs headers and obs-frontend-api.h but NOT
	-- obs-scripting.h, so obs_script_reload should be absent: a script cannot
	-- reload a script. And obs-scripting-lua.c calls luaL_openlibs with no
	-- sandbox, so loadstring should be present: a script CAN run code handed to
	-- it at runtime, which is the same capability by another route.
	local script_fns = {}
	for name in pairs(obs) do
		if type(name) == "string" and name:find("script", 1, true) then
			table.insert(script_fns, name)
		end
	end
	table.sort(script_fns)
	log("  obslua script symbols: %s", #script_fns > 0 and table.concat(script_fns, ", ") or "(none)")
	log("  obs_script_reload bound: %s", tostring(obs.obs_script_reload ~= nil))
	log("  loadstring available:   %s", tostring(loadstring ~= nil))
end

-- 2d -- the control. Does proc_handler_call work from Lua when the handler
-- arrives correctly typed?
--
-- Without this, "SWIG refused the handler" reads as "obslua cannot call procs",
-- which would be the wrong lesson entirely. obs_source_get_proc_handler is
-- declared to return a proc_handler_t*, so SWIG types it properly, and any
-- source will do.
--
-- The proc asked for does not exist on purpose. SWIG checks argument types
-- before the handler ever looks the name up, so the two outcomes cannot be
-- confused: "not registered" means the handler was ACCEPTED and obslua calls
-- procs fine, while a SWIG refusal would mean the binding is the problem.
local function probe_typed_handler()
	local sources = obs.obs_enum_sources()
	if sources == nil or #sources == 0 then
		log("NOTE  control skipped: this collection has no sources to borrow a handler from")
		return
	end

	local handler = obs.obs_source_get_proc_handler(sources[1])
	local name = obs.obs_source_get_name(sources[1])
	local cd, err = run(handler, "agentic_obs_no_such_proc")
	obs.source_list_release(sources)

	if cd ~= nil then
		obs.calldata_destroy(cd)
		log("NOTE  control: a proc that should not exist answered. Ignore this probe.")
		return
	end

	if err:find("SWIG refused", 1, true) then
		log("FAIL  control: obslua cannot pass a proc_handler_t at all -- %s", err)
		return
	end
	log("PASS  control: proc_handler_call accepts a TYPED handler from %q (%s).", name, err)
	log("       So the binding is fine. What it cannot do is type the pointer")
	log("       obs-websocket hands back, because that arrives as a bare void*.")
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

-- 7 -- the fallback, measured instead of assumed.
--
-- With the vendor API closed, the reply path is a source whose settings carry
-- the payload. The plan assumed that costs a GetInputSettings poll on a
-- correlation id. It may not: obs-websocket has an InputSettingsChanged event
-- carrying the whole new settings object, and its own documentation notes the
-- event fires when the properties dialog changes a value -- a change made
-- inside OBS, not one obs-websocket made itself. If a Lua write raises it too,
-- the fallback is push, one hop, and costs nothing the vendor event would not
-- have.
--
-- Whether obs_source_update raises the signal obs-websocket listens on is the
-- part worth measuring: settings updates on an active source are deferred, and
-- a deferred update is exactly where a signal goes missing. So the source is
-- created unparented -- in no scene, rendering nowhere, released on unload.
local function mailbox_open()
	if mailbox ~= nil then
		return true
	end

	local settings = obs.obs_data_create()
	mailbox = obs.obs_source_create("color_source_v3", MAILBOX_SOURCE, settings, nil)
	obs.obs_data_release(settings)

	if mailbox == nil then
		log("FAIL  mailbox: could not create %q", MAILBOX_SOURCE)
		return false
	end
	log("PASS  mailbox: created %q. It is in no scene and renders nowhere.", MAILBOX_SOURCE)
	return true
end

local function mailbox_write()
	if mailbox == nil then
		return
	end
	seq = seq + 1

	local settings = obs.obs_data_create()
	obs.obs_data_set_int(settings, "seq", seq)
	obs.obs_data_set_string(settings, "marshalled", "yes")
	obs.obs_source_update(mailbox, settings)
	obs.obs_data_release(settings)

	if seq <= 3 then
		log("mailbox write #%d", seq)
		if seq == 1 then
			log("       Now check the websocket side. An InputSettingsChanged carrying")
			log("       seq means the reply path is push and needs no poll.")
		end
	end
end

-- 8 -- the inbound half, and with it the whole chosen design end to end:
-- agentic-obs ships Lua source, this script runs it, the answer leaves by the
-- mailbox. Proving it here is the point -- the reply path was measured above,
-- and a transport with one measured half is the thing this spike exists to
-- avoid.
--
-- Inbound gets its OWN source, deliberately. One source carrying both
-- directions echoes every write back to whoever made it: agentic-obs would
-- receive its own commands as InputSettingsChanged, and this script would
-- re-run its own replies as commands. That is the identity problem ADR-010
-- already solved once for automation rules, and two sources mean nobody has to
-- solve it twice.
--
-- Latency is about one frame, not zero: obs_source_update on a video source
-- only bumps defer_update_count, and the "update" signal is raised later by
-- obs_source_deferred_update on the video tick (libobs/obs-source.c).
local function reply(id, ok, result)
	if mailbox == nil then
		return
	end
	seq = seq + 1

	local settings = obs.obs_data_create()
	obs.obs_data_set_int(settings, "seq", seq)
	obs.obs_data_set_string(settings, "id", id)
	obs.obs_data_set_bool(settings, "ok", ok)
	obs.obs_data_set_string(settings, "result", tostring(result))
	obs.obs_source_update(mailbox, settings)
	obs.obs_data_release(settings)
end

-- Every failure mode here is a value to send back, never a raised error. A
-- bridge that dies on a bad payload has to be reloaded by hand, which is the
-- problem this design exists to remove.
local function on_inbox_update(cd)
	local src = obs.calldata_source(cd, "source")
	if src == nil then
		return
	end

	local settings = obs.obs_source_get_settings(src)
	local id = obs.obs_data_get_string(settings, "id")
	local code = obs.obs_data_get_string(settings, "lua")
	obs.obs_data_release(settings)

	-- Creating the source raises this signal too, and OBS may coalesce
	-- writes, so the id is what makes a command a command.
	if id == nil or id == "" or id == last_id then
		return
	end
	last_id = id

	local chunk, compile_err = loadstring(code)
	if chunk == nil then
		log("inbox %s: did not compile -- %s", id, tostring(compile_err))
		reply(id, false, compile_err)
		return
	end

	local ok, result = pcall(chunk)
	log("inbox %s: ok=%s result=%s", id, tostring(ok), tostring(result))
	reply(id, ok, result)
end

local function inbox_open()
	if inbox ~= nil then
		return true
	end

	local settings = obs.obs_data_create()
	inbox = obs.obs_source_create("color_source_v3", INBOX_SOURCE, settings, nil)
	obs.obs_data_release(settings)

	if inbox == nil then
		log("FAIL  inbox: could not create %q", INBOX_SOURCE)
		return false
	end

	obs.signal_handler_connect(obs.obs_source_get_signal_handler(inbox), "update", on_inbox_update)
	log("PASS  inbox: %q created and listening on its \"update\" signal", INBOX_SOURCE)
	log("       Send it {id, lua} with SetInputSettings and the answer comes")
	log("       back on %q.", MAILBOX_SOURCE)
	return true
end

function stop()
	if emitting then
		obs.timer_remove(emit)
		obs.timer_remove(mailbox_write)
		emitting = false
		log("stopped after %d writes", seq)
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

-- The same shape as start(), for the path taken when the vendor API is closed:
-- write on a timer so a listener attaching at any moment catches one.
-- Not a fallback any more: with the vendor path closed this IS the transport,
-- so it brings up both directions. The heartbeat stays on so the reply-path
-- probe remains reproducible after the fact.
function start_transport()
	if emitting then
		return
	end
	if not mailbox_open() then
		return
	end
	if not inbox_open() then
		return
	end
	obs.timer_add(mailbox_write, EMIT_INTERVAL_MS)
	emitting = true
	log("transport up: commands in on %q, answers out on %q", INBOX_SOURCE, MAILBOX_SOURCE)
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
		probe_typed_handler()
		probe_cast_inventory()
		start_transport()
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
	if mailbox ~= nil then
		obs.obs_source_release(mailbox)
		mailbox = nil
	end
	if inbox ~= nil then
		obs.obs_source_release(inbox)
		inbox = nil
	end
	vendor = nil
	ws_ph = nil
end
