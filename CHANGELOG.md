# Changelog

All notable changes to agentic-obs are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed
- **The two test doubles are now one world (FB-88)** — `MockOBSClient`'s OBS
  state moved onto `obstest.Fake`, the double the behavioural contract and a live
  OBS already agree on. This had been flagged as urgent for several increments;
  what settled it was measuring it. Running the contract against `MockOBSClient`
  for the first time: **all thirty rows failed.** Not a handful of edge cases —
  every behaviour the contract checks, on the double that ~40 mcp tests rely on.

  The cost had been paid repeatedly and each time looked like a separate bug.
  `SceneSource.Visible` (FB-60) was populated by *both* doubles while the real
  client never set it, so every test agreed with a client that was wrong. FB-64
  found three ways this mock contradicted itself. FB-71 found `CreateInput`
  making an input `ListSources` could not see. And the divergence was still
  growing: groups, source uuids, input kinds and blend modes were all added to
  both doubles separately in the last few increments.

  `MockOBSClient` keeps what is genuinely its own — connection state, the
  `ErrorOnX` injection points, recording and streaming flags, hotkeys,
  transitions, audio devices. Everything the contract covers delegates.
  `mock_obs.go` went from 2,777 lines to 1,670, and **all 30 contract rows now
  pass against it**, the same rows the fake and OBS 32.2.2 pass.

  Folding them immediately found a split fact: `SetSceneItemLocked` wrote to the
  world while `GetSceneItemLocked` still read a map of the mock's own, so the two
  halves of one piece of state lived in two places. That is the exact bug class
  one world makes impossible, and the repo's own `mock_consistency_test.go`
  caught it within minutes.

### Fixed
- **The fake described scene items OBS never produces (FB-88)** — a new item had
  scale 0 and alignment 0, meaning scaled to nothing and anchored at its centre.
  libobs gives a new scene item scale 1 and `OBS_ALIGN_TOP|OBS_ALIGN_LEFT`.
  Alignment matters especially: it is the field FB-54 was about, where every
  transform write silently sent 0 and re-anchored items. The fake also reported
  no supported image formats, which made `take_screenshot`'s format validation
  untestable against it.
- **The fake knew two input kinds' defaults; the mock knew five (FB-88)** — the
  richer table won. A settings diff merges defaults into both sides before
  comparing, so a kind whose defaults are unknown makes every stored default read
  as drift.
- **One test depended on a state OBS cannot be in (FB-88)** — it created a text
  source named `Text` in a non-existent scene and expected "scene not found". It
  passed only because the old mock held a scene item whose source was absent from
  its own input list. With one world, `Text` is a real input, so the name
  collision is reported first. The test now uses a name nothing else uses.

### Added
- **Per-rule debounce (FB-87)** — `debounce_ms` in a rule's `trigger_config`
  makes it wait for the events to stop and then run once, on the last one.
  Showing nine layers in a scene fires a `source_visibility_changed` rule nine
  times in a few milliseconds; the rule wants to run once, at the end.

  **It is the opposite of `cooldown_ms`, which is why both exist.** Cooldown runs
  on the *first* event and ignores the rest for its window, so it acts on the
  state before the burst; debounce acts on the state the operator ended up in.
  They compose: the cooldown is checked when the debounced run actually fires, so
  a burst cannot consume a rule's cooldown without the rule ever executing. A
  debounced rule does not respond immediately by design, so it is wrong for
  anything that must act now — a scene switch on `recording_started`, say — and
  the help says so.

  It lives in `trigger_config` rather than in a column because
  `internal/storage`'s `migrate()` re-runs every statement on every boot and so
  forbids `ALTER TABLE`. That config is already JSON, so a new key costs no schema
  change.

  This is what grew the clock seam. Debounce needs `AfterFunc`, and a debounce
  test that waited out real durations would be as slow and as flaky as the
  cooldown test the seam was built to fix (FB-37). The fake clock now schedules
  timers, fires them in deadline order when advanced, and fires them **after**
  releasing its own lock — a debounce callback asks the clock what time it is, so
  firing under the lock would deadlock the normal path rather than an edge case.
  Pending timers are cancelled when the engine stops, so one cannot fire into an
  engine that has released its OBS client.

### Fixed
- **A runaway rule is now disabled rather than left running (FB-86)** — the
  engine counts each rule's executions over a sliding second and switches off one
  that crosses twenty, recording `error=oscillation` against it and saying so in
  the log. Suppression (FB-85) removes the loops it can identify, and it
  identifies most; it cannot identify all. A rule whose action is
  `trigger_hotkey` is the clean example — the engine hands OBS a name and cannot
  know what it does, so whatever comes back has no entry waiting for it. The
  breaker does not need to know why a rule is running away, only that it is.

  The threshold sits three orders of magnitude above the measured loop, so
  "broken" and "busy" are not close: a rule firing nineteen times a second
  indefinitely is left alone, because disabling a working setup is worse than the
  loop — the operator did nothing wrong. Counts are per rule, so one runaway does
  not take out the others, and a rule's history is dropped when it is disabled so
  that re-enabling it starts clean instead of tripping immediately.

  **The first version did not work, and the reason is worth keeping.** It wrote
  the disable and the oscillation row to SQLite *before* flipping the rule off in
  memory — which reads as the tidier order, persist then reflect. But a runaway
  rule has already saturated that same file with its own execution rows, so every
  recovery write came back `SQLITE_BUSY` and the rule stayed enabled. **The
  breaker was trying to record that it had tripped using the resource the rule it
  was stopping had saturated.** It now stops the loop in memory first, which
  needs nothing that can be contended, and writes afterwards against a quiet
  database.

- **The database had no `busy_timeout`, and was silently losing writes
  (FB-86)** — WAL lets readers run alongside a writer, but writers still
  serialise, and without a timeout the second one fails immediately instead of
  waiting its turn. With ten pooled connections and an engine that runs several
  rules at once, **execution records were being dropped under any burst** — which
  is exactly when the record matters. Found while chasing the breaker failure
  above, but not specific to it: any concurrent rule activity hit this. Now set
  to five seconds.

