# ADR-011: Declarative Scene Specs (FB-82, FB-83, FB-84, FB-89)

**Status:** Accepted
**Date:** 2026-09-21

## Context

agentic-obs exposes OBS as verbs. Ninety-nine tools map almost one-to-one onto
obs-websocket requests, so building a scene costs an agent a dozen sequential
round trips, with no way to state what the scene should look like, no way to
check whether the result matches, and no way to put it back.

The workspace's own answer to this is two hand-rolled Python scripts
(`obs_starting_soon.py`, `obs_wire.py`) that each re-implement look-it-up,
create-if-missing, update-if-not against a hardcoded list of layers. They exist
because there was no way to say *this is the scene*.

**What makes the obvious design fail is sharing.** Reading the collection this
was written for, before designing anything:

- Eleven sources are placed in more than one scene, up to five scenes each.
- `jurmiey_avatar` is placed **twice in the same scene** (items 245 and 259).
- Eight of the containers are groups, not scenes, and `isGroup` is the only
  thing that tells them apart -- both report `OBS_SOURCE_TYPE_SCENE`.

A document that lists placements, each carrying its own source settings, cannot
round-trip any of that. Applying it would write one source's settings once per
placement, fighting itself, or silently collapse two placements into one.

## Decision

**A scene is a document with two lists: what the sources are, and where they
are placed.**

```
Spec { version, scene, sources[], items[], omitted[] }
```

### 1. Sources and placements are separate, because OBS separates them

An input is a global object; a scene item is one reference to it. So a source is
captured once however often it appears, and a placement references it by name.
A placement's identity is **source name plus occurrence index**, counted in
render order -- not the scene item id, which OBS hands out and which a created
item will not match.

### 2. A source is one of exactly three things

`input`, `scene` or `group`, and which one decides what can be captured:

- A **nested scene** is placed, transformed and filtered exactly like any other
  source, because libobs says *"a scene is a source"*. It is **not** an input:
  `GetInputSettings` answers `InvalidResourceType` (602) for one. So a spec
  references it, and that scene is captured in its own right.
- A **group** reports the same source type as a nested scene. Its contents come
  from `GetGroupSceneItemList`, which refuses a scene, while `GetSceneItemList`
  refuses a group. Neither is a superset of the other.
- **Filters hang off all three**, because a scene and a group are both
  `obs_source_t`. Capturing filters for inputs only would silently drop filter
  state from ten of this collection's eleven scenes -- a failure by omission,
  which still produces a document that looks complete.

### 3. The diff *is* the plan

`Apply` runs `Diff` and acts on the findings. There is no second code path that
decides what to write, so a dry run and a real apply cannot disagree about what
would happen, and a second apply has nothing to do because the diff is empty.

Five kinds, each mapping to one apply behaviour:

| Kind | Meaning | What an apply does |
|---|---|---|
| `drift` | a managed value moved | writes it back |
| `missing` | in the spec, not in the scene | creates it |
| `unmanaged` | in the scene, not in the spec | leaves it alone |
| `kind_mismatch` | a source's input kind changed | fails the op |
| `renamed` | same uuid, different name | reports only |

`unmanaged` is a reported non-action on purpose. A scene almost always holds
things configured by hand that the spec was never meant to own, and removing
them is the most destructive thing an apply can do.

### 4. Most of the work is in what the diff does *not* report

Each of these is a false-positive class that would make the diff cry wolf, and a
diff that cries wolf gets ignored exactly when it matters:

- **Stored defaults.** `GetInputSettings` returns only what differs from the
  kind's defaults, so a spec storing a value equal to a default reads as "spec
  says X, live says nothing". Defaults are merged under *both* sides first.
- **float32 rounding.** OBS stores transforms as float32 and sends them as JSON
  numbers, so a float64 that goes out comes back rounded. Tolerances are per
  unit: 0.01px position and crop, 1e-4 scale, 1e-3 degrees rotation.
- **Absolute index.** Order is compared by the relative rank of *managed* items.
  Anything unmanaged shifts every index below it.
- **JSON number types.** `1920` and `1920.0` are the same value.
- **A parameter another writer owns.** A source may name query parameters of its
  URL as `preserve_url_params`; they are carried onto the spec's URL before
  comparing. See the note on `overlay=false` below.

### 5. Apply order is fixed, because the steps depend on each other

sources -> placements -> settings -> filters -> transform -> blend -> lock ->
visibility -> one ordering pass -> prune.

A failure is a **per-op result, never an abort**. A partial apply against a live
OBS is normal -- a locked source, a moved file -- and a run that stopped without
saying how far it got is worse than one that finished and reported four
failures. Ops that depended on a failed op are reported `skipped` with the cause.

### 6. The pre-apply capture is the undo

An apply returns `before`: the scene as it was, captured before anything was
written. Nothing else records what was replaced, and applying that document puts
the scene back.

## Constraints that shaped this, and will otherwise be re-derived

