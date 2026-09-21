---
name: visual-workflow
description: Activate this skill when a visual asset has to be made or changed and then put on screen in OBS — "generate a background for my starting-soon scene", "this overlay is the wrong size", "make me a lower third", "replace the logo", "outpaint this to 16:9", "cut the background out of this render", "build the starting screen from these layers". It covers the whole loop — originate, condition, place, look, adjust, persist — across the image tools on this machine and the OBS tools in this server. Use scene-designer instead when the sources already exist and only need arranging.
---

# Visual Workflow

Making a visual asset and getting it on screen is one loop, but it crosses four
different toolchains. This skill is the map: which tool originates, which
conditions, which places, and which verifies — and, just as importantly, which
tool never to reach for.

## When to Use This Skill

Activate **visual-workflow** when the request needs a *new or changed image*, not
just a rearrangement:

- **Originating** — "generate a synthwave background", "make me a lower third",
  "design a starting-soon screen", "I need an overlay for alerts"
- **Conditioning** — "this is the wrong aspect ratio", "cut the background out",
  "make it 1920 wide", "it needs transparency", "outpaint this to fill the canvas"
- **Replacing** — "swap the logo for this one", "update the background art"
- **Building a layer stack** — "assemble these six PNGs into the scene"

Use **scene-designer** instead when nothing new is being made and existing
sources only need moving, resizing or toggling.

## The rule that decides everything else

**agentic-obs has no image processing and will never grow any.** Its part of
this loop is exactly four things: knowing the canvas, making a source exist,
placing it with intent, and **showing you the result in the same turn**.
Everything upstream belongs to the tool that already does it well.

```
originate            condition              place                  verify
─────────            ─────────              ─────                  ──────
Gemini (JPEG)     ─┐
Canva (MCP)       ─┼─► Pillow          ─►  agentic-obs:        ─►  agentic-obs:
ComfyUI (alpha)   ─┤   krita-mcp           ensure_input            take_screenshot
Krita             ─┘   ComfyUI             set_source_transform    → look → adjust
existing assets                            apply_scene_spec        → loop
```

Orchestration is your job, not the server's. Files on disk are how these tools
meet.

## Workflow Steps

### 1. Read the canvas before deciding any size

**Tools used**: `get_obs_status`

Take `video.base_width` / `video.base_height`. **Never assume 1920×1080** — the
collection this was built against is 2560×1440. Every size decision downstream
is derived from what you read here.

### 2. Originate

Pick by what the asset needs, not by habit:

| Need | Tool | The catch |
|---|---|---|
| A new image from a description | `gemini_render.py` | **JPEG only.** The Interactions API rejects `image/png`, so there is **no alpha** |
| A designed layout, brand assets | Canva MCP connector | `generate-design`, `export-design`, then place the exported file |
| An *edit* of an existing image, with real transparency | ComfyUI | Local, GPU, **normally stopped** |
| Layered work, precise manual edits | krita-mcp | Drives a live Krita, 23 tools |

**Gemini** (`TGV-SongifyWidget/scripts/gemini_render.py`, also in
`VTuber-Live2D/scripts/`): model `gemini-3-pro-image`, key read at runtime from
`Twitch-WhisperPy/.env` (`GEMINI_API_KEY`). Takes `--prompt`, `--out`,
repeatable `--ref`, `--size 1K|2K|4K`, `--aspect`.

> Because it cannot return alpha, both existing pipelines here **generate onto a
> flat background and key it out afterwards** — either with Pillow or with a
> `chroma_key_filter_v2` in OBS. Plan for that step; do not discover it after
> the render.

**ComfyUI** is the `docker/comfyui` stack (RTX 3060 passthrough, port 8188,
`comfyui.neo.geektup.net`). It is **stopped on purpose** — `update-stacks.ps1`
skips it — so bring it up with `docker compose up -d` and expect a **~10 minute
first model load** off the G: spinning disk. `G:\ComfyUI\models\` holds *edit*
models rather than plain text-to-image: Qwen-Image-Edit 2509, FireRed-Image-Edit,
Flux-2-klein, with 4-step Lightning LoRAs and a **Flux-2-klein outpaint LoRA** —
that last one is how you reframe an asset to a new aspect ratio without
stretching it.

Drive it with `VTuber-Live2D/scripts/comfy_run.py` against
`VTuber-Live2D/workflows/*.api.json`. It patches a fixed node contract: **78**
LoadImage, **111**/**110** positive/negative prompt, **3** seed, `--ref-image`.
Do not invent node numbers; use a workflow that has them.

**krita-mcp** (`VTuber-Live2D/tools/krita-mcp/`, registered for the
`G:\PROJECTS` scope) drives a live Krita 5.3.3 through an installed pykrita
plugin. The useful ones here are `transform_image` (a real resize/scale),
`get_image` (renders a PNG back **so you can see the edit**), `apply_filter`,
`export_document` and `run_python` (full libkis).

> **Do not use the `cli-anything-krita` skill for this.** Its `layer`, `filter`,
> `canvas` and `project` commands never launch Krita — they mutate a JSON state
> file — and its export synthesises *blank* layers with no way to load an
> existing image. It cannot touch a generated PNG.