- **A rule that reacts to what it does no longer feeds itself (FB-85)** — the
  engine reacts to OBS events and writes to OBS, and OBS announces every write as
  an event, so a rule triggering on `source_visibility_changed` with a
  `set_visibility` action looped. That is the obvious way to express "this must
  stay visible", and it has always looped. Measured before the fix: **one
  external event produced 2,300 writes in 300 ms.**

  `cooldown_ms` was never the answer. It defaults to 0, so the loop was the
  out-of-the-box behaviour rather than something a user opted into, and when set
  it throttles genuine events exactly as hard as echoes — it counts rather than
  identifies. Cooldown stays what it is, a rate limit, and is now documented as
  such.

  The engine records each state-setting write before making it and drops the
  first matching event. Four details carry the design, each the difference
  between working and looking like it works:

  - **The value is part of the key.** Without it, hiding something the engine had
    just shown would read as its own echo — so it would stop answering the
    operator precisely when they were correcting it.
  - **Entries are counted, not flagged.** Two writes produce two events, and
    collapsing them lets the second re-trigger.
  - **Entries are consumed on match**, or the suppressor becomes a filter that
    swallows every event of that shape for its lifetime.
  - **Entries expire after 500 ms.** A write can fail and OBS can coalesce two
    changes into one announcement, so an expected echo may never arrive; one that
    waited forever would eat a real event minutes later, silently.

  The check sits ahead of the rule loop, because the event is not ours "for this
  rule" — it is ours, and a second rule watching the same event would otherwise
  act on what the first one caused. The suppressor shares the engine's clock
  seam, so a fake clock moves both; two notions of `now` in one engine is exactly
  what that seam exists to prevent.

  Now: one external event, one write — and two separate operator changes still
  produce two writes. `toggle_visibility` is only partly covered, deliberately:
  the value a toggle lands on is not known until it has landed, and converting it
  into a read-then-set would change what the action means. **ADR-010** records
  the decision, including the rejected alternative of detaching the event
  callback during a write, which would blind the thumbnail cache and the resource
  notifications that share the same fan-out.

### Added
- **Scene specs, apply (FB-84)** — `apply_scene_spec` reconciles a scene to a
  captured spec: creates what is missing, writes back what drifted, and leaves
  alone what the spec does not describe. **The tool defaults to a dry run**,
  where the library function does not — a tool call is a decision made by a model
  reading a description, and the scene may be on air. The report carries the
  scene as it was, so applying that document undoes the apply; nothing else
  records what was replaced.

  The order is fixed because the steps depend on each other: sources →
  placements → settings → filters → transform → visibility → lock → one ordering
  pass → prune. **What to write is decided by a diff rather than written
  unconditionally**, which is what makes a second apply report nothing left to
  do. That matters beyond tidiness: every write is an event, and an automation
  rule watching the scene fires on each one, so an apply that writes regardless
  is indistinguishable from one that converges until something is listening.

  **A failure is a per-op result, never an abort.** A partial apply against a
  live OBS is the normal case — a locked source, a moved file — and a run that
  stopped without saying how far it got is worse than one that finished and
  reported four failures. Ops that depend on a failed one are skipped with the
  cause rather than failing again for a reason that reads as unrelated.

  **What it refuses to do**, each because the alternative is destructive:
  a missing **group** is skipped, since obs-websocket has `CreateScene` and no
  `CreateGroup`; a missing **nested scene** is skipped, because its contents are
  its own spec and an empty scene is not what the document describes; a changed
  **input kind** is reported failed, because reconciling it means removing and
  recreating the source, which destroys every placement of it in every scene; and
  `on_unmanaged: remove` takes out scene **items** only — OBS refcounts sources,
  so an input goes away by itself once nothing references it. `keep` is the
  default because removing is unrecoverable.

  Ordering permutes managed items among the positions they already occupy.
  Using the spec's own index would be wrong twice: an index past the end of a
  scene holding unmanaged items is out of range, and an unmanaged item between
  two managed ones would be shoved aside by a stack assuming it owned every slot.

  Verified against a real OBS in a scratch scene: a dry run changed nothing, the
  apply reported 1 created / 5 updated / 7 unchanged, **the post-apply diff found
  zero differences**, and a second apply had nothing to do.

### Fixed
- **A source's existence is global, and the apply was asking the wrong question
  (FB-84)** — it decided whether to create a source by looking at the scene being
  applied, so a source placed in five other scenes but not this one looked
  missing and `CreateInput` failed with `ResourceAlreadyExists`. Existence is now
  read from `GetInputList`, `GetSceneList` and `GetGroupList`, which is where OBS
  actually keeps it. This was the layered source/placement model being violated
  by the package built on it.
- **The fake gave new scene items an empty `BoundsType` (FB-84)** — obs-websocket
  never reports one; it reports `OBS_BOUNDS_NONE` for an item with no bounding
  box. A capture recorded `""`, the apply wrote it, `NormaliseBounds` turned it
  into `OBS_BOUNDS_NONE`, and the scene then disagreed with the spec it had just
  been reconciled to. Found by the round-trip test.
- **The live suites were racing each other (FB-84)** — `go test ./internal/...`
  builds packages in parallel and runs them concurrently, and every live package
  talks to the same OBS. `internal/obs`'s contract suite creates and removes a
  scratch scene per row while `internal/scenespec` is enumerating the collection,
  so a scene that vanished between the listing and the capture read as a failure.
  The collection-wide tests now ignore `agentic-obs-*` scenes, which belong to a
  test run rather than to the operator, and `make test-live` passes `-p 1` and
  covers both packages rather than only `internal/obs`.

- **Scene specs, diff (FB-83)** — `diff_scene_spec` compares a captured spec
  against the live scene and classifies each difference as `drift`, `missing`,
  `unmanaged`, `kind_mismatch` or `renamed`. It is read-only, and the findings
  are exactly what an apply would act on, so a diff is also the dry run. The
  taxonomy is the substance: each kind needs a different answer, and collapsing
  them into "different" loses that. `unmanaged` is left alone by default because
  a scene almost always holds things the spec was never meant to own;
  `kind_mismatch` is never treated as drift because no write reconciles it —
  recreating the source is the only fix and it destroys every placement.

  **Most of the work is in not reporting things.** Against a real OBS, a naive
  comparison is wrong in four separate ways, and a diff that cries wolf gets
  ignored exactly when it matters:

  - `GetInputSettings` returns only what *differs* from the kind's defaults, so a
    spec storing a value equal to a default reads as "spec says X, live says
    nothing". Defaults are merged into both sides first. This is the most common
    false positive a settings diff can have — it fires on every source whose
    document mentions a default.
  - OBS stores transforms as **float32**, so a value sent as float64 comes back
    rounded. Tolerances are per unit, because the units differ: 0.01 for position
    and crop, 1e-4 for scale, 1e-3° for rotation. A hundredth of a pixel is
    invisible; a hundredth of a scale factor is a 1% size change.
  - Order is compared by the **relative rank of managed items**, never absolute
    index. Anything unmanaged shifts every index below it, so an index comparison
    reports the whole scene as drifted the moment someone adds an overlay.
  - `1920` and `1920.0` are the same value. Every spec arriving over MCP has been
    through JSON, which makes all its numbers float64.

  Bounds dimensions are skipped under `OBS_BOUNDS_NONE`, where they are inert and
  OBS reports zero for an item that never had a bounding box. A rename is matched
  on uuid: without it a rename reads as one source missing and another unmanaged,
  and acting on that would recreate the source and orphan the original.

  **Verified against the live collection: all 11 scenes captured and immediately
  diffed report zero findings**, while a 37px change is still reported. A diff is
  refused for a spec captured from another scene, or captured with settings or
  filters omitted — it would report everything it skipped as matching.

