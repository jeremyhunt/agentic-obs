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

THE INTERPRETER IS LuaJIT, NOT VANILLA LUA 5.1. OBS ships it as lua51.dll
because LuaJIT is Lua 5.1 ABI-compatible, and that is exactly why it goes
unnoticed: the filename, the syntax and the manual all say 5.1. It matters
because LuaJIT compiles hot code, and a compiled trace never consults a debug
hook -- so run() must call jit.off on every chunk or the budget below does
nothing at all. Verified against OBS's own lua51.dll: LuaJIT 2.1.1736781742.

OBS ALSO REPLACES THE GLOBAL error AND print BEFORE THIS SCRIPT EVER LOADS.
obs-scripting-lua.c's add_hook_functions() runs before the script file is
read, and sets _G.error and _G.print to its own C functions, which log to
OBS's Script Log and return normally -- no lua_error, no unwind. Nothing in
this file is setfenv'd away from that global scope except the sandboxed
chunk, so a bare error(...) call anywhere else in here, jit.off or not,
was never actually raising anything: it was logging, under OBS's own
prefix rather than this script's, and letting execution carry on. That
defeated the budget completely even after jit.off fixed the tracing problem
above -- see the comment on INSTRUCTION_BUDGET and run() for that fix
(assert, which OBS does not touch).

The sandboxed CHUNK'S OWN error() had the identical problem for the
identical reason: make_env() used to hand chunks error = error, and by the
time this script loads that global is already OBS's shim, so a chunk
calling error("boom") to signal its own failure logged the message and
pcall(chunk) saw a normal return -- a failed call reporting success. Fixed
the same way error() is fixed for run()'s own hook: bridge_error(), defined
just above make_env below, builds a working error() out of assert instead
of handing the shimmed global straight through. See the comment on
bridge_error for what it does and does not reproduce of real error()'s
behavior.
]]

obs = obslua

local INBOX = "agentic-obs-inbox"
local MAILBOX = "agentic-obs-mailbox"
local VERSION = "1"

