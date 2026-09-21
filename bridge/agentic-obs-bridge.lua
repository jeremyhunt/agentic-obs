--[[
agentic-obs bridge.

agentic-obs writes {id, lua, args} to the inbox source; OBS raises "update" on
it; this script runs the chunk and writes {id, ok, result} to the mailbox
source, which leaves as InputSettingsChanged. Both directions are push, and the
round trip measures about 30ms.

The vendor API would have been the obvious transport and is closed to Lua:
calldata_ptr hands obs-websocket's proc handler back as a bare void* that SWIG
will not retype. ADR-013 has the measurements.

EVERYTHING HERE RUNS ON THE VIDEO THREAD. obs_source_update defers on a video
source and the signal is raised from obs_source_video_tick, so a chunk that
blocks drops frames. A timer could not help with that anyway: this thread is
synchronous, so nothing runs to fire one until the chunk returns control. The
instruction budget below stands in for a timer instead, but it only bounds an
ordinary runaway loop -- see the comment on INSTRUCTION_BUDGET for what it
does not cover.
]]

obs = obslua

local INBOX = "agentic-obs-inbox"
local MAILBOX = "agentic-obs-mailbox"
local VERSION = "1"

-- A frame's worth of work, near enough. Not a security control: it stops an
-- ordinary runaway loop, the way FB-86 stops a runaway rule -- but it is not
-- a hard bound. A chunk that wraps its own loop in its own pcall (see run(),
-- below) catches the error this budget's hook raises just like any other
-- error, so the hook keeps firing and the chunk keeps running: Lua 5.1 gives
-- a hook no way to raise an error a script-level pcall cannot catch, and a
-- coroutine would not help either, since a count hook cannot yield across
-- the C boundary. When that happens OBS itself stays blocked with no upper
-- bound, and the real backstop is the Go side's 2s transport timeout
-- (internal/bridge/transport.go), which lets agentic-obs recover and report
-- failure even while OBS stays wedged. A bridge that was not itself Lua
-- could close this gap; this one cannot.
local INSTRUCTION_BUDGET = 2000000

-- Caps on what a chunk may hand back. Exceeding one is a failure, never a
-- truncated result that reads like success.
local MAX_DEPTH = 8
local MAX_BYTES = 64 * 1024

local inbox, mailbox = nil, nil
local last_id = nil

local function log(fmt, ...)
	obs.script_log(obs.LOG_INFO, "[agentic-obs] " .. string.format(fmt, ...))
end

-- The sandbox. obslua goes in whole, because reaching OBS internals is the
-- entire point. What stays out is everything that reaches the machine or the
-- interpreter itself:
--
--   os, io            -- files and os.execute
--   package, require,
--   dofile, loadfile  -- loading more code from disk
--   load, loadstring  -- nested eval, which would undo this table
--   debug             -- debug.getregistry walks straight out of any sandbox
--   setfenv, getfenv  -- swapping the environment back
--   the raw*/…metatable family -- getmetatable("") reaches the shared string
--                     metatable, so a chunk could poison every string in the
--                     process for every other script OBS has loaded
--
-- This shrinks what a mistake can reach. It is not a boundary against an
-- attacker: Lua sandbox escapes are a known class, and anyone holding the
-- obs-websocket password could load their own script anyway.
local function make_env(args)
	return {
		obslua = obs,
		obs = obs,
		args = args,
		bridge_version = VERSION,

		string = string,
		table = table,
		math = math,

		ipairs = ipairs,
		pairs = pairs,
		next = next,
		select = select,
		tonumber = tonumber,
		tostring = tostring,
		type = type,
		unpack = unpack,
		error = error,
		assert = assert,
		pcall = pcall,
		xpcall = xpcall,
	}
end

-- set_value walks a returned Lua value into an obs_data_t.
--
-- Everything is nested under one key because obs_data has no "set arbitrary
-- value" call -- the type has to be chosen per field.
local function set_value(data, key, value, depth, budget)
	local kind = type(value)

	if kind == "nil" then
		obs.obs_data_set_string(data, key, "")
		return true
	elseif kind == "boolean" then
		obs.obs_data_set_bool(data, key, value)
		return true
	elseif kind == "number" then
		obs.obs_data_set_double(data, key, value)
		return true
	elseif kind == "string" then
		budget.bytes = budget.bytes + #value
		if budget.bytes > MAX_BYTES then
			return false, "the result exceeded " .. MAX_BYTES .. " bytes"
		end
		obs.obs_data_set_string(data, key, value)
		return true
	elseif kind == "table" then
		if depth >= MAX_DEPTH then
			return false, "the result nested deeper than " .. MAX_DEPTH .. " levels"
		end
		local child = obs.obs_data_create()
		for k, v in pairs(value) do
			-- Keys are part of the encoding too: without this, a chunk
			-- returning one table with a huge key would sail past MAX_BYTES
			-- and still read as success.
			local key_str = tostring(k)
			budget.bytes = budget.bytes + #key_str
			if budget.bytes > MAX_BYTES then
				obs.obs_data_release(child)
				return false, "the result exceeded " .. MAX_BYTES .. " bytes"
			end
			local ok, err = set_value(child, key_str, v, depth + 1, budget)
			if not ok then
				obs.obs_data_release(child)
				return false, err
			end
		end
		obs.obs_data_set_obj(data, key, child)
		obs.obs_data_release(child)
		return true
	end

	-- functions, userdata, threads
	return false, "a " .. kind .. " cannot be sent back"