### Changed
- **`SceneSource` carries the uuid, input kind and blend mode that were already
  on the wire (FB-83)** — `GetSceneItemList` returns `sourceUuid`, `inputKind`
  and `sceneItemBlendMode` on every item, and all three were being discarded.
  Without them a rename was undetectable, blend mode was invisible, and the
  capture had to call `ListSources` separately just to learn an input's kind —
  a round trip for something already in hand. That call is gone, and
  `scenespec.Reader` is one method smaller.

  The reflective guard added in FB-80 could not catch this: it checks that every
  field `SceneSource` *has* was carried, not that `SceneSource` has every field
  the item offers. That is a real limit of it, and a named test covers the gap.

- **Scene specs, read side (FB-82)** — `internal/scenespec` captures a scene as
  a document and `capture_scene_spec` returns it inline. The spec separates what
  a source *is* from where it is *placed*, because OBS does: an input is a shared
  object and a scene item is one reference to it. This collection places
  `jurmiey_avatar` twice in a single scene and shares eleven sources across up to
  five scenes, so a flat list of placements each carrying its own settings cannot
  round-trip — an apply would write one source's settings several times, fighting
  itself, or drop a placement. Sources are captured once each, and the expensive
  calls are per source, so `Game`'s twelve placements cost nine settings reads.

  The three source types carry the weight. An **input** has a kind and settings.
  A **nested scene** has neither, and is referenced rather than expanded — that
  scene is captured in its own right, since expanding here would duplicate it
  into every spec referencing it and recurse forever on a cycle. A **group** has
  neither either, its children belong to the group rather than to any placement
  of it, and its contents open only through `GetGroupSceneItemList`. Dispatch is
  on `isGroup`, never on `sourceType`: both containers report
  `OBS_SOURCE_TYPE_SCENE` and each list call refuses the other's argument with
  `602`. Filters are captured for all three, because a scene and a group are both
  `obs_source_t`, and reading them only for inputs would silently drop filter
  state from most containers here.

  `include_settings` and `include_filters` make a lighter document for reading a
  layout, and **the spec records what they omitted** — a partial document says so
  rather than being indistinguishable from a scene that has no filters.

  Verified against the live collection: 11 scenes, 84 source entries, 85
  placements, exercising groups, nested scenes and repeated placements.

### Fixed
- **The group requests were on the client but on no role (FB-82)** — FB-80 added
  `GetGroupList` and `GetGroupSceneItemList` to `*obs.Client` without putting
  them on a role, so `mcp.OBSClient` could not see them at all.
  `TestOBSClientIsExactlyTheUnionOfRoles` only catches the reverse direction — a
  method on the interface belonging to no role. `obs.GroupReader` is that role,
  kept separate from `SceneReader` because offering both list calls
  interchangeably would suggest a caller can pick either.

- **Every obs-websocket request is reachable (FB-81)** — `call_obs_request`
  issues any request by name, and `list_obs_requests` reports what the connected
  build supports. Measured against the live server: obs-websocket 5.7.4 offers
  **151 requests and 147 are now reachable**, where the typed tools wrap about 65.

  The registry is derived, not declared. goobs generates, per request, a params
  type carrying `GetRequestName()` and a response type embedding
  `api.ResponseCommon` whose exported `GetRaw()` holds the server's raw
  `responseData`. Both shapes are exported, so reflection over the category
  subclients reconstructs the whole request table using nothing private — no
  fork, no `unsafe`, no second connection, and no eighty near-identical wrapper
  files to go stale the next time OBS adds a request. Matching only goobs'
  variadic method shape finds 78 of 147 and looks like it works, so both shapes
  are recognised explicitly.

  This opens surfaces nothing could reach before: media playback
  (`TriggerMediaInputAction`, `SetMediaInputCursor`), profiles and scene
  collections, projectors and the properties/filters/interact dialogs,
  `SplitRecordFile` and `CreateRecordChapter`, `GetStats`, generic outputs,
  `TriggerHotkeyByKeySequence` and `GetCanvasList`.

  Six requests are refused here on purpose. `RemoveScene`, `RemoveInput`,
  `RemoveSceneItem` and `RemoveSourceFilter` have tools that confirm first, and
  routing around those while looking like a feature is the one failure mode this
  tool could introduce; `SetCurrentSceneCollection` and `RemoveProfile` rebuild
  or discard state that cannot be recovered from here. Each refusal names what to
  use instead.

  **The four that remain out of reach are named, not forgotten**:
  `Get`/`SetSourcePrivateSettings` and `Get`/`SetSceneItemPrivateSettings`. OBS
  offers them; goobs does not generate them. A live test compares the registry
  against `GetVersion.availableRequests` on every run, so a request a future OBS
  adds shows up as uncovered the first time the suite runs rather than whenever
  someone next reads a design document. That test reads the server's list
  *through* `call_obs_request`, so a broken passthrough cannot report a clean
  bill of health.