**There is no `CreateGroup` over obs-websocket.** The Scenes category has
`CreateScene`; obs-websocket's `RequestHandler` registers only `GetGroupList`
and `GetGroupSceneItemList`. libobs itself *does* have `obs_scene_add_group` --
the gap is the protocol's, not the library's. So a spec can capture a group and
can **never** materialise a missing one; it is reported
`skipped: group_creation_unsupported`. An in-OBS script could close this later.

**`CreateInput` requires a scene.** The handler calls `AcquireScene` and rejects
a request with neither `sceneName` nor `sceneUuid`. So a missing source is
created *into the first scene that places it*, and every further placement is a
`CreateSceneItem`.

**Four transform fields are derived.** `Width`, `Height`, `SourceWidth` and
`SourceHeight` are read-only: captured so a diff can see them, stripped before
`SetSceneItemTransform`.

**`internal/storage`'s `migrate()` forbids `ALTER TABLE`.** This is the one most
likely to be tripped over. It re-executes *every* statement in the migration
slice on every boot inside one transaction, and stamps
`schema_version = len(migrations)`. It is idempotent only because every
statement is `CREATE ... IF NOT EXISTS`. An `ALTER TABLE ... ADD COLUMN` would
succeed the first time and **hard-fail every subsequent boot**. Any schema
change must therefore be a new table, and a spec is stored as a versioned JSON
document -- which is what `SpecVersion` exists for -- rather than as columns.

**Settings are written with `overlay=false`.** The live object is made to match
the document, so a key the spec dropped returns to its default instead of
lingering and a second apply has nothing to do. That is right for every key the
spec owns and is exactly what drops a key it does not -- which is why a browser
source whose URL has a second writer names those parameters in
`preserve_url_params` (FB-89). A capture never fills that field in: only the
author knows which parameters are foreign.

## Alternatives rejected

- **A flat list of placements, each carrying its source's settings.** Cannot
  round-trip a shared source, which this collection has eleven of.
- **Addressing a placement by scene item id.** Ids are assigned by OBS and are
  unique only within a scene, so a spec applied anywhere else addresses nothing.
- **Normalising the spec into SQL columns.** See the `ALTER TABLE` constraint.
- **Migrating `scene_presets` into specs.** A preset is a *masked* spec --
  the same document with `fields=[enabled]` -- so it is read through an adapter,
  never migrated.
- **Re-encoding URLs when merging query parameters.** `url.Values.Encode` sorts
  keys and escapes characters a URL may carry raw, so a round trip rewrites the
  string without changing its meaning, and a spec whose URL comes back different
  from the one it stores drifts forever.

## Deferred, with the trigger for each

- **A `scene_specs` table** and `save/list/get/delete_scene_spec` plus
  `obs://spec/{name}`: until a named baseline is wanted. Specs are inline
  documents first, because the consumer that motivated this keeps its scene
  definition in a git repo, not in a server's database.
- **A `layout` block on `ItemSpec`.** Today a placement carries an absolute
  transform, so a spec does not survive a canvas resize. `internal/layout`
  already resolves fit/fill/stretch/center to OBS bounds types and needs only
  the canvas; wiring it into a spec is the remaining step. The trigger is a spec
  that has to apply at two resolutions.
- **A `presetToSpec` adapter.** `apply_scene_preset` still routes through
  `CaptureSceneState`/`ApplyScenePreset` on the client rather than through
  `Diff`/`Apply` with `fields=[enabled]`. Folding it in removes two methods from
  `OBSClient` and gives presets the dry run and the per-op report for free.
- **`RequestBatch` (opcodes 8/9).** The server implements it fully --
  `SerialRealtime`, `SerialFrame` and `Parallel`, with `haltOnFailure` and a
  `variables` object threaded through the batch. **`goobs` v1.8.3 does not**:
  `api/opcodes/opcodes.go` has `case 8:` and `case 9:` as empty placeholders
  commented `// request batch`. This is a deliberate deferral, not a conclusion.
  `SerialFrame` executes a whole batch inside one video frame tick, which for
  applying a spec means every transform lands on the same rendered frame instead
  of sources visibly moving one at a time. It needs a goobs contribution.

## Consequences

### Positive
- A scene is something you can capture, read, diff, edit and put back, and the
  diff names exactly what changed.
- Dry run is the default on apply, and it is the same code path as the write.
- An apply is recoverable: the report carries the scene as it was.
- The two hand-rolled setup scripts become a document plus one tool call.

### Negative
- A capture of a scene with a nested scene is incomplete on its own; the nested
  scene is a separate document, and the caller has to walk.
- A missing group cannot be created, so a spec containing one is only fully
  applicable against a collection that already has it.
- Settings are all-or-nothing per source: `overlay=false` means a spec that
  omits a key resets it. `preserve_url_params` is the one exception, and it is
  narrow on purpose.

### Neutral
- Specs are documents, not rows. Nothing in `internal/storage` knows about them.
- `internal/scenespec` is pure: it reads through a narrow interface, performs no
  I/O of its own, and imports only `internal/obs`.