end

local function answer(id, ok, value, err)
	if mailbox == nil then
		return
	end

	local settings = obs.obs_data_create()
	obs.obs_data_set_string(settings, "id", id)
	obs.obs_data_set_bool(settings, "ok", ok)
	obs.obs_data_set_string(settings, "error", err or "")

	-- Cleared unconditionally, like every other field: obs_source_update
	-- merges into the mailbox's existing settings, so a later failing call
	-- would otherwise leave a previous success's result sitting under this
	-- key, and the Go side reads settings["result"] on every reply.
	obs.obs_data_set_string(settings, "result", "")

	if ok then
		local written, cap_err = set_value(settings, "result", value, 0, { bytes = 0 })
		if not written then
			obs.obs_data_set_bool(settings, "ok", false)
			obs.obs_data_set_string(settings, "error", cap_err)
		end
	end

	obs.obs_source_update(mailbox, settings)
	obs.obs_data_release(settings)
end

-- run compiles and executes one chunk. It never raises: a bridge that dies on
-- a bad payload needs a human to reload it, which is the one repair it cannot
-- perform on itself.
local function run(source, args)
	local chunk, compile_err = loadstring(source, "agentic-obs-chunk")
	if chunk == nil then
		return false, nil, tostring(compile_err)
	end

	setfenv(chunk, make_env(args))

	-- The hook is installed from out here, where debug is still reachable.
	-- The chunk itself never sees it.
	local tripped = false
	debug.sethook(function()
		tripped = true
		error("the chunk exceeded its instruction budget", 2)
	end, "", INSTRUCTION_BUDGET)

	local ok, result = pcall(chunk)
	debug.sethook()

	if not ok then
		if tripped then
			return false, nil, "the chunk exceeded its instruction budget of " .. INSTRUCTION_BUDGET
		end
		return false, nil, tostring(result)
	end
	return true, result, nil
end

-- read_args rebuilds the args table from the command.
--
-- The wire carries an ARRAY of {name, s|n|b} rather than an object, because a
-- Lua script cannot walk an obs_data object at all: obs_data_item_next takes
-- obs_data_item_t** in C and SWIG will not marshal that. Arrays are plain --
-- obs_data_array_count and obs_data_array_item are ordinary calls.
--
-- This is also where code and data stay apart. Values arrive as values; none
-- of this is ever concatenated into the chunk's source.
local function read_args(settings)
	local out = {}
	local list = obs.obs_data_get_array(settings, "args")
	if list == nil then
		return out
	end

	local count = obs.obs_data_array_count(list)
	for i = 0, count - 1 do
		local entry = obs.obs_data_array_item(list, i)
		if entry ~= nil then
			local name = obs.obs_data_get_string(entry, "name")
			if name ~= "" then
				if obs.obs_data_has_user_value(entry, "s") then
					out[name] = obs.obs_data_get_string(entry, "s")
				elseif obs.obs_data_has_user_value(entry, "n") then
					out[name] = obs.obs_data_get_double(entry, "n")
				elseif obs.obs_data_has_user_value(entry, "b") then
					out[name] = obs.obs_data_get_bool(entry, "b")
				end
			end
			obs.obs_data_release(entry)
		end
	end

	obs.obs_data_array_release(list)
	return out
end

local function on_inbox_update(cd)
	local src = obs.calldata_source(cd, "source")
	if src == nil then
		return
	end

	local settings = obs.obs_source_get_settings(src)
	local id = obs.obs_data_get_string(settings, "id")
	local source_text = obs.obs_data_get_string(settings, "lua")
	local args = read_args(settings)
	obs.obs_data_release(settings)

	-- Creating the source raises this signal too, and OBS coalesces writes, so
	-- the id is what makes a command a command.
	if id == nil or id == "" or id == last_id then
		return
	end
	last_id = id

	local ok, value, err = run(source_text, args)
	answer(id, ok, value, err)
end

local function open_sources()
	local blank = obs.obs_data_create()
	mailbox = obs.obs_source_create("color_source_v3", MAILBOX, blank, nil)
	inbox = obs.obs_source_create("color_source_v3", INBOX, blank, nil)
	obs.obs_data_release(blank)

	if inbox == nil or mailbox == nil then
		log("FAILED to create the transport sources; the bridge is not running")
		return false
	end

	obs.signal_handler_connect(obs.obs_source_get_signal_handler(inbox), "update", on_inbox_update)
	return true
end

function script_description()
	return [[<h2>agentic-obs bridge</h2>
<p>Lets agentic-obs run code inside OBS. It creates two sources,
<code>agentic-obs-inbox</code> and <code>agentic-obs-mailbox</code>, which
belong to no scene and render nowhere. <b>Deleting them stops the bridge
working.</b></p>
<p>Installed and updated with <code>agentic-obs install-bridge</code>.</p>]]
end

function script_load(settings)
	if open_sources() then
		log("bridge %s up: commands on %q, answers on %q", VERSION, INBOX, MAILBOX)
	end
end

function script_unload()
	if inbox ~= nil then
		obs.obs_source_release(inbox)
		inbox = nil
	end
	if mailbox ~= nil then
		obs.obs_source_release(mailbox)
		mailbox = nil
	end
end