- **Groups are a distinct kind of container, and are now visible as one
  (FB-80)** — `GetGroupList` and `GetGroupSceneItemList` on the client,
  `SceneSource.IsGroup` populated, `is_group` published on `obs://scene/{name}`,
  and three contract rows. The live collection has **eight groups in ten
  placements**, so this is not a hypothetical: a capture that mishandled a group
  would mishandle half the containers on the main canvas.

  The trap is that a group and a nested scene are identical in the field most
  code would branch on. Both report `sourceType: OBS_SOURCE_TYPE_SCENE`, because
  "groups in OBS are actually scenes, but renamed and modified" — `GetGroupList`'s
  own documentation — and only `isGroup` separates them. Getting it wrong is not
  cosmetic, because **the two list calls refuse each other's arguments** rather
  than degrading: `GetSceneItemList` on a group answers `InvalidResourceType`
  (602) *"The specified source is not a scene. (Is group)"*, and
  `GetGroupSceneItemList` on a scene answers *"The specified source is not a
  group. (Is scene)"*. Neither is a superset of the other, so a walker has to
  dispatch on `isGroup` instead of trying one and falling back. A group is
  likewise not an input, and is absent from both `GetSceneList` and
  `GetInputList` — so before this, a group was invisible to anything enumerating
  a collection while still being placed in scenes like any other source.

  The FB-64 bounds trap reaches groups too, confirmed against a real one: reading
  `MERCH`'s transform and writing it straight back is refused with
  `RequestFieldOutOfRange` (402) on `boundsWidth`. `NormaliseBounds` already
  covers it, and a row now says so.

  The live suite cannot build this fixture — obs-websocket has `CreateScene` and
  no `CreateGroup` — so it **borrows** a group by duplicating an existing
  placement into its scratch scene. A duplicate references the group rather than
  copying it, so the rows mutate only the scratch placement and the operator's
  scenes are untouched. Rows skip, visibly, when a collection has no group.

### Fixed
- **`SceneSource.Type` meant different things in the fake and the client
  (FB-80)** — the fake put the *input kind* there while the client puts OBS's
  source-type vocabulary there, which is what `obs://scene/{name}` publishes. The
  fake now reports `OBS_SOURCE_TYPE_INPUT` / `OBS_SOURCE_TYPE_SCENE` as OBS does.
- **The guard against dropped conversion fields did not guard (FB-80)** —
  `TestSceneSourceFromItemCopiesEveryField` describes itself as "the guard
  against the next dropped field", but it compared a hand-written list, so it
  only ever covered fields someone remembered to add to it. `IsGroup` was added
  to `SceneSource`, left out of `sceneSourceFromItem`, and the test passed. It
  now walks the struct by reflection and fails on any field left zero.

- **Nested scenes are modelled, not assumed (FB-79)** — four contract rows for a
  scene placed inside another scene, which is the shape the live collection is
  actually built from: all ten of its containers are nested scenes, not groups,
  following obs-websocket's own advice that "using groups is discouraged; nested
  scenes are recommended". The rows fix which halves of the input contract a
  scene keeps, because `capture_scene_spec` has to branch on exactly that.

  Each row started as a documentation lookup rather than a guess. libobs settles
  it: "a scene is a source which contains and renders other sources using
  specific transforms and/or filtering" (`reference-scenes.rst`). So a nested
  scene is placed and transformed like any other source, and it carries filters
  like any other source — which is why obs-websocket's filter requests take the
  generic `sourceName` rather than `inputName`. It is *not* an input: the
  protocol's error table types the word outright, `InvalidInputKind` (605) being
  "the specified input (`obs_source_t-OBS_SOURCE_TYPE_INPUT`) had the wrong
  kind", and a scene is `OBS_SOURCE_TYPE_SCENE`. A spec that captured scenes and
  inputs alike would try to re-create every container as an input. The fourth
  row pins that a scene cannot contain itself, since
  `obs_source_add_active_child` returns "false if it causes recursion" and a
  spec is a document that can ask for one.

  `obstest.Fake` could not represent any of this. `CreateSceneItem` required
  `world.inputs[sourceName]`, so a nested scene could not be placed at all, and
  filters resolved against inputs only — the fake could not model the collection
  it stands in for. Scenes are now filter hosts and placeable sources, while
  staying out of `ListSources` and `GetSourceSettings`, which is the distinction
  that matters. All four rows pass against OBS 32.2.2.

### Fixed
- **The live contract suite no longer panics at random (FB-79)** — it opened a
  connection per row and disconnected at the end of each, and goobs v1.8.3
  panics on disconnect if the server pushes a message at the wrong moment:
  `client.go:351-368` checks whether `Disconnected` is closed and then sends on
  `Opcodes`, but `markDisconnected` closes both, so a message arriving between
  the check and the send is a send on a closed channel. It is a panic in goobs'
  own goroutine, so it cannot be recovered from here — it fails the whole run.
  Every row creates and removes scenes, inputs and filters, so OBS is nearly
  always emitting an event as a row tears down, which is exactly the window the
  race needs. Row isolation is a property of the fixture — a fresh scratch scene
  per row — and never needed a socket of its own, so the suite now connects once
  instead of twenty-seven times. Six consecutive clean runs, against one panic
  in four before.

- **Contract test suite for the OBS client (FB-58)** — `internal/obs/obstest`
  holds one behavioural contract run against two implementations: an in-memory
  fake in CI, and a real obs-websocket connection under `go test -tags obslive`
  (`make test-live`). `internal/obs/commands.go` is 1,642 lines and 66 request
  methods with effectively no direct coverage; everything was tested through a
  mock that *replaces* that layer, so defects living in the goobs boundary were
  structurally invisible — which is exactly where FB-54 lived. Rows assert
  observable behaviour rather than field lists, and compare transforms by
  reflection, so a field added later is covered without anyone remembering to
  extend the test.

  Running it against a real OBS immediately found two ways the fake had been more
  permissive than obs-websocket, both of which affect any code that builds a
  transform from scratch rather than reading one first: `boundsType` must not be
  empty (`RequestFieldEmpty`, 403), and `boundsWidth`/`boundsHeight` must be at
  least 1 even for `OBS_BOUNDS_NONE` (`RequestFieldOutOfRange`, 402). goobs sends
  every field with no `omitempty`, so unset values are transmitted as zero rather
  than omitted. Both are now contract rows and both are enforced by the fake.
  `apply_scene_spec` will construct transforms from scratch and would have hit
  this.
- **Contract coverage for inputs, placements and filters (FB-60)** — nine rows
  on top of FB-58's four, covering input creation and name collisions, scene-item
  removal, and the filter lifecycle including the merge/replace distinction
  between `overlay: true` and `overlay: false`. All nine pass against OBS 32.2.2
  as well as the fake. The placement row is the one worth knowing about: OBS
  refcounts sources, so removing an input's *only* scene item frees the input —
  what holds is that removing one placement leaves an input another placement
  still references, which is what `apply_scene_spec`'s `on_unmanaged: remove`
  depends on. `GetSceneItemEnabled` was added as the reader `SetSceneItemEnabled`
  never had, and the contract's client interface split into `SceneItemClient`,
  `InputClient` and `FilterClient` so each row declares only the role it uses.