-- A frame's worth of work, near enough. Not a security control: it stops an
-- ordinary runaway loop, the way FB-86 stops a runaway rule -- but it is not
-- a hard bound.
--
-- IT WORKS ONLY BECAUSE run() CALLS jit.off ON THE CHUNK. A count hook is
-- checked by LuaJIT's interpreter and by nothing else; once a loop is
-- compiled into a trace, the trace is native code that never looks at the
-- hook again. LuaJIT compiles a loop after about 56 iterations, so before
-- jit.off the hook fired exactly zero times on `while true do end` and the
-- chunk pegged the video thread until the operator killed OBS. Reasoning
-- from the Lua 5.1 manual is the trap that let that ship: every word of it
-- about debug.sethook is true, and the interpreter it describes is not what
-- runs a hot loop here.
--
-- IT ALSO ONLY WORKS BECAUSE THE HOOK SIGNALS VIA assert, NOT error. Even
-- with jit.off applied, `while true do end` still did not stop: the hook
-- fired (confirmed by OBS's own log, ~500 times a second) but the loop kept
-- running until the operator force-killed OBS. Cause: obs-scripting-lua.c's
-- add_hook_functions() replaces the GLOBAL error and print with its own
-- logging shims before this script is ever loaded, and nothing in this file
-- is setfenv'd away from that global scope. So the hook's error(...) call
-- was never actually raising an error -- it was calling OBS's shim, which
-- logs (under OBS's own prefix, not this script's log()) and returns
-- normally, same as a print statement. pcall had nothing to catch, and the
-- interpreter just kept running the same loop. assert(false, msg) is
-- unaffected: OBS never touches assert, and assert raises through the C API
-- directly (lua_error / LuaJIT's lj_err_callermsg) rather than through the
-- Lua-callable global error, so it keeps working regardless of what error
-- currently is. Verified directly against OBS's own lua51.dll with error
-- and print replaced exactly as add_hook_functions() replaces them:
-- error(...) does not stop `while true do end` at all (still running after
-- 8s); assert(false, ...) stops it in under a millisecond, every time.
--
-- What it still does not cover:
--
--   * A chunk that wraps its own loop in its own pcall (see run(), below)
--     catches the error this budget's hook raises just like any other
--     error. That alone would only cost the chunk one pass through the
--     loop -- pcall does not resume what it caught, it ends the protected
--     call -- but a chunk that RE-ENTERS pcall from an unprotected outer
--     loop (`while true do pcall(risky) end`) gets a fresh protected call
--     every pass, and the hook has no way to reach past that: it can only
--     raise an error into the nearest pcall, and that pcall is the chunk's
--     own. A coroutine does not change this either -- re-verified directly
--     against the assert-based hook above, not assumed -- because wrapping
--     the whole chunk in one only moves the OUTERMOST catch; it does
--     nothing to whatever pcall the chunk installs for itself on the
--     inside, which is the one actually catching each firing.
--   * One long C call. The hook counts VM instructions, so time spent inside
--     string.rep, a pathological string.find pattern or a blocking obslua
--     call is not counted at all.
--
-- When either happens OBS itself stays blocked with no upper bound, and the
-- real backstop is the Go side's 2s transport timeout
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
--   ffi, jit, bit     -- LuaJIT's own. ffi is the serious one: ffi.cdef plus
--                     ffi.load reach any DLL on the machine, which is a
--                     bigger hole than os.execute. obs-scripting-lua.c calls
--                     luaopen_ffi(script) right after luaL_openlibs, so the
--                     ffi table is live in this process -- as
--                     package.loaded.ffi rather than a global, which is why
--                     withholding package and require is what actually
--                     closes the route, and why this table must never grow
--                     either of them back.
--
-- This shrinks what a mistake can reach. It is not a boundary against an
-- attacker: Lua sandbox escapes are a known class, and anyone holding the
-- obs-websocket password could load their own script anyway.

-- bridge_error is what the sandbox hands a chunk as error(). By the time this
-- script loads, OBS's add_hook_functions() has already replaced the real
-- global error with its own logging shim (see the header comment and
-- INSTRUCTION_BUDGET above), so simply exposing error = error, as make_env()
-- used to, handed chunks OBS's shim: error("boom") logged to OBS's Script Log
-- and returned normally, so pcall(chunk) saw a normal return and a failed
-- chunk reported ok=true.
--
-- Defined out here rather than inline in make_env's returned table, so it
-- keeps THIS scope's debug and assert -- both real, since OBS only ever
-- touches error and print -- no matter what setfenv does to the chunk that
-- calls it. A Lua 5.1 closure's environment is fixed to whatever was current
-- when the closure was created, not to its caller's, so a chunk invoking
-- this through its own sandboxed env still runs it against the real globals.
--
-- assert is the actual raise, the same way run()'s budget hook signals:
-- assert is never touched by OBS's shim and raises through the C API
-- directly (lua_error / LuaJIT's lj_err_callermsg), so it works no matter
-- what error currently is.
--
-- IT MUST BE A TAIL CALL -- `return assert(...)`, not a bare `assert(...)`
-- statement. assert(false, message) does not just raise message unchanged:
-- for a string (or number) message it ALSO adds its OWN position prefix,
-- unconditionally, blaming whichever Lua function directly called assert.
-- Called as a plain statement from in here, that caller is always this
-- function, so every message would be prefixed with THIS line, forever,
-- regardless of what the chunk actually did. Written as a tail call instead,
-- this function's own stack frame is gone by the time assert goes looking
-- for someone to blame, so assert attributes the prefix to WHATEVER CALLED
-- bridge_error instead -- which is always exactly wherever the chunk itself
-- wrote error(...). That is real error()'s DEFAULT (level 1) behavior,
-- exactly, with no extra code needed to compute it. Verified directly
-- against the real DLL: a plain `assert(false, msg)` statement inside a
-- wrapper prefixes with the wrapper's own fixed line every time; the
-- identical call written as `return assert(false, msg)` instead prefixes
-- with whatever called the wrapper, however deep the chunk's own call
-- stack is above that point.
--
-- A table (or other non-string, non-number) message is exempt from that
-- prefixing altogether and passed through completely unchanged, tail call
-- or not -- confirmed against the real DLL -- which is what lets a
-- structured error object like {code = 5} survive the trip as a table,
-- not a stringified approximation of one.
--
-- What this does NOT reproduce, stated rather than hidden: the level
-- argument itself. Real error(msg, level) lets a caller blame an arbitrary
-- stack level, or pass 0 to suppress the prefix entirely. assert -- the
-- only unshimmed raising primitive available here -- offers no such knob:
-- whatever position it attributes a string (or number) message to is fixed
-- by where assert is actually (tail-)called from, not by any argument.
-- level is still accepted, so a chunk that passes one does not hit a
-- surprising arity mismatch, but it has NO EFFECT: level 0 does not
-- suppress the prefix, and level 2+ does not move it to a different frame
-- -- every string/number message gets exactly the level-1 prefix above,
-- always. This was tried the other way first -- computing the requested
-- level's position by hand with debug.getinfo, then handing the
-- already-prefixed string to assert -- and rejected: assert's own
-- automatic prefix still applies on top of a hand-built one exactly as it
-- does on top of a plain message, producing a confusing double prefix for
-- level 1 and a flatly wrong one for level 0 or 2+ (assert's own prefix
-- always names the level-1 position, whatever the hand-built one said).
-- Covering the overwhelming common case -- a bare error("message"), which
-- means level 1 -- exactly, and being honest here about not covering the
-- rest, beat a fragile attempt at the rest that would have undermined the
-- common case too. See the LuaJIT report this shipped with for the
-- empirical trail, including the level-0 and level-2 cases as tried and
-- reverted.
local function bridge_error(message, level)
	-- level kept in the signature to match error(message, level)'s shape --
	-- see the comment above for why it is read no further than this.
	return assert(false, message)
end

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
		-- error is bridge_error (defined above), not OBS's own shimmed
		-- global -- see the comment there for why that is necessary and what
		-- it does not reproduce exactly. assert, pcall and xpcall are all
		-- untouched by OBS's shim and are handed through as-is.
		error = bridge_error,
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

	-- Keep the chunk interpreted, or the budget below is decoration. LuaJIT
	-- checks a count hook in the interpreter only, so the first hot loop gets
	-- compiled and the hook is never consulted again. The second argument
	-- recurses into the chunk's nested function prototypes, which is what
	-- covers a loop the chunk hides inside a function of its own -- and since
	-- the sandbox withholds load and loadstring, those prototypes are all the
	-- Lua code a chunk can reach. Everything else it can call is a C function,
	-- which LuaJIT cannot trace into anyway.
	--
	-- Pass literal true, never a variable that might hold false: checked
	-- directly against this DLL, jit.off(chunk, false) recurses exactly like
	-- jit.off(chunk, true) -- LuaJIT recurses whenever a second argument is
	-- present at all, regardless of what it is. Only a one-argument
	-- jit.off(chunk) is non-recursive, and that is not this call.
	--
	-- The cost is real: interpreted arithmetic measured 2-3x slower than
	-- compiled on this machine. Chunks are meant to be short, and one long
	-- enough for that to matter is already dropping frames.
	--
	-- Guarded so the script still loads under a vanilla Lua 5.1 that has no
	-- jit table.
	if jit then
		jit.off(chunk, true)
	end

	-- The hook is installed from out here, where debug is still reachable.
	-- The chunk itself never sees it.
	--
	-- It signals with assert(false, msg), not error(msg). OBS replaces the
	-- global error (and print) with its own logging shim before this script
	-- ever loads, and nothing out here is setfenv'd away from that global
	-- scope -- so error(...) does not raise in this file, it logs and
	-- returns, same as OBS treats a bare print(). See the comment on
	-- INSTRUCTION_BUDGET above for how that was found and verified. assert
	-- is untouched by OBS and raises through the C API directly, so it
	-- works regardless of what error currently is.
	local tripped = false
	debug.sethook(function()
		tripped = true
		assert(false, "the chunk exceeded its instruction budget")
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
