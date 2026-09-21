"""Syntax-check Lua files against the Lua 5.1 that OBS itself runs.

No Lua interpreter is installed, and CGO is out (ADR-001), so a Go test cannot
parse these. OBS ships lua51.dll, which is the exact parser that matters.
"""
import ctypes
import glob
import sys

DLL_CANDIDATES = [
    "C:/Program Files/obs-studio/bin/64bit/lua51.dll",
    "/usr/lib/x86_64-linux-gnu/liblua5.1.so.0",
]


def load_lua():
    for path in DLL_CANDIDATES:
        try:
            return ctypes.CDLL(path)
        except OSError:
            continue
    print("SKIP: no Lua 5.1 library found; install OBS to enable this check")
    sys.exit(0)


lua = load_lua()
lua.luaL_newstate.restype = ctypes.c_void_p
lua.luaL_loadbuffer.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_size_t, ctypes.c_char_p]
lua.luaL_loadbuffer.restype = ctypes.c_int
lua.lua_tolstring.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_void_p]
lua.lua_tolstring.restype = ctypes.c_char_p

state = lua.luaL_newstate()
failed = False

for path in sorted(glob.glob("bridge/**/*.lua", recursive=True) + glob.glob("internal/bridge/snippets/*.lua")):
    with open(path, "rb") as handle:
        source = handle.read()
    if lua.luaL_loadbuffer(state, source, len(source), path.encode()) == 0:
        print(f"ok   {path}")
    else:
        failed = True
        print(f"FAIL {path}: {lua.lua_tolstring(state, -1, None).decode(errors='replace')}")

sys.exit(1 if failed else 0)