- **`toggle_source_visibility` accepts an explicit state (FB-55)** — pass
  `visible: true`/`false` to set the state directly, or omit it to keep the
  previous toggle behaviour. A bare toggle is not safe to retry: if a call times
  out and the caller repeats it, the source ends up back where it started. This
  mirrors `toggle_source_filter`, which already worked this way.
- **`list_audio_devices` and `create_audio_input`** — enumerate Windows WASAPI
  playback and recording devices and add a capture source to a scene.
  `device_kind` selects the direction: `output` for playback devices such as a
  Voicemeeter virtual output carrying TTS, `input` for microphones. Windows only.
- **Environment variable aliases (FB-52)** — `OBS_WEBSOCKET_*` and `OBS_API_*` are
  now accepted as aliases for `OBS_HOST`/`OBS_PORT`/`OBS_PASSWORD` when the
  canonical name is unset, with the chosen source logged. Documented in the README.
- **Help entries for all nine automation tools and `help` itself** — these were
  never written, which went unnoticed because the Automation group was disabled in
  the shipped binary (see Fixed).
- Documentation restructuring with `design/` directory
- Architecture Decision Records (ADRs)
- docs-maintainer agent for documentation consistency
- **`automation-setup` prompt (FB-20 follow-up)** — 14th MCP workflow prompt; guides users through creating, testing, and monitoring automation rules. Accepts optional `rule_type` ('event'|'schedule') and `trigger_event` arguments for targeted guidance.

### Changed
- **`OBSClient` is composed from role interfaces (FB-61)** — the flat 76-method
  interface is replaced by roles declared in `internal/obs/roles.go` next to the
  client that implements them (`SceneReader`, `SceneItemWriter`,
  `FilterManager`, `RecordingController`, …). `internal/automation` had
  maintained its own near-copy to avoid depending on all 76, and the copy
  drifted — its comment asserted obs-websocket had no visibility setter long
  after one existed, which is how FB-55 happened. Both consumers now compose
  what they use, so there is nothing to keep in step by hand.
  `TestOBSClientIsExactlyTheUnionOfRoles` enforces it, and it catches what the
  compiler cannot: a method declared inline that both the client and the mock
  already implement builds perfectly. `CallVendorRequest` turned out to be on
  the client already with nothing exposing it, so it gets a `VendorCaller` role
  pending increment 2a.

### Fixed
- **A transform read from OBS could not be written back (FB-64)** —
  `set_source_transform`, `set_source_crop` and `set_source_bounds` all read the
  current transform, change a field, and write the whole thing back. That failed
  on any scene item that had never been given a bounding box, which is most of
  them: OBS reports such an item with `boundsWidth`/`boundsHeight` of zero,
  obs-websocket rejects a write below 1 even under `OBS_BOUNDS_NONE` where the
  dimensions are inert, and goobs marshals without `omitempty` so the zeroes go
  on the wire. OBS refused to accept its own output, and the caller got
  `RequestFieldOutOfRange` naming a field they never touched. `NormaliseBounds`
  fills in only unused bounds, so a genuine bounding box of zero width is still
  an error. Found by adding one contract row asking whether a transform read
  back can be written back — the invariant every transform tool depends on and
  none of them stated. Verified against OBS 32.2.2.
- **Filter and vendor events were never subscribed (FB-62)** — the connection
  asked obs-websocket for six event categories and omitted `Filters` and
  `Vendors`. An unsubscribed category raises no error and logs nothing; the
  events simply never arrive, so a handler written for one looks correct
  forever. `VendorEvent` is the only inbound channel from a plugin — Advanced
  Scene Switcher, obs-browser, and the proposed Lua bridge all reply through
  it — so that channel was one-way. The mask now lives on `ConnectionConfig`,
  resolved once at construction so a reconnect cannot return with a different
  subscription set, and the tests pin both the categories that must be present
  and the high-volume ones that must stay opt-in (`InputVolumeMeters`,
  `SceneItemTransformChanged`).
- **`obs://scene/{name}` reported every source as hidden (FB-60)** —
  `GetSceneByName` built each `SceneSource` without ever setting `Visible`, so
  the resource published `"visible": false` for every source in every scene,
  directly beneath an `"enabled"` field that told the truth. obs-websocket has
  one notion of an item showing (`sceneItemEnabled`), so both fields now carry
  it. The conversion moved out into `sceneSourceFromItem` so it can be tested
  without a websocket connection — it previously had no CI coverage at all,
  because both test doubles populated `Visible` and so agreed with a client
  that never did.
- **Subscriptions were accepted for resources that never emit (FB-59)** — the
  FB-57 guard's comment said it rejected "URIs we will never notify about", but it
  only checked the `obs://` prefix, so `obs://screenshot/...` and
  `obs://preset/...` were accepted and the client waited forever. Only
  `obs://scene/{name}` emits. Found by writing the guard's first test.
- **Resource update notifications reached no client (FB-57)** — no
  `SubscribeHandler` was set, so the SDK advertised `resources.subscribe=false`
  and `ResourceUpdated`, which delivers only to sessions in the server's
  subscription set, had an empty set to deliver to. The fan-out logged "Sent
  resource updated notification" while sending nothing. Both handlers are now
  wired, and the tests assert on what an in-memory client actually receives rather
  than on what the server believed it sent. ADR-003's notification promise now
  holds.
- **Source visibility changes did not update the scene resource (FB-57)** —
  `ShouldTriggerResourceUpdated` mapped only `scene_changed`, so a client
  subscribed to `obs://scene/{name}` was never told when the contents of that
  scene changed, even though the resource includes per-item visibility.
- **Intermittent `TestEngineCooldown` failure (FB-56, closes FB-37)** — the test
  slept for exactly as long as the cooldown it was waiting out, so it raced the
  boundary and failed at `-count>=3`. The engine's cooldown decisions now read the
  time through an injectable `clock`, and the test advances a fake clock past the
  deadline instead of sleeping through it. Several sleep-then-assert blocks in the
  same file were converted to wait-for-the-observable, which is what they were
  approximating. Ten consecutive package runs, previously flaking roughly one in
  four, now pass.