### 3. Condition

**Pillow lives in `G:\PROJECTS\.venv`** (12.3.0, alongside numpy, psd-tools,
obsws-python, google-genai). Resize, crop, composite, alpha work, green-key
removal — this is the default for anything geometric.

Two traps worth stating outright:

- **There is no ImageMagick on this machine.** `convert` on PATH resolves to the
  **NTFS filesystem conversion utility**. Calling it on an image is not a failed
  command, it is a different program entirely. Never call `convert`.
- **There is no SVG rasteriser anywhere.** Krita is the nearest option.

### 4. Place

**Tools used**: `ensure_input`, `set_source_transform`, `set_source_order`, `apply_scene_spec`

Two patterns, and the first is usually better:

**(a) The layer stack — no coordinates at all.** The established convention
here (`TGV-SongifyWidget/starting-soon/layers.py`) is to emit **every layer as a
full-canvas RGBA PNG**, so every source sits at position 0,0 with scale 1.0 and
**z-order is the only thing that matters**. Conditioning absorbs the geometry,
placement becomes trivial, and nothing needs re-measuring when the canvas
changes. Prefer this whenever you control the asset.

In a scene spec, one line per placement says exactly that:

```json
{ "source": "OVERLAY_StartingSoonCRT", "layout": { "mode": "stretch" } }
```

A `layout` is resolved against the live canvas every time the spec is applied or
diffed, so the document survives a resolution change that a captured transform
cannot — a transform is a correct answer about one canvas with no way to say
so.

**(b) A positioned item** — a PiP camera, a window-capture avatar, a corner
badge. Use `set_source_transform` with a `fit` block and state the intent
(`fit`, `fill`, `stretch`, `center`) rather than computing a scale factor. OBS
implements these as bounding-box types, so the placement needs the canvas only:
it does not go stale when you replace the asset, and it cannot divide by zero
before a browser source has loaded.

Use `ensure_input` rather than a `create_*` tool whenever the source might
already exist. It reports `created`, `placed`, `updated` or `unchanged`, so a
re-run is safe *and* honest.

### 5. Verify — look at it

**Tools used**: `take_screenshot`

`take_screenshot` returns the image **in the same turn**. This is the step the
loop exists for. Actually look at it, and look for the things coordinates cannot
tell you:

- Is the alpha real, or is there a grey box where transparency should be?
- Is it behind something it should be in front of?
- Is text legible at this size, and is it inside the safe area?
- Did the browser source load at all, or is it a blank rectangle?
- Did a chroma key eat part of the subject?

Then adjust and repeat from step 4. One look beats three confident transforms.

### 6. Persist

**Tools used**: `capture_scene_spec`, `diff_scene_spec`, `apply_scene_spec`

Once it looks right, `capture_scene_spec` turns the scene into a document you
can store, diff against later, and apply to put it back. That is what makes the
next iteration cheap instead of a rebuild.

For assets that need a URL rather than a local path, `tgv-cdn-push <file>`
uploads to the TGV CDN and prints (and copies) the public URL. It only ever
adds files.

## Things that will bite you

**A browser source's URL may have a second writer.** The "Starting Soon" overlay
is the case: a setup script owns the page and its quad geometry while the
streaming dashboard owns `text` and `until` on the same URL. Rewriting the URL
drops the other writer's parameters. Name them:

```json
{ "settings": { "url": "http://localhost:8791/soon.html?quad=2" },
  "preserve_url_params": ["text", "until"] }
```

`ensure_input` takes this directly; in a scene spec it goes on the source's
entry. It means *keep it if I did not say*, so a value you set still wins.

**Reload rules for browser sources**, which differ by what changed:

**Tools used**: `set_source_settings`, `press_source_properties_button`

- **Changing the URL reloads the page by itself.** No button press. (The
  starting-soon typewriter relies on exactly this as its restart cue.)
- **Changing CSS, or editing a local file the page loads**, needs
  `press_source_properties_button` with `"refreshnocache"`.
- **Never flip `restart_when_active`.** `obs_wire.py` relies on it staying
  `false` so the widget's websocket survives a scene switch. The cache-buster
  trick some scripts use is the wrong lesson — `refreshnocache` is one call and
  mutates nothing persisted.

**Settings-key discovery beats guessing.**

**Tools used**: `get_input_default_settings`, `list_input_property_items`, `call_obs_request`

`get_input_default_settings` gives every key a kind accepts with its default.
`list_input_property_items` enumerates a dropdown's real values — the window
list for a `window_capture`, the device list for a camera. And anything with no
wrapper at all is still reachable through `call_obs_request`.

**Filters are where the image work lands inside OBS.**

**Tools used**: `create_source_filter`, `set_source_filter_settings`, `get_source_filter`

A `chroma_key_filter_v2` recovers transparency from a keyed Gemini render
without touching the file; `color_filter_v2` is what the existing scenes use for
haze and grade passes. Prefer a filter over re-rendering when the change is
tonal — it is instant, reversible, and leaves the asset alone.

## Related Skills

- **scene-designer** — arranging sources that already exist
- **preset-manager** — saving and restoring source visibility states
- **streaming-assistant** — going live, recording, health checks