- **`set_visibility` automation action toggled instead of setting (FB-55)** — the
  executor delegated to `toggleVisibility` under a comment claiming obs-websocket
  had no setter. It has one, and `internal/obs` was already calling it privately;
  it simply was not on the client interface. A rule asking for `visible=true`
  flipped the item instead, so firing it twice hid what it was meant to show, and
  any invariant built on the action would oscillate by construction.
  `SetSceneItemEnabled` is now exposed on both client interfaces and the action
  sets the state it was given.
- **Scene item transforms silently re-anchored items (FB-54)** — `obs.SceneItemTransform`
  carried no `Alignment`, `BoundsAlignment` or `CropToBounds` field. goobs marshals
  the wire struct with no `omitempty` and obs-websocket applies any transform key
  present in a request, so those absent fields were not left alone on a write: they
  were sent as zero and applied. Every `set_source_transform`, `set_source_crop` and
  `set_source_bounds` call therefore sent `alignment=0` (`OBS_ALIGN_CENTER`),
  re-anchoring any item still on the libobs default of `OBS_ALIGN_TOP|OBS_ALIGN_LEFT`
  and shifting it on screen by half its rendered size. The three fields now round-trip,
  the conversion is extracted as `toGoobsTransform`, and the read-only derived fields
  (`Width`, `Height`, `SourceWidth`, `SourceHeight`) are explicitly not sent.
- **Automation tool group was disabled in every released binary (FB-52)** —
  `main.go` copied eight of the nine `config.ToolGroups` fields into the MCP
  server's config, silently omitting `Automation`. Because the zero value of a
  bool is `false`, the automation engine was never constructed and its nine tools
  were never registered outside of tests, which build `ServerConfig` directly. The
  mapping is now a tested function; `TestToolGroupsFromConfigCopiesEveryField`
  fails if a group is ever dropped again.
- **Automation engine graceful shutdown** — `AutomationEngine.Stop()` now waits for in-flight event dispatch and rule execution goroutines via a `sync.WaitGroup`, preventing execution records from being stranded in the `running` status on restart.
- **`delete_automation_rule` elicitation safety** — when the elicitation RPC itself errors, the handler now returns that error instead of silently falling through and deleting without user confirmation.

### Changed
- **Tool counts derive from the registration metadata (FB-52)** — a group's size
  is now `len(ToolNames)` and nothing else. The stored `ToolCount` field, the nine
  per-group `Help*ToolCount` constants, and the hardcoded expectation tables in
  `tool_config_test.go` are gone. `HelpToolCount` remains the one hand-written
  total, because `verify-docs.sh` reads it as the documented figure, and two new
  tests keep it honest.
- **Tool registration is verified against the served tool list (FB-52)** —
  `TestRegisteredToolsMatchMetadata` connects an in-memory MCP client, calls
  `ListTools`, and compares the result to `toolGroupMetadata` plus
  `MetaToolNames`. `TestToolGroupGatingIsReal` disables each group in turn and
  asserts exactly that group's tools disappear, which is the first end-to-end
  check of the gating ADR-004 describes. Previously the only tests compared one
  hand-typed number to another, which is why 81/83/85 could all coexist.
- **`verify-docs.sh` reads its expected values from the Go constants (FB-52)** —
  it previously declared its own `EXPECTED_TOOLS=81` and then "verified"
  `HelpToolCount` against that literal, so the two had to be updated in lockstep
  and the check could only ever confirm they matched each other. It now reads
  `HelpToolCount`, `HelpResourceCount` and `HelpPromptCount` from
  `internal/mcp/help_content.go` and checks the documentation against them; the
  three self-comparing constant checks are gone.
- **Documentation consistency now runs in Go CI (FB-52)** — tool counts and help
  entries live in Go source, so a Go-only change could break them without
  triggering the markdown-only `docs-check` workflow. `go.yml` now runs
  `verify-docs.sh` and `verify-skills.sh`, and `docs-check.yml` also triggers on
  `internal/mcp/**`.
- **ADR list is discovered from disk (FB-52)** — `verify-docs.sh` no longer carries
  a hardcoded ADR array that silently went stale (it stopped at 007 while 008
  existed); it now enumerates `design/decisions/[0-9]*.md` and checks each is
  indexed in the decisions README.
- **`TestHelpContentCompleteness` derives its tool list (FB-52)** — it previously
  checked a hand-written list of 56 names that labelled Core as "13 tools" when it
  has 25, so it only covered tools someone remembered to add. It now iterates
  `toolGroupMetadata` plus `MetaToolNames`.
- **FB-15: mcpui-go extraction** - Extracted `pkg/mcpui/` to standalone module
  - New repository: [github.com/ironystock/mcpui-go](https://github.com/ironystock/mcpui-go)
  - Go SDK for MCP-UI protocol with 77.7% test coverage
  - 6 documentation files, 5 runnable examples
  - agentic-obs now depends on external module

---

## [0.13.0] - 2025-12-23

### Phase 13: Automation Rules

**Summary:** FB-20 - Event-triggered actions and scheduled automation for OBS.

### Added
- **Automation Rules** (FB-20, 9 tools):
  - `list_automation_rules` - List all automation rules with status
  - `get_automation_rule` - Get detailed rule configuration
  - `create_automation_rule` - Create event-triggered or scheduled rules
  - `update_automation_rule` - Modify existing rules
  - `delete_automation_rule` - Remove rules (with confirmation)
  - `enable_automation_rule` - Activate a rule
  - `disable_automation_rule` - Deactivate a rule
  - `trigger_automation_rule` - Manually trigger for testing
  - `list_rule_executions` - View execution history
- **AutomationEngine** - Background service for rule execution:
  - Event-triggered rules (scene change, recording start, streaming, etc.)
  - Cron-based scheduled rules (using `robfig/cron/v3`)
  - Action execution with cooldown enforcement
  - In-memory rule caching with hot-reload
- **17 trigger event types**:
  - Scene: `scene_changed`, `scene_created`, `scene_removed`
  - Recording: `recording_started`, `recording_stopped`, `recording_paused`, `recording_resumed`
  - Streaming: `streaming_started`, `streaming_stopped`
  - Virtual Cam: `virtual_cam_started`, `virtual_cam_stopped`
  - Replay Buffer: `replay_buffer_saved`
  - Audio: `input_mute_changed`
  - Sources: `source_visibility_changed`
  - Transitions: `transition_started`
  - UI: `studio_mode_changed`
- **14 action types**:
  - Scenes: `set_scene`
  - Audio: `toggle_mute`, `set_mute`, `set_volume`
  - Sources: `toggle_visibility`
  - Recording: `start_recording`, `stop_recording`
  - Streaming: `start_streaming`, `stop_streaming`
  - Output: `toggle_virtual_cam`, `save_replay`, `trigger_hotkey`, `trigger_transition`
  - Control: `delay` (ms)
- **Storage migrations** (14-18) for automation rules and execution history
- **New tool group**: "Automation" (9th tool group)

### Changed
- Expanded OBS event subscriptions to include Outputs, Inputs, SceneItems, Transitions, and UI
- EventCallback interface expanded with 13 new callback methods
- Server lifecycle now manages AutomationEngine start/stop
- config.go ToolGroupConfig includes Automation option

### Dependencies
- Added `github.com/robfig/cron/v3` for cron expression parsing

### Metrics
- **Tools:** 81 (+9)
- **Resources:** 4 (unchanged)
- **Prompts:** 13 (unchanged)
- **Skills:** 4 (unchanged)
- **Tool Groups:** 9 (+1 Automation)

---

## [0.12.0] - 2025-12-21

### Phase 12: Dynamic Tool Configuration

**Summary:** FB-27 (Dynamic Tool Config) and FB-28 (Skills Update).

### Added
- **Dynamic Tool Configuration** (FB-27, 3 tools):
  - `get_tool_config` - Query tool group configuration (enabled/disabled state, tool counts)
  - `set_tool_config` - Enable/disable tool groups at runtime (session-only or persistent)
  - `list_tool_groups` - List all tool groups with descriptions and status
- **Meta-tools category** - 4 always-enabled tools that cannot be disabled:
  - `help`, `get_tool_config`, `set_tool_config`, `list_tool_groups`
- Thread-safe tool configuration with `sync.RWMutex`
- Tool group metadata with tool counts and tool name lists
- Persistence option for tool configuration via SQLite

### Changed
- "Help" category renamed to "Meta" tools for clarity
- Help content updated to describe meta-tools functionality
- **streaming-assistant skill** (FB-28): Added FB-25/26 tools and workflows:
  - Virtual camera management (start/stop for video calls)
  - Replay buffer highlight capture workflows
  - Studio mode preview/program transitions
  - Hotkey automation guidance
  - Updated cleanup recommendations

### Tests
- 32 test cases across 9 test functions for tool config handlers
- Tests for getGroupEnabled, setGroupEnabled, convertToStorageConfig helpers
- Tool group metadata validation tests

### Metrics
- **Tools:** 72 (+3)
- **Resources:** 4 (unchanged)
- **Prompts:** 13 (unchanged)
- **Skills:** 4 (unchanged)

---

## [0.11.0] - 2025-12-21

### Phase 11: Virtual Camera, Replay Buffer, Studio Mode & Hotkeys

**Summary:** FB-25 and FB-26 - Advanced OBS output control and preview features.

### Added
- **Virtual Camera & Replay Buffer** (FB-25, 6 tools):
  - `get_virtual_cam_status`, `toggle_virtual_cam` - Virtual camera control
  - `get_replay_buffer_status`, `toggle_replay_buffer` - Replay buffer state
  - `save_replay_buffer`, `get_last_replay` - Capture highlights
- **Studio Mode & Hotkeys** (FB-26, 6 tools):
  - `get_studio_mode_enabled`, `toggle_studio_mode` - Studio mode control
  - `get_preview_scene`, `set_preview_scene` - Preview/program workflow
  - `list_hotkeys`, `trigger_hotkey_by_name` - Hotkey automation

### Changed
- `stream-teardown` prompt: Scene switch now happens BEFORE stopping stream (proper ordering)
- `health-check` prompt: Added virtual cam, replay buffer, studio mode, and hotkey checks
- `recording-workflow` prompt: Integrated replay buffer for highlight capture

### Tests
- 15 new test functions with 36 test cases for FB-25/FB-26 handlers
- 3 integration workflow tests (virtual cam, studio mode, hotkeys)

### Metrics
- **Tools:** 69 (+12)
- **Resources:** 4 (unchanged)
- **Prompts:** 13 (unchanged)

---

## [0.10.0] - 2025-12-20

### Phase 10: Filters & Transitions

**Summary:** FB-23 and FB-24 - Source filter management and scene transition control.

### Added
- **Filters Tool Group** (FB-23, 7 tools):
  - `list_source_filters`, `get_source_filter` - Query filters
  - `create_source_filter`, `remove_source_filter` - Manage filters
  - `toggle_source_filter`, `set_source_filter_settings` - Configure filters
  - `list_filter_kinds` - Discover available filter types
- **Transitions Tool Group** (FB-24, 5 tools):
  - `list_transitions`, `get_current_transition` - Query transitions
  - `set_current_transition`, `set_transition_duration` - Configure transitions
  - `trigger_transition` - Trigger studio mode transition

### Metrics
- **Tools:** 57 (+12)
- **Resources:** 4 (unchanged)
- **Prompts:** 13 (unchanged)

---

## [0.7.0] - 2025-12-18

### Phase 7: MCP Completions, Help Tool & Claude Skills

**Summary:** Autocomplete support, comprehensive help system, and shareable skill packages.

### Added
- **MCP Completions**: Autocomplete for prompt arguments and resource URIs
- **Help Tool**: Topic-based guidance for tools, resources, prompts, workflows, troubleshooting
- **Claude Skills**: 4 shareable skill packages
  - `streaming-assistant` - Stream management workflows
  - `scene-designer` - Visual layout creation
  - `audio-engineer` - Audio control and monitoring
  - `preset-manager` - Scene preset management
- **New Prompts**: `scene-designer`, `source-management`, `visual-setup` (total: 13)

### Metrics
- **Tools:** 45 (unchanged)
- **Resources:** 4 (unchanged)
- **Prompts:** 13 (+3)

---

## [0.6.3] - 2025-12-17

### Phase 6.3: Agentic Scene Design

**Summary:** AI can programmatically create and manipulate OBS sources.

### Added
- **Design Tool Group**: 14 new tools for scene design
  - Source Creation: `create_text_source`, `create_image_source`, `create_color_source`, `create_browser_source`, `create_media_source`
  - Layout Control: `set_source_transform`, `get_source_transform`, `set_source_crop`, `set_source_bounds`, `set_source_order`
  - Advanced: `set_source_locked`, `duplicate_source`, `remove_source`, `list_input_kinds`

### Metrics
- **Tools:** 45 (+14)
- **Resources:** 4 (unchanged)
- **Prompts:** 10 (unchanged)

---

## [0.6.2] - 2025-12-17

### Phase 6.2: TUI Dashboard

**Summary:** Terminal-based dashboard using bubbletea/lipgloss.

### Added
- TUI dashboard mode (`--tui` or `-t` flag)
- Status view with OBS connection info
- Config view with settings display
- History view with scrollable action log
- Tab navigation with keyboard shortcuts
- Auto-refresh capability

### Dependencies
- `github.com/charmbracelet/bubbletea` v1.3.3
- `github.com/charmbracelet/lipgloss` v1.1.0

---

## [0.6.1] - 2025-12-16

### Phase 6.1: Web Dashboard

**Summary:** Web-based dashboard with REST API.

### Added
- Web dashboard at `http://localhost:8765/`
- REST API endpoints:
  - `GET /api/status` - Server status
  - `GET /api/history` - Action history (supports `?limit=N`, `?tool=name`)
  - `GET /api/history/stats` - Action statistics
  - `GET /api/screenshots` - Screenshot sources
  - `GET/POST /api/config` - Configuration management
- Real-time status with auto-refresh
- Screenshot gallery with live preview
- Action history viewer with filtering
- Dark-themed responsive UI
- Action history database table

---

## [0.5.0] - 2025-12-15

### Phase 5A: Setup & Configuration

**Summary:** Tool groups, optional HTTP server, enhanced setup experience.

### Added
- **Tool Groups**: Configurable categories (Core, Visual, Layout, Audio, Sources, Design)
- First-run setup prompts for tool groups and webserver
- Optional HTTP server (can be disabled)
- Screenshot-URL resource (`obs://screenshot-url/{name}`)
- Conditional tool registration based on preferences
- Persistent configuration in SQLite

### Changed
- Tool registration now respects group preferences
- Screenshot access available via URL resource (lightweight alternative)

---

## [0.4.0] - 2025-12-15

### Phase 4: MCP Resources & Prompts

**Summary:** Expanded resources and workflow prompts.

### Added
- **Screenshot Resource**: `obs://screenshot/{name}` - Binary image blob
- **Preset Resource**: `obs://preset/{name}` - JSON configuration
- **10 MCP Prompts**:
  - `stream-launch` - Pre-stream checklist
  - `stream-teardown` - End-stream cleanup
  - `audio-check` - Audio verification
  - `visual-check` - Visual layout analysis
  - `health-check` - OBS diagnostic
  - `problem-detection` - Issue detection
  - `preset-switcher` - Preset management
  - `recording-workflow` - Recording session
  - `scene-organizer` - Scene organization
  - `quick-status` - Brief status
- Prompt argument handling (required/optional)
- 57 new tests

### Metrics
- **Resources:** 4 (+2: screenshots, presets)
- **Prompts:** 10 (new)

---

## [0.3.0] - 2025-12-15

### Phase 3: Agentic Screenshot Sources

**Summary:** Enable AI visual monitoring through periodic screenshot capture.

### Added
- **Screenshot Tools** (4):
  - `create_screenshot_source` - Create periodic capture
  - `remove_screenshot_source` - Stop and remove source
  - `list_screenshot_sources` - List sources with status
  - `configure_screenshot_cadence` - Update capture interval
- HTTP server at `http://localhost:8765/screenshot/{name}`
- Background capture manager with configurable cadence
- SQLite storage for sources and images
- Automatic cleanup (keeps 10 latest per source)
- Security hardening (path traversal prevention)

### Metrics
- **Tools:** 30 (+4)

---

## [0.2.0] - 2025-12-15

### Phase 2: Scene Presets & Testing

**Summary:** Preset management and testing infrastructure.

### Added
- **Scene Preset Tools** (6):
  - `save_scene_preset` - Save source visibility states
  - `list_scene_presets` - List saved presets
  - `get_preset_details` - Get preset configuration
  - `apply_scene_preset` - Restore preset
  - `rename_scene_preset` - Rename preset
  - `delete_scene_preset` - Remove preset
- **Audio Tool**: `get_input_volume`
- OBSClient interface for dependency injection
- Mock OBS client for testing
- Comprehensive storage layer tests
- MCP tool handler tests

### Metrics
- **Tools:** 26 (+7)

---

## [0.1.0] - 2025-12-14

### Phase 1: Foundation

**Summary:** Initial MCP server with core OBS control.

### Added
- Go 1.25.5 project structure
- MCP server with stdio transport
- **Scene Resources**: `obs://scene/{name}` with notifications
- OBS event monitoring (SceneCreated, SceneRemoved, CurrentProgramSceneChanged)
- SQLite storage layer (modernc.org/sqlite, pure Go)
- Auto-detection setup flow
- Contextual error handling

### Core Tools (19)
- **Scene Management**: `list_scenes`, `set_current_scene`, `create_scene`, `remove_scene`
- **Recording**: `start_recording`, `stop_recording`, `get_recording_status`, `pause_recording`, `resume_recording`
- **Streaming**: `start_streaming`, `stop_streaming`, `get_streaming_status`
- **Sources**: `list_sources`, `toggle_source_visibility`, `get_source_settings`
- **Audio**: `get_input_mute`, `toggle_input_mute`, `set_input_volume`
- **Status**: `get_obs_status`

### Metrics
- **Tools:** 19
- **Resources:** 1 (scenes)

---

## Version Summary

| Version | Phase | Tools | Resources | Prompts | Date |
|---------|-------|-------|-----------|---------|------|
| 0.12.0 | 12 | 72 | 4 | 13 | 2025-12-21 |
| 0.11.0 | 11 | 69 | 4 | 13 | 2025-12-21 |
| 0.10.0 | 10 | 57 | 4 | 13 | 2025-12-20 |
| 0.7.0 | 7 | 45 | 4 | 13 | 2025-12-18 |
| 0.6.3 | 6.3 | 45 | 4 | 10 | 2025-12-17 |
| 0.6.2 | 6.2 | 31 | 4 | 10 | 2025-12-17 |
| 0.6.1 | 6.1 | 31 | 4 | 10 | 2025-12-16 |
| 0.5.0 | 5A | 30 | 4 | 10 | 2025-12-15 |
| 0.4.0 | 4 | 30 | 4 | 10 | 2025-12-15 |
| 0.3.0 | 3 | 30 | 2 | - | 2025-12-15 |
| 0.2.0 | 2 | 26 | 1 | - | 2025-12-15 |
| 0.1.0 | 1 | 19 | 1 | - | 2025-12-14 |
