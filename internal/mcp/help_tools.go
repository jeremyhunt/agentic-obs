package mcp

// toolHelpContent maps tool names to their detailed help text.
// This is separate from help_content.go for maintainability.
var toolHelpContent = map[string]string{
	// Core - Scenes
	"list_scenes": `# list_scenes

**Category**: Core - Scene Management

**Description**: List all available scenes in OBS and identify the current active scene.

**Input**: None

**Output**:
- scenes: Array of scene names
- current_scene: Name of currently active scene

**Example**:
{
  "scenes": ["Scene 1", "Gaming", "BRB"],
  "current_scene": "Gaming"
}`,

	"set_current_scene": `# set_current_scene

**Category**: Core - Scene Management

**Description**: Switch to a different scene in OBS.

**Input**:
- scene_name (string, required): Name of scene to switch to

**Output**:
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming"
}`,

	"create_scene": `# create_scene

**Category**: Core - Scene Management

**Description**: Create a new empty scene in OBS.

**Input**:
- scene_name (string, required): Name for the new scene

**Output**:
- message: Success confirmation

**Example Input**:
{
  "scene_name": "New Tutorial Scene"
}

**Note**: Scene names must be unique. Use list_scenes to check existing scenes.`,

	"remove_scene": `# remove_scene

**Category**: Core - Scene Management

**Description**: Remove a scene from OBS. Cannot remove currently active scene.

**Input**:
- scene_name (string, required): Name of scene to remove

**Output**:
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Old Scene"
}

**Warning**: This permanently deletes the scene and all its source configurations.`,

	// Core - Recording
	"start_recording": `# start_recording

**Category**: Core - Recording

**Description**: Start recording in OBS using configured output settings.

**Input**: None

**Output**:
- message: Success confirmation

**Requirements**: OBS recording output must be configured (Settings > Output > Recording)`,

	"stop_recording": `# stop_recording

**Category**: Core - Recording

**Description**: Stop the current recording and finalize the output file.

**Input**: None

**Output**:
- message: Success confirmation with output file path

**Example Output**:
{
  "message": "Successfully stopped recording. Output saved to: C:/Videos/2024-01-15_12-30-45.mp4"
}`,

	"get_recording_status": `# get_recording_status

**Category**: Core - Recording

**Description**: Check current recording state and statistics.

**Input**: None

**Output**:
- output_active: Whether recording is active (bool)
- output_paused: Whether recording is paused (bool)
- output_duration: Recording duration in milliseconds
- output_bytes: Total bytes recorded

**Example Output**:
{
  "output_active": true,
  "output_paused": false,
  "output_duration": 125000,
  "output_bytes": 52428800
}`,

	"pause_recording": `# pause_recording

**Category**: Core - Recording

**Description**: Pause an active recording. Recording must be in progress.

**Input**: None

**Output**:
- message: Success confirmation

**Note**: Use resume_recording to continue. Not all output formats support pausing.`,

	"resume_recording": `# resume_recording

**Category**: Core - Recording

**Description**: Resume a paused recording. Recording must be paused.

**Input**: None

**Output**:
- message: Success confirmation`,

	// Core - Streaming
	"start_streaming": `# start_streaming

**Category**: Core - Streaming

**Description**: Start streaming using configured stream settings.

**Input**: None

**Output**:
- message: Success confirmation

**Requirements**: OBS stream settings must be configured (Settings > Stream)`,

	"stop_streaming": `# stop_streaming

**Category**: Core - Streaming

**Description**: Stop the current stream.

**Input**: None

**Output**:
- message: Success confirmation`,

	"get_streaming_status": `# get_streaming_status

**Category**: Core - Streaming

**Description**: Check current streaming state and statistics.

**Input**: None

**Output**:
- output_active: Whether streaming is active (bool)
- output_reconnecting: Whether attempting to reconnect (bool)
- output_duration: Stream duration in milliseconds
- output_bytes: Total bytes sent

**Example Output**:
{
  "output_active": true,
  "output_reconnecting": false,
  "output_duration": 3600000,
  "output_bytes": 1073741824
}`,

	// Core - Status
	"get_obs_status": `# get_obs_status

**Category**: Core - Status

**Description**: Get comprehensive OBS status including version, scenes, and output states.

**Input**: None

**Output**:
- version: OBS Studio version
- websocket_version: OBS WebSocket version
- current_scene: Active scene name
- recording_active: Recording state
- streaming_active: Streaming state
- video: Canvas and frame rate -- base_width/base_height (the coordinate
  space scene item transforms live in), output_width/output_height (what is
  actually encoded), and fps_numerator/fps_denominator. Read the canvas
  before computing any placement: it is frequently not 1920x1080, and a
  layout built on that assumption lands off-screen rather than off-centre.

**Example Output**:
{
  "version": "30.0.0",
  "websocket_version": "5.5.6",
  "current_scene": "Gaming",
  "recording_active": false,
  "streaming_active": true
}

**Use Case**: Verify OBS connection and overall state before starting operations.`,

	// Sources
	"list_sources": `# list_sources

**Category**: Sources

**Description**: List all input sources (audio and video) available in OBS.

**Input**: None

**Output**: Array of source objects with:
- name: Source name
- type: Source type/kind
- type_id: OBS internal type ID

**Example Output**:
[
  {"name": "Webcam", "type": "Video Capture Device", "type_id": "dshow_input"},
  {"name": "Microphone", "type": "Audio Input Capture", "type_id": "wasapi_input_capture"}
]`,

	"toggle_source_visibility": `# toggle_source_visibility

**Category**: Sources

**Description**: Show or hide a source in a scene. Supply 'visible' to set an
explicit state, or omit it to flip whatever the current state is.

**Input**:
- scene_name (string, required): Name of the scene containing the source
- source_id (int, required): Scene item ID of the source
- visible (bool, optional): Explicit state. Omit to toggle.

**Output**:
- scene_name: Scene name
- source_id: Scene item ID
- visible: The resulting visibility state

**Example Input**:
{
  "scene_name": "Main",
  "source_id": 3,
  "visible": true
}

**Note**: Prefer passing visible. A bare toggle is not safe to retry -- if a call
times out and you repeat it, the source ends up back where it started. With an
explicit state the call is idempotent.`,

	"get_source_settings": `# get_source_settings

**Category**: Sources

**Description**: Retrieve configuration settings for a specific source.

**Input**:
- source_name (string, required): Name of source

**Output**: Source settings object (varies by source type)

**Example Input**:
{
  "source_name": "Webcam"
}

**Use Case**: Inspect source configuration, useful for debugging or verification.`,

	"set_source_settings": `# set_source_settings

**Category**: Sources

**Description**: Write a source's own settings. This is how you change what a
source *is* -- a browser source's URL, a text source's text, an image source's
file -- as opposed to where it sits in a scene.

**Input**:
- source_name (string, required): Name of source
- settings (object, required): Settings to apply
- overlay (bool, optional, default true): Merge into existing settings. Pass
  false to replace them entirely, which resets every key you do not supply back
  to its default.

**Output**:
- source_name: Source that was written
- overlay: Whether the write merged or replaced

**Example Input**:
{
  "source_name": "OVERLAY_NowPlaying",
  "settings": { "url": "http://localhost:8080/widget.html" }
}

**Use Case**: Retarget a browser source, change text content, or swap an image
file. Use get_input_default_settings first if you do not know a kind's keys.

**Caution**: overlay=false is a reset-then-apply. Reach for it when the settings
you pass are meant to be the complete state -- restoring a saved configuration,
say -- and leave it alone for single-field edits.`,

	"press_source_properties_button": `# press_source_properties_button

**Category**: Sources

**Description**: Press a button on a source's properties dialog. Some source
behaviour is reachable only this way, because the button triggers an action
rather than storing a setting.

**Input**:
- source_name (string, required): Name of source
- property_name (string, required): Name of the button property

**Output**:
- source_name, property_name: What was pressed

**Example Input**:
{
  "source_name": "OVERLAY_NowPlaying",
  "property_name": "refreshnocache"
}

**Use Case**: "refreshnocache" reloads a browser source and bypasses its cache.
Prefer it to appending a cache-busting query parameter: pressing the button
changes no stored settings, whereas rewriting the URL does, and other writers of
that URL will notice.

**Note**: Buttons are per source kind. Use list_input_property_items or the
source's properties dialog in OBS to find valid names.`,

	"get_input_default_settings": `# get_input_default_settings

**Category**: Sources

**Description**: Get the default settings for an input kind. Defaults belong to
the kind, not to any one source, so this works without creating anything.

**Input**:
- input_kind (string, required): Input kind, e.g. browser_source (see
  list_input_kinds)

**Output**: Object of default settings for that kind

**Example Input**:
{
  "input_kind": "browser_source"
}

**Use Case**: Discover what settings keys a kind accepts before calling
set_source_settings or create_source, instead of guessing names. Also useful for
telling a real change from a setting that merely equals its default.`,

	"ensure_input": `# ensure_input

**Category**: Sources

**Description**: Make an input exist in a scene, configured as described,
whatever state things are in now. This is the create-or-update idiom: the
create_* tools are create-only and fail on a second run, so re-running a setup
is safe with this and not with them.

**Input**:
- scene_name (string, required): Scene the input should appear in
- source_name (string, required): Name of the input
- input_kind (string, required): Input kind (see list_input_kinds)
- settings (object, optional): Settings to apply
- overlay (bool, optional, default true): Merge settings, or replace them when
  false

**Output**:
- action: one of created, placed, updated, unchanged
- scene_item_id: the placement's ID, so you can position it without a lookup
- scene_name, source_name, input_kind

**What each action means**:
- created   -- the input did not exist anywhere
- placed    -- the input existed in another scene and was added to this one,
               sharing the same object rather than copying it
- updated   -- the input was already here and its settings changed
- unchanged -- nothing needed doing

**Example Input**:
{
  "scene_name": "Starting Soon",
  "source_name": "OVERLAY_NowPlaying",
  "input_kind": "browser_source",
  "settings": { "url": "http://localhost:8080/widget.html", "width": 1920, "height": 1080 }
}

**Use Case**: Re-runnable scene setup. Run it once to build a scene, run it again
after editing and only what changed is written.

**Note on sharing**: placing an existing input in a second scene adds a
reference, not a copy. One overlay configured once can appear in several scenes,
and a later settings change reaches all of them. That is usually what you want;
when it is not, use a different source_name.

**Refusals**: if the name is taken by an input of a different kind, this fails
rather than guessing. Silently updating would leave you believing you have a
browser source when you have a webcam.`,

	"list_input_property_items": `# list_input_property_items

**Category**: Sources

**Description**: List the selectable items of a source property -- the contents
of a dropdown in the properties dialog.

**Input**:
- source_name (string, required): Name of source
- property_name (string, required): Name of the property to enumerate

**Output**:
- items: Array of {name, value} pairs; name is the label shown in OBS, value is
  what set_source_settings expects
- count: Number of items

**Example Input**:
{
  "source_name": "Window Capture",
  "property_name": "window"
}

**Use Case**: Find the available windows for a window_capture, monitors for a
monitor_capture, or devices for an audio input. The value, not the display name,
is what you write back with set_source_settings.`,

	// Audio
	"get_input_mute": `# get_input_mute

**Category**: Audio

**Description**: Check whether an audio input is currently muted.

**Input**:
- input_name (string, required): Name of audio input

**Output**:
- input_name: Audio input name
- is_muted: Mute state (bool)

**Example Input**:
{
  "input_name": "Microphone"
}`,

	"toggle_input_mute": `# toggle_input_mute

**Category**: Audio

**Description**: Toggle the mute state of an audio input (muted <-> unmuted).

**Input**:
- input_name (string, required): Name of audio input
- muted (bool, optional): Explicit mute state. Omit to toggle. Prefer supplying
  it: a toggle cannot be retried safely, and a mute that lands the wrong way
  round is a silent stream.

**Output**:
- message: Success confirmation

**Example Input**:
{
  "input_name": "Microphone"
}`,

	"set_input_volume": `# set_input_volume

**Category**: Audio

**Description**: Set the volume level of an audio input. Supports dB or multiplier format.

**Input**:
- input_name (string, required): Name of audio input
- volume_db (float, optional): Volume in decibels (e.g., -6.0)
- volume_mul (float, optional): Volume as linear multiplier (e.g., 0.5)

**Output**:
- message: Success confirmation

**Example Input (dB)**:
{
  "input_name": "Microphone",
  "volume_db": -6.0
}

**Example Input (multiplier)**:
{
  "input_name": "Desktop Audio",
  "volume_mul": 0.75
}

**Note**: Provide either volume_db OR volume_mul, not both. 0 dB = no change, negative = quieter.`,

	"get_input_volume": `# get_input_volume

**Category**: Audio

**Description**: Get the current volume level of an audio input in both dB and multiplier formats.

**Input**:
- input_name (string, required): Name of audio input

**Output**:
- input_name: Audio input name
- volume_db: Volume in decibels
- volume_mul: Volume as linear multiplier

**Example Input**:
{
  "input_name": "Microphone"
}

**Example Output**:
{
  "input_name": "Microphone",
  "volume_db": -6.0,
  "volume_mul": 0.5011872336272722
}`,

	// Layout - Scene Presets
	"save_scene_preset": `# save_scene_preset

**Category**: Layout - Scene Presets

**Description**: Save the current source visibility states from an OBS scene as a named preset.

**Input**:
- preset_name (string, required): Name for the new preset
- scene_name (string, required): Name of OBS scene to capture state from

**Output**:
- id: Preset ID
- preset_name: Preset name
- scene_name: Scene name
- source_count: Number of sources saved
- message: Success confirmation

**Example Input**:
{
  "preset_name": "gaming_webcam_only",
  "scene_name": "Gaming"
}

**Use Case**: Save complex scene layouts so you can restore them later with apply_scene_preset.`,

	"apply_scene_preset": `# apply_scene_preset

**Category**: Layout - Scene Presets

**Description**: Load a saved preset and apply its source visibility states to the target scene.

**Input**:
- preset_name (string, required): Name of preset to apply

**Output**:
- preset_name: Preset name
- scene_name: Scene name
- applied_count: Number of sources updated
- message: Success confirmation

**Example Input**:
{
  "preset_name": "gaming_webcam_only"
}

**Note**: Sources that no longer exist in the scene are skipped automatically.`,

	"list_scene_presets": `# list_scene_presets

**Category**: Layout - Scene Presets

**Description**: List all saved scene presets, optionally filtered by scene name.

**Input**:
- scene_name (string, optional): Filter presets for specific scene

**Output**:
- presets: Array of preset summaries (id, name, scene_name, created_at)
- count: Total number of presets

**Example Input** (all presets):
{}

**Example Input** (filtered):
{
  "scene_name": "Gaming"
}`,

	"get_preset_details": `# get_preset_details

**Category**: Layout - Scene Presets

**Description**: Get full details of a scene preset including all source states.

**Input**:
- preset_name (string, required): Name of preset

**Output**:
- id: Preset ID
- name: Preset name
- scene_name: Scene name
- sources: Array of source states (name, visible)
- created_at: Creation timestamp

**Example Input**:
{
  "preset_name": "gaming_webcam_only"
}`,

	"rename_scene_preset": `# rename_scene_preset

**Category**: Layout - Scene Presets

**Description**: Change the name of an existing scene preset.

**Input**:
- old_name (string, required): Current preset name
- new_name (string, required): New preset name

**Output**:
- message: Success confirmation

**Example Input**:
{
  "old_name": "gaming_preset_1",
  "new_name": "gaming_webcam_only"
}`,

	"delete_scene_preset": `# delete_scene_preset

**Category**: Layout - Scene Presets

**Description**: Permanently remove a scene preset from storage.

**Input**:
- preset_name (string, required): Name of preset to delete

**Output**:
- message: Success confirmation

**Example Input**:
{
  "preset_name": "old_preset"
}

**Warning**: This action cannot be undone.`,

	// Visual - Screenshot Sources
	"create_screenshot_source": `# create_screenshot_source

**Category**: Visual - Screenshot Monitoring

**Description**: Create a periodic screenshot capture source for AI visual monitoring of OBS scenes.

**Input**:
- name (string, required): Unique name for this screenshot source
- source_name (string, required): OBS scene or source name to capture
- cadence_ms (int, optional): Capture interval in milliseconds (default: 5000)
- image_format (string, optional): "png" or "jpg" (default: "png")
- image_width (int, optional): Resize width, 0 = original (default: 0)
- image_height (int, optional): Resize height, 0 = original (default: 0)
- quality (int, optional): Compression quality 0-100 (default: 80)

**Output**:
- id: Screenshot source ID
- name: Source name
- url: HTTP URL for accessing screenshots
- message: Success confirmation

**Example Input**:
{
  "name": "stream_monitor",
  "source_name": "Gaming",
  "cadence_ms": 5000,
  "image_format": "jpg",
  "quality": 85
}

**Use Case**: Enable AI to "see" your stream output for visual verification and issue detection.`,

	"remove_screenshot_source": `# remove_screenshot_source

**Category**: Visual - Screenshot Monitoring

**Description**: Stop and remove a screenshot capture source.

**Input**:
- name (string, required): Name of screenshot source to remove

**Output**:
- message: Success confirmation

**Example Input**:
{
  "name": "stream_monitor"
}

**Note**: This stops capture and deletes all stored screenshots.`,

	"list_screenshot_sources": `# list_screenshot_sources

**Category**: Visual - Screenshot Monitoring

**Description**: List all configured screenshot sources with their status and HTTP URLs.

**Input**: None

**Output**:
- sources: Array of screenshot source objects
- count: Total number of sources

**Example Output**:
{
  "sources": [
    {
      "id": 1,
      "name": "stream_monitor",
      "source_name": "Gaming",
      "cadence_ms": 5000,
      "enabled": true,
      "url": "http://localhost:8765/screenshot/stream_monitor",
      "screenshot_count": 42
    }
  ],
  "count": 1
}`,

	"take_screenshot": `# take_screenshot

**Category**: Visual

**Description**: Capture a source or scene now and return the image in the same
turn, so you can look at the result of a change immediately.

**Input**:
- source_name (string, required): Source or scene to capture
- format (string, optional): png (default), jpg, bmp, webp. The set varies by OBS
  build; get_obs_status reports it as supported_image_formats
- width (int, optional): Resize width; omit for the source's own size
- height (int, optional): Resize height; omit for the source's own size
- quality (int, optional): Compression quality 1-100, for jpg
- save_path (string, optional): Also write the image to this path

**Output**: The image itself, plus source_name, format, bytes, and saved_to when
a path was given.

**Example Input**:
{
  "source_name": "Starting Soon",
  "width": 1280
}

**Use Case**: The verify step of a visual loop -- place or resize something, take
a screenshot, look at it, adjust. This is the difference between reasoning about
coordinates and seeing where they put things.

**Versus the screenshot sources**: create_screenshot_source sets up a standing
capture on a cadence, which suits monitoring. take_screenshot is a single shot
with no source created and nothing stored; prefer it for checking your own work.

**Note on size**: a full-resolution PNG of a 2560x1440 canvas is large. Pass
width to scale it down when you only need to check placement -- the layout is
just as legible at 1280 and costs a quarter as much.`,

	"configure_screenshot_cadence": `# configure_screenshot_cadence

**Category**: Visual - Screenshot Monitoring

**Description**: Update the capture interval for a screenshot source.

**Input**:
- name (string, required): Name of screenshot source
- cadence_ms (int, required): New capture interval in milliseconds

**Output**:
- name: Screenshot source name
- cadence_ms: New cadence value
- message: Success confirmation

**Example Input**:
{
  "name": "stream_monitor",
  "cadence_ms": 10000
}

**Use Case**: Adjust monitoring frequency based on performance or monitoring needs.`,

	// Design - Source Creation
	"create_text_source": `# create_text_source

**Category**: Design - Source Creation

**Description**: Create a text/label source in a scene with customizable font and color.

**Input**:
- scene_name (string, required): Name of scene to add source to
- source_name (string, required): Name for the new text source
- text (string, required): Text content to display
- font_name (string, optional): Font face name (default: "Arial")
- font_size (int, optional): Font size in points (default: 36)
- color (int, optional): Text color as ABGR integer (default: white)
- if_exists (string, optional, default "error"): What to do if the source
  already exists. "error" refuses (the default, and what this tool has always
  done). "update" applies these settings to the existing source, reporting
  updated or unchanged. "skip" leaves it alone. Use ensure_input when
  create-or-update is what you want throughout.

**Output**:
- scene_name: Scene name
- source_name: Source name
- scene_item_id: Scene item ID for positioning
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "source_name": "Stream Title",
  "text": "Welcome to my stream!",
  "font_size": 48,
  "color": 4294967295
}

**Color Format**: ABGR format where 0xFFFFFFFF = white, 0xFF0000FF = red, 0xFF00FF00 = green, 0xFFFF0000 = blue`,

	"create_image_source": `# create_image_source

**Category**: Design - Source Creation

**Description**: Create an image source in a scene from a file path.

**Input**:
- scene_name (string, required): Name of scene to add source to
- source_name (string, required): Name for the new image source
- file_path (string, required): Path to the image file
- if_exists (string, optional, default "error"): What to do if the source
  already exists. "error" refuses (the default, and what this tool has always
  done). "update" applies these settings to the existing source, reporting
  updated or unchanged. "skip" leaves it alone. Use ensure_input when
  create-or-update is what you want throughout.

**Output**:
- scene_name: Scene name
- source_name: Source name
- scene_item_id: Scene item ID for positioning
- file_path: Image file path
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "source_name": "Logo",
  "file_path": "C:/Images/logo.png"
}

**Supported Formats**: PNG, JPG, BMP, GIF, etc.`,

	"create_color_source": `# create_color_source

**Category**: Design - Source Creation

**Description**: Create a solid color source in a scene (useful for backgrounds).

**Input**:
- scene_name (string, required): Name of scene to add source to
- source_name (string, required): Name for the new color source
- color (int, required): Color as ABGR integer
- width (int, optional): Width in pixels (default: 1920)
- height (int, optional): Height in pixels (default: 1080)
- if_exists (string, optional, default "error"): What to do if the source
  already exists. "error" refuses (the default, and what this tool has always
  done). "update" applies these settings to the existing source, reporting
  updated or unchanged. "skip" leaves it alone. Use ensure_input when
  create-or-update is what you want throughout.

**Output**:
- scene_name: Scene name
- source_name: Source name
- scene_item_id: Scene item ID for positioning
- width: Width in pixels
- height: Height in pixels
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "source_name": "Background",
  "color": 4278190080,
  "width": 1920,
  "height": 1080
}

**Color Examples**: 0xFF000000 = black, 0xFFFFFFFF = white, 0xFF1a1a1a = dark gray`,

	"create_browser_source": `# create_browser_source

**Category**: Design - Source Creation

**Description**: Create a browser source in a scene to display web content.

**Input**:
- scene_name (string, required): Name of scene to add source to
- source_name (string, required): Name for the new browser source
- url (string, required): URL to load in the browser source
- width (int, optional): Browser width in pixels (default: 800)
- height (int, optional): Browser height in pixels (default: 600)
- fps (int, optional): Frame rate (default: 30)
- if_exists (string, optional, default "error"): What to do if the source
  already exists. "error" refuses (the default, and what this tool has always
  done). "update" applies these settings to the existing source, reporting
  updated or unchanged. "skip" leaves it alone. Use ensure_input when
  create-or-update is what you want throughout.
- css (string, optional): Custom CSS injected into the page. Omit to keep OBS's
  default stylesheet, which hides scrollbars and makes the background
  transparent -- usually what an overlay wants.
- is_local_file (bool, optional): Treat url as a local file path.
- shutdown (bool, optional): Free the browser when the source is hidden. Saves
  memory; loses page state on every hide.
- restart_when_active (bool, optional): Reload the page each time the source
  becomes visible. Leave this alone unless you mean it: turning it on breaks any
  overlay that holds a connection across scene switches.

**Output**:
- scene_name: Scene name
- source_name: Source name
- scene_item_id: Scene item ID for positioning
- url: Browser URL
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "source_name": "Chat Overlay",
  "url": "https://example.com/chat-overlay",
  "width": 400,
  "height": 600,
  "fps": 30
}

**Use Cases**: Stream overlays, alerts, chat widgets, web dashboards`,

	"create_media_source": `# create_media_source

**Category**: Design - Source Creation

**Description**: Create a media/video source in a scene from a file path.

**Input**:
- scene_name (string, required): Name of scene to add source to
- source_name (string, required): Name for the new media source
- file_path (string, required): Path to the media file
- loop (bool, optional): Whether to loop the media (default: false)
- if_exists (string, optional, default "error"): What to do if the source
  already exists. "error" refuses (the default, and what this tool has always
  done). "update" applies these settings to the existing source, reporting
  updated or unchanged. "skip" leaves it alone. Use ensure_input when
  create-or-update is what you want throughout.

**Output**:
- scene_name: Scene name
- source_name: Source name
- scene_item_id: Scene item ID for positioning
- file_path: Media file path
- loop: Loop setting
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Intro",
  "source_name": "Intro Video",
  "file_path": "C:/Videos/intro.mp4",
  "loop": false
}

**Supported Formats**: MP4, MOV, AVI, MKV, WebM, etc.`,

	// Design - Layout Control
	"set_source_transform": `# set_source_transform

**Category**: Design - Layout Control

**Description**: Set position, scale, and rotation of a source in a scene.

**Input**:
- scene_name (string, required): Name of scene containing source
- scene_item_id (int, required): Scene item ID of the source
- source_name (string, optional): Source name, resolved to a scene item ID.
  Supply this *or* scene_item_id. Refused if the source is placed more than
  once in the scene, since the name cannot say which placement you mean; the
  error lists the candidate IDs.
- x (float, optional): X position in pixels
- y (float, optional): Y position in pixels
- scale_x (float, optional): X scale factor (1.0 = 100%)
- scale_y (float, optional): Y scale factor (1.0 = 100%)
- rotation (float, optional): Rotation in degrees
- fit (object, optional): Place by intent instead of coordinates. Reads the
  canvas itself, so you do not need to know the resolution.
  - mode (string, required): fit (whole source visible, letterboxed), fill
    (covers the region, overflow cropped), stretch (ignores aspect ratio),
    width, height, shrink (scales down to fit but never up), or none (clears
    the bounding box).
  - region (object, optional): {x, y, width, height} in canvas pixels. The
    whole canvas when omitted.
  - anchor (string, optional): Where the source sits in its region when there
    is spare space -- center (default), top-left, top, top-right, left, right,
    bottom-left, bottom, bottom-right.

  These map onto OBS's own bounding-box types, so OBS does the scaling. Explicit
  x/y/scale_x/scale_y still win where both are given.

**Output**:
- scene_name: Scene name
- scene_item_id: Scene item ID
- x, y, scale_x, scale_y, rotation: Applied values
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "scene_item_id": 1,
  "x": 1600,
  "y": 900,
  "scale_x": 0.25,
  "scale_y": 0.25,
  "rotation": 0
}

**Note**: Omit parameters you don't want to change. Only provided values are updated.`,

	"get_source_transform": `# get_source_transform

**Category**: Design - Layout Control

**Description**: Get the current transform properties of a source in a scene.

**Input**:
- scene_name (string, required): Name of scene containing source
- scene_item_id (int, required): Scene item ID of the source
- source_name (string, optional): Source name, resolved to a scene item ID.
  Supply this *or* scene_item_id. Refused if the source is placed more than
  once in the scene, since the name cannot say which placement you mean; the
  error lists the candidate IDs.

**Output**: Transform object with position, scale, rotation, bounds, crop, size

**Example Input**:
{
  "scene_name": "Gaming",
  "scene_item_id": 1
}

**Use Case**: Get current position/scale before adjusting, or to verify layout.`,

	"set_source_crop": `# set_source_crop

**Category**: Design - Layout Control

**Description**: Set crop values for a source in a scene (trim edges).

**Input**:
- scene_name (string, required): Name of scene containing source
- scene_item_id (int, required): Scene item ID of the source
- source_name (string, optional): Source name, resolved to a scene item ID.
  Supply this *or* scene_item_id. Refused if the source is placed more than
  once in the scene, since the name cannot say which placement you mean; the
  error lists the candidate IDs.
- crop_top (int, optional): Pixels to crop from top (default: 0)
- crop_bottom (int, optional): Pixels to crop from bottom (default: 0)
- crop_left (int, optional): Pixels to crop from left (default: 0)
- crop_right (int, optional): Pixels to crop from right (default: 0)

**Output**:
- scene_name: Scene name
- scene_item_id: Scene item ID
- crop values: Applied crop values
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "scene_item_id": 1,
  "crop_top": 50,
  "crop_bottom": 50
}`,

	"set_source_bounds": `# set_source_bounds

**Category**: Design - Layout Control

**Description**: Set bounds type and size for a source (controls scaling behavior).

**Input**:
- scene_name (string, required): Name of scene containing source
- scene_item_id (int, required): Scene item ID of the source
- source_name (string, optional): Source name, resolved to a scene item ID.
  Supply this *or* scene_item_id. Refused if the source is placed more than
  once in the scene, since the name cannot say which placement you mean; the
  error lists the candidate IDs.
- bounds_type (string, required): Bounds type (see below)
- bounds_width (float, optional): Bounds width in pixels
- bounds_height (float, optional): Bounds height in pixels

**Bounds Types**:
- OBS_BOUNDS_NONE: No bounds
- OBS_BOUNDS_STRETCH: Stretch to bounds
- OBS_BOUNDS_SCALE_INNER: Scale to fit inside bounds
- OBS_BOUNDS_SCALE_OUTER: Scale to cover bounds
- OBS_BOUNDS_SCALE_TO_WIDTH: Scale to width
- OBS_BOUNDS_SCALE_TO_HEIGHT: Scale to height
- OBS_BOUNDS_MAX_ONLY: Scale down only

**Example Input**:
{
  "scene_name": "Gaming",
  "scene_item_id": 1,
  "bounds_type": "OBS_BOUNDS_SCALE_INNER",
  "bounds_width": 1920,
  "bounds_height": 1080
}`,

	"set_source_order": `# set_source_order

**Category**: Design - Layout Control

**Description**: Set the z-order index of a source (layering, 0 = back, higher = front).

**Input**:
- scene_name (string, required): Name of scene containing source
- scene_item_id (int, required): Scene item ID of the source
- source_name (string, optional): Source name, resolved to a scene item ID.
  Supply this *or* scene_item_id. Refused if the source is placed more than
  once in the scene, since the name cannot say which placement you mean; the
  error lists the candidate IDs.
- index (int, required): New index position (0 = bottom layer)

**Output**:
- scene_name: Scene name
- scene_item_id: Scene item ID
- index: New index
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "scene_item_id": 1,
  "index": 5
}

**Use Case**: Control which sources appear in front/back (e.g., background at 0, overlays at higher indices).`,

	// Design - Advanced
	"set_source_locked": `# set_source_locked

**Category**: Design - Advanced

**Description**: Lock or unlock a source to prevent/allow accidental changes.

**Input**:
- scene_name (string, required): Name of scene containing source
- scene_item_id (int, required): Scene item ID of the source
- source_name (string, optional): Source name, resolved to a scene item ID.
  Supply this *or* scene_item_id. Refused if the source is placed more than
  once in the scene, since the name cannot say which placement you mean; the
  error lists the candidate IDs.
- locked (bool, required): Whether source should be locked

**Output**:
- scene_name: Scene name
- scene_item_id: Scene item ID
- locked: Lock state
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "scene_item_id": 1,
  "locked": true
}

**Use Case**: Lock background elements to prevent accidental movement during stream.`,

	"duplicate_source": `# duplicate_source

**Category**: Design - Advanced

**Description**: Duplicate a source within the same scene or to another scene.

**Input**:
- scene_name (string, required): Name of source scene
- scene_item_id (int, required): Scene item ID to duplicate
- source_name (string, optional): Source name, resolved to a scene item ID.
  Supply this *or* scene_item_id. Refused if the source is placed more than
  once in the scene, since the name cannot say which placement you mean; the
  error lists the candidate IDs.
- dest_scene_name (string, optional): Destination scene (default: same scene)

**Output**:
- source_scene: Source scene name
- source_item_id: Source scene item ID
- dest_scene: Destination scene name
- new_scene_item_id: New scene item ID
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "scene_item_id": 1,
  "dest_scene_name": "BRB"
}

**Use Case**: Copy configured sources between scenes without recreating.`,

	"remove_source": `# remove_source

**Category**: Design - Advanced

**Description**: Remove a source from a scene (deletes scene item, not the source itself).

**Input**:
- scene_name (string, required): Name of scene containing source
- scene_item_id (int, required): Scene item ID to remove
- source_name (string, optional): Source name, resolved to a scene item ID.
  Supply this *or* scene_item_id. Refused if the source is placed more than
  once in the scene, since the name cannot say which placement you mean; the
  error lists the candidate IDs.

**Output**:
- scene_name: Scene name
- scene_item_id: Scene item ID removed
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Gaming",
  "scene_item_id": 1
}

**Note**: This removes the source from the scene only. The source can still exist in other scenes.`,

	"list_input_kinds": `# list_input_kinds

**Category**: Design - Advanced

**Description**: List all available input source types in OBS (useful for knowing what sources can be created).

**Input**: None

**Output**:
- input_kinds: Array of available source type IDs
- count: Total number of types

**Example Output**:
{
  "input_kinds": [
    "dshow_input",
    "wasapi_input_capture",
    "browser_source",
    "image_source",
    "color_source_v3",
    "text_gdiplus_v3"
  ],
  "count": 50
}

**Use Case**: Discover available source types for your OBS installation.`,

	// Filters (FB-23)
	"list_source_filters": `# list_source_filters

**Category**: Filters

**Description**: List all filters applied to a source.

**Input**:
- source_name (string, required): Name of the source to list filters for

**Output**:
- source_name: Source name
- filters: Array of filter objects (name, kind, index, enabled)
- count: Total number of filters

**Example Input**:
{
  "source_name": "Webcam"
}

**Example Output**:
{
  "source_name": "Webcam",
  "filters": [
    {"name": "Color Correction", "kind": "color_filter_v2", "index": 0, "enabled": true},
    {"name": "Sharpen", "kind": "sharpness_filter_v2", "index": 1, "enabled": true}
  ],
  "count": 2
}`,

	"get_source_filter": `# get_source_filter

**Category**: Filters

**Description**: Get detailed information about a specific filter on a source.

**Input**:
- source_name (string, required): Name of the source containing the filter
- filter_name (string, required): Name of the filter to get details for

**Output**:
- source_name: Source name
- filter: Filter details (name, kind, index, enabled, settings)

**Example Input**:
{
  "source_name": "Webcam",
  "filter_name": "Color Correction"
}

**Use Case**: Inspect filter configuration before modifying settings.`,

	"create_source_filter": `# create_source_filter

**Category**: Filters

**Description**: Add a new filter to a source (e.g., color correction, noise suppression).

**Input**:
- source_name (string, required): Name of the source to add the filter to
- filter_name (string, required): Name for the new filter
- filter_kind (string, required): Type of filter (use list_filter_kinds to see available types)
- filter_settings (object, optional): Initial settings for the filter

**Output**:
- source_name: Source name
- filter_name: Filter name
- filter_kind: Filter type
- message: Success confirmation

**Example Input**:
{
  "source_name": "Webcam",
  "filter_name": "My Color Correction",
  "filter_kind": "color_filter_v2",
  "filter_settings": {"brightness": 0.1, "contrast": 0.05}
}

**Common Filter Types**:
- color_filter_v2: Color correction (brightness, contrast, saturation)
- sharpness_filter_v2: Image sharpening
- noise_suppress_filter_v2: Audio noise suppression
- compressor_filter: Audio compressor
- chroma_key_filter_v2: Green screen removal`,

	"remove_source_filter": `# remove_source_filter

**Category**: Filters

**Description**: Remove a filter from a source.

**Input**:
- source_name (string, required): Name of the source containing the filter
- filter_name (string, required): Name of the filter to remove

**Output**:
- source_name: Source name
- filter_name: Filter name
- message: Success confirmation

**Example Input**:
{
  "source_name": "Webcam",
  "filter_name": "Old Filter"
}

**Warning**: This action cannot be undone. The filter configuration will be lost.`,

	"toggle_source_filter": `# toggle_source_filter

**Category**: Filters

**Description**: Enable or disable a filter on a source.

**Input**:
- source_name (string, required): Name of the source containing the filter
- filter_name (string, required): Name of the filter to toggle
- filter_enabled (bool, optional): Set to true/false to enable/disable; omit to toggle

**Output**:
- source_name: Source name
- filter_name: Filter name
- filter_enabled: New enabled state
- message: Success confirmation

**Example Input** (toggle):
{
  "source_name": "Webcam",
  "filter_name": "Color Correction"
}

**Example Input** (explicit):
{
  "source_name": "Webcam",
  "filter_name": "Color Correction",
  "filter_enabled": false
}

**Use Case**: Quickly enable/disable effects without removing the filter.`,

	"set_source_filter_settings": `# set_source_filter_settings

**Category**: Filters

**Description**: Modify the configuration settings of a filter.

**Input**:
- source_name (string, required): Name of the source containing the filter
- filter_name (string, required): Name of the filter to update
- filter_settings (object, required): Settings to apply to the filter
- overlay (bool, optional): If true, merge with existing settings; if false, replace entirely (default: true)

**Output**:
- source_name: Source name
- filter_name: Filter name
- overlay: Whether overlay mode was used
- message: Success confirmation

**Example Input**:
{
  "source_name": "Webcam",
  "filter_name": "Color Correction",
  "filter_settings": {"brightness": 0.2, "saturation": 0.1},
  "overlay": true
}

**Note**: Use overlay=true to update specific settings while keeping others unchanged.`,

	"list_filter_kinds": `# list_filter_kinds

**Category**: Filters

**Description**: List all available filter types in OBS.

**Input**: None

**Output**:
- filter_kinds: Array of available filter type IDs
- count: Total number of types

**Example Output**:
{
  "filter_kinds": [
    "color_filter_v2",
    "sharpness_filter_v2",
    "noise_suppress_filter_v2",
    "compressor_filter",
    "limiter_filter",
    "gain_filter",
    "chroma_key_filter_v2"
  ],
  "count": 15
}

**Use Case**: Discover available filter types before creating filters with create_source_filter.`,

	// Transitions (FB-24)
	"list_transitions": `# list_transitions

**Category**: Transitions

**Description**: List all available scene transitions and identify the current one.

**Input**: None

**Output**:
- transitions: Array of transition objects (name, kind, fixed, configurable)
- current_transition: Name of currently active transition
- count: Total number of transitions

**Example Output**:
{
  "transitions": [
    {"name": "Cut", "kind": "cut_transition", "fixed": true, "configurable": false},
    {"name": "Fade", "kind": "fade_transition", "fixed": false, "configurable": true},
    {"name": "Swipe", "kind": "swipe_transition", "fixed": false, "configurable": true}
  ],
  "current_transition": "Fade",
  "count": 3
}`,

	"get_current_transition": `# get_current_transition

**Category**: Transitions

**Description**: Get details about the current scene transition including duration and settings.

**Input**: None

**Output**:
- name: Transition name
- kind: Transition type
- duration_ms: Transition duration in milliseconds
- configurable: Whether transition has configurable settings
- settings: Current transition settings (if configurable)

**Example Output**:
{
  "name": "Fade",
  "kind": "fade_transition",
  "duration_ms": 300,
  "configurable": true,
  "settings": {}
}`,

	"set_current_transition": `# set_current_transition

**Category**: Transitions

**Description**: Change the active scene transition (e.g., Cut, Fade, Swipe).

**Input**:
- transition_name (string, required): Name of the transition to set as current

**Output**:
- transition_name: Transition name
- message: Success confirmation

**Example Input**:
{
  "transition_name": "Fade"
}

**Use Case**: Change how scenes transition during scene switches.`,

	"set_transition_duration": `# set_transition_duration

**Category**: Transitions

**Description**: Set the duration of the current scene transition in milliseconds.

**Input**:
- transition_duration (int, required): Duration in milliseconds

**Output**:
- duration_ms: New duration value
- message: Success confirmation

**Example Input**:
{
  "transition_duration": 500
}

**Note**: Typical durations range from 100ms (quick) to 1000ms (slow). Cut transition ignores duration.`,

	"trigger_transition": `# trigger_transition

**Category**: Transitions

**Description**: Trigger the current transition in studio mode (swaps preview and program scenes).

**Input**: None

**Output**:
- message: Success confirmation

**Requirements**: OBS must be in Studio Mode for this to work.

**Use Case**: Manually trigger scene changes in studio mode workflow.

**Error Handling**: Returns error if studio mode is not enabled.`,

	// =========================================================================
	// Virtual Camera Tools (FB-25)
	// =========================================================================

	"get_virtual_cam_status": `# get_virtual_cam_status

**Category**: Core (Virtual Camera)

**Description**: Check if the virtual camera is currently active.

**Input**: None

**Output**:
- active: Boolean indicating if virtual camera is running
- message: Human-readable status

**Use Case**: Check virtual camera state before starting a video call or application that uses the OBS virtual camera.`,

	"toggle_virtual_cam": `# toggle_virtual_cam

**Category**: Core (Virtual Camera)

**Description**: Start or stop the OBS virtual camera.

**Input**:
- active (bool, optional): Explicit state. Omit to toggle. Supplying it is
  safe to retry: asking for a state the output is already in does nothing.

**Output**:
- active: Boolean indicating new virtual camera state
- message: Human-readable result

**Use Case**: Enable virtual camera for video conferencing apps (Zoom, Teams, etc.) that can use OBS as a camera source.

**Note**: Virtual camera must be configured in OBS settings before use.`,

	// =========================================================================
	// Replay Buffer Tools (FB-25)
	// =========================================================================

	"get_replay_buffer_status": `# get_replay_buffer_status

**Category**: Core (Replay Buffer)

**Description**: Check if the replay buffer is currently active.

**Input**: None

**Output**:
- active: Boolean indicating if replay buffer is running
- message: Human-readable status

**Use Case**: Check replay buffer state before attempting to save clips.`,

	"toggle_replay_buffer": `# toggle_replay_buffer

**Category**: Core (Replay Buffer)

**Description**: Start or stop the replay buffer.

**Input**:
- active (bool, optional): Explicit state. Omit to toggle. Supplying it is
  safe to retry: asking for a state the output is already in does nothing.

**Output**:
- active: Boolean indicating new replay buffer state
- message: Human-readable result

**Use Case**: Enable replay buffer to capture the last N seconds of gameplay for highlights.

**Note**: Replay buffer duration and settings must be configured in OBS settings.`,

	"save_replay_buffer": `# save_replay_buffer

**Category**: Core (Replay Buffer)

**Description**: Save the current replay buffer contents to disk.

**Input**: None

**Output**:
- message: Success confirmation

**Requirements**: Replay buffer must be active.

**Use Case**: Capture a highlight moment - saves the last N seconds of recorded content.

**Error Handling**: Returns error if replay buffer is not active.`,

	"get_last_replay": `# get_last_replay

**Category**: Core (Replay Buffer)

**Description**: Get the file path of the last saved replay buffer.

**Input**: None

**Output**:
- saved_replay_path: Full path to the saved replay file
- message: Human-readable result

**Use Case**: Retrieve the location of the most recent saved replay for review or sharing.`,

	// =========================================================================
	// Studio Mode Tools (FB-26)
	// =========================================================================

	"get_studio_mode_enabled": `# get_studio_mode_enabled

**Category**: Core (Studio Mode)

**Description**: Check if studio mode is currently enabled in OBS.

**Input**: None

**Output**:
- studio_mode_enabled: Boolean indicating if studio mode is active
- message: Human-readable status

**Use Case**: Determine current mode before using preview scene features.

**Note**: Studio mode provides separate preview and program outputs, allowing scene setup before going live.`,

	"toggle_studio_mode": `# toggle_studio_mode

**Category**: Core (Studio Mode)

**Description**: Enable or disable studio mode in OBS.

**Input**:
- studio_mode_enabled (bool, required): True to enable, false to disable

**Output**:
- studio_mode_enabled: New studio mode state
- message: Success confirmation

**Example Input**:
{
  "studio_mode_enabled": true
}

**Use Case**: Enable studio mode for professional streaming workflows with preview capabilities.`,

	"get_preview_scene": `# get_preview_scene

**Category**: Core (Studio Mode)

**Description**: Get the current preview scene in studio mode.

**Input**: None

**Output**:
- preview_scene: Name of the current preview scene
- message: Human-readable result

**Requirements**: Studio mode must be enabled.

**Error Handling**: Returns error if studio mode is not enabled.`,

	"set_preview_scene": `# set_preview_scene

**Category**: Core (Studio Mode)

**Description**: Set the preview scene in studio mode.

**Input**:
- scene_name (string, required): Name of the scene to preview

**Output**:
- preview_scene: Name of the new preview scene
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Starting Soon"
}

**Requirements**: Studio mode must be enabled.

**Use Case**: Queue up the next scene without switching live output.

**Error Handling**: Returns error if studio mode is not enabled or scene doesn't exist.`,

	// =========================================================================
	// Hotkey Tools (FB-26)
	// =========================================================================

	"trigger_hotkey_by_name": `# trigger_hotkey_by_name

**Category**: Core (Hotkeys)

**Description**: Trigger an OBS hotkey by its registered name.

**Input**:
- hotkey_name (string, required): Name of the hotkey to trigger

**Output**:
- hotkey_name: The triggered hotkey name
- message: Success confirmation

**Example Input**:
{
  "hotkey_name": "OBSBasic.StartRecording"
}

**Use Case**: Trigger OBS actions that have hotkey bindings, including custom plugin hotkeys.

**Note**: Use list_hotkeys to discover available hotkey names.`,

	"list_hotkeys": `# list_hotkeys

**Category**: Core (Hotkeys)

**Description**: List all available OBS hotkey names.

**Input**: None

**Output**:
- hotkeys: Array of available hotkey names
- count: Number of hotkeys found
- message: Human-readable summary

**Use Case**: Discover available hotkeys for automation or to find the correct name for trigger_hotkey_by_name.

**Note**: Hotkey names follow the pattern "Context.Action" (e.g., "OBSBasic.StartRecording").`,

	// Meta Tools - Tool Configuration
	"get_tool_config": `# get_tool_config

**Category**: Meta Tools

**Description**: Get current tool group configuration showing which tool groups are enabled or disabled.

**Input**:
- group (string, optional): Filter by specific group name (Core, Sources, Audio, Layout, Visual, Design, Filters, Transitions)
- verbose (boolean, optional): Include list of tool names in each group (default: false)

**Output**:
- groups: Array of tool group info (name, description, enabled, tool_count, tools)
- total_tools: Total number of tools across all groups
- enabled_tools: Number of currently enabled tools
- meta_tools: List of meta-tools that cannot be disabled (help, get_tool_config, set_tool_config, list_tool_groups)
- message: Human-readable summary

**Example Input**:
{
  "group": "Audio",
  "verbose": true
}

**Use Case**: Check which tool categories are available and their enabled state. Useful for understanding available capabilities.`,

	"set_tool_config": `# set_tool_config

**Category**: Meta Tools

**Description**: Enable or disable a tool group at runtime. Changes are session-only by default.

**Input**:
- group (string, required): Tool group name to configure (Core, Sources, Audio, Layout, Visual, Design, Filters, Transitions)
- enabled (boolean, required): True to enable the group, false to disable
- persist (boolean, optional): Save configuration to database for future sessions (default: false)

**Output**:
- group: The configured group name
- previous_state: Previous enabled state (true/false)
- new_state: New enabled state (true/false)
- tools_affected: Number of tools affected by this change
- persisted: Whether the change was saved to database
- message: Human-readable confirmation

**Example Input**:
{
  "group": "Visual",
  "enabled": false,
  "persist": true
}

**Use Case**: Temporarily disable tool categories you don't need to reduce cognitive load, or permanently configure your preferred tool setup.

**Note**: Meta-tools (help, get_tool_config, set_tool_config, list_tool_groups) cannot be disabled.`,

	"list_tool_groups": `# list_tool_groups

**Category**: Meta Tools

**Description**: List all available tool groups with their descriptions and enabled status.

**Input**:
- include_disabled (boolean, optional): Include disabled groups in listing (default: true)

**Output**:
- groups: Array of tool group info (name, description, enabled, tool_count)
- count: Number of groups in response
- meta_tools: List of meta-tools that are always available
- message: Human-readable summary

**Example Input**:
{
  "include_disabled": false
}

**Use Case**: Quick overview of tool categories without detailed tool lists. Use get_tool_config with verbose=true for full tool lists.

**Tool Groups**:
- Core (25 tools): Scene management, recording, streaming, virtual camera, replay buffer, studio mode, hotkeys
- Sources (3 tools): Source visibility and settings
- Audio (4 tools): Audio input muting and volume control
- Layout (6 tools): Scene preset management
- Visual (4 tools): Screenshot capture for AI visual analysis
- Design (14 tools): Source creation and transform control
- Filters (7 tools): Source filter management
- Transitions (5 tools): Scene transition control`,

	// Automation (FB-20). These had no help entries until FB-52: the group was
	// never enabled in the shipped binary, so nobody noticed.
	"list_automation_rules": `# list_automation_rules

**Category**: Automation

**Description**: List all automation rules with their triggers, actions and current state.

**Input**:
- enabled_only (bool, optional): Only return enabled rules (default: false)

**Output**:
- rules: Array of rules with name, description, enabled, trigger_type, trigger_config, actions, cooldown_ms, priority, last_run
- count: Number of rules returned

**Example Input**:
{
  "enabled_only": true
}

**Note**: Use get_automation_rule for the full definition of a single rule.`,

	"get_automation_rule": `# get_automation_rule

**Category**: Automation

**Description**: Retrieve the full definition of one automation rule by name.

**Input**:
- name (string, required): Name of the automation rule to retrieve

**Output**:
- rule: Full rule definition including trigger_config and actions
- message: Confirmation, or an error if the rule does not exist

**Example Input**:
{
  "name": "mute-mic-on-brb"
}`,

	"create_automation_rule": `# create_automation_rule

**Category**: Automation

**Description**: Create an automation rule that runs a list of actions when a trigger fires.

**Input**:
- name (string, required): Unique name for the rule
- description (string, optional): What the rule does
- trigger_type (string, required): 'event', 'schedule', or 'manual'
- trigger_config (object, required): For 'event', {event_type, event_filter}; for 'schedule', {schedule: "<cron>"}; for 'manual', {}
- actions (array, required): Ordered list of {type, parameters, on_error}
- cooldown_ms (int, optional): Minimum time between executions (default: 0)
- priority (int, optional): Higher priority rules execute first (default: 0)
- enabled (bool, optional): Whether the rule is active (default: true)

**Event types**: scene_changed, scene_created, scene_removed, recording_started,
recording_stopped, recording_paused, recording_resumed, recording_file_changed,
streaming_started, streaming_stopped, virtual_cam_started, virtual_cam_stopped,
replay_buffer_saved, input_mute_changed, source_visibility_changed,
transition_started, studio_mode_changed

**Action types**: set_scene, toggle_mute, set_mute, set_volume, toggle_visibility,
set_visibility, start_recording, stop_recording, pause_recording, resume_recording,
start_streaming, stop_streaming, toggle_virtual_cam, start_virtual_cam,
stop_virtual_cam, toggle_replay_buffer, save_replay, trigger_hotkey,
trigger_transition, set_preview_scene, delay

**Output**:
- rule: The created rule
- message: Success confirmation

**Example Input**:
{
  "name": "mute-mic-on-brb",
  "trigger_type": "event",
  "trigger_config": {"event_type": "scene_changed", "event_filter": {"scene_name": "BRB"}},
  "actions": [{"type": "set_mute", "parameters": {"input_name": "Mic/Aux", "muted": true}}],
  "cooldown_ms": 1000
}

**Note**: on_error accepts 'continue' (default) or 'stop'. A rule whose actions
change the same state its trigger watches can re-trigger itself; set a cooldown.`,

	"update_automation_rule": `# update_automation_rule

**Category**: Automation

**Description**: Update an existing rule. Only the fields you supply are changed.

**Input**:
- name (string, required): Name of the rule to update
- new_name (string, optional): Rename the rule
- description (string, optional): New description
- trigger_type (string, optional): New trigger type
- trigger_config (object, optional): New trigger configuration
- actions (array, optional): Replacement action list
- cooldown_ms (int, optional): New cooldown
- priority (int, optional): New priority

**Output**:
- rule: The updated rule
- message: Success confirmation

**Example Input**:
{
  "name": "mute-mic-on-brb",
  "cooldown_ms": 5000
}

**Note**: Supplying actions replaces the whole list; it is not a merge.`,

	"delete_automation_rule": `# delete_automation_rule

**Category**: Automation

**Description**: Delete a rule and its execution history. Asks for confirmation.

**Input**:
- name (string, required): Name of the rule to delete

**Output**:
- message: Success confirmation

**Example Input**:
{
  "name": "mute-mic-on-brb"
}

**Note**: Irreversible, and execution history goes with it. To stop a rule
temporarily use disable_automation_rule instead.`,

	"enable_automation_rule": `# enable_automation_rule

**Category**: Automation

**Description**: Enable a rule so its trigger can fire.

**Input**:
- name (string, required): Name of the rule to enable

**Output**:
- message: Success confirmation

**Example Input**:
{
  "name": "mute-mic-on-brb"
}`,

	"disable_automation_rule": `# disable_automation_rule

**Category**: Automation

**Description**: Disable a rule without deleting it. The definition and history are kept.

**Input**:
- name (string, required): Name of the rule to disable

**Output**:
- message: Success confirmation

**Example Input**:
{
  "name": "mute-mic-on-brb"
}`,

	"trigger_automation_rule": `# trigger_automation_rule

**Category**: Automation

**Description**: Run a rule's actions immediately, ignoring its trigger. Useful for
testing a rule before arming it.

**Input**:
- name (string, required): Name of the rule to trigger

**Output**:
- execution: Result of the run, including per-action status
- message: Success confirmation

**Example Input**:
{
  "name": "mute-mic-on-brb"
}

**Note**: Manual triggering bypasses the cooldown but still records an execution.`,

	"list_rule_executions": `# list_rule_executions

**Category**: Automation

**Description**: List recent rule executions with status, duration and any error.

**Input**:
- rule_name (string, optional): Only show executions of this rule
- limit (int, optional): Maximum executions to return (default: 20, max: 100)

**Output**:
- executions: Array of {rule_name, triggered_at, status, duration_ms, error}
- count: Number returned

**Example Input**:
{
  "rule_name": "mute-mic-on-brb",
  "limit": 10
}

**Note**: This is the first place to look when a rule is not doing what you expect.`,

	// Meta
	"help": `# help

**Category**: Meta

**Description**: Get help on agentic-obs features, tools, resources, prompts and
workflows. Always available; cannot be disabled.

**Input**:
- topic (string, optional): 'overview', 'tools', 'resources', 'prompts', 'workflows',
  'troubleshooting', or any tool name (default: 'overview')
- verbose (bool, optional): Include extra detail (default: false)

**Output**:
- topic: The topic requested
- help: The help text
- verbose: Whether verbose mode was used

**Example Input**:
{
  "topic": "create_automation_rule"
}

**Note**: help with topic='tools' lists every tool by category.`,

	// Audio - WASAPI device capture
	"list_audio_devices": `# list_audio_devices

**Category**: Audio

**Description**: List the Windows audio devices (WASAPI) available for capture,
separated into playback and recording devices. Use the returned device ids with
create_audio_input.

**Input**: none

**Output**:
- output_devices: Playback devices, each {name, value}. Includes virtual outputs
  such as Voicemeeter, which is how TTS audio is commonly routed into OBS.
- input_devices: Recording devices (microphones), each {name, value}
- message: Summary of how many of each were found

**Example Input**:
{}

**Note**: Windows only; WASAPI is not available on macOS or Linux. The 'value'
field is the device id to pass to create_audio_input.`,

	"create_audio_input": `# create_audio_input

**Category**: Audio

**Description**: Add a WASAPI audio capture source to a scene.

**Input**:
- scene_name (string, required): Scene to add the source to
- source_name (string, required): Name for the new audio source
- device_kind (string, required): 'output' for playback devices (e.g. a Voicemeeter
  virtual output carrying TTS), 'input' for microphones
- device_id (string, required): Device id from list_audio_devices, or 'default'
  for the system default

**Output**:
- scene_name, source_name, scene_item_id: The created scene item
- device_kind, input_kind, device_id: What was captured and how
- message: Success confirmation

**Example Input**:
{
  "scene_name": "Main",
  "source_name": "TTS Output",
  "device_kind": "output",
  "device_id": "default"
}

**Note**: device_kind selects the capture direction and therefore the OBS input
kind: 'output' maps to wasapi_output_capture, 'input' to wasapi_input_capture.
Call list_audio_devices first unless you intend to use 'default'.`,
	// Advanced Scene Switcher (FB-51). Reached through the obs-websocket vendor
	// interface, so every one of these requires the ASS plugin to be installed.
	"ass_run_macro": `# ass_run_macro

**Category**: Advanced Scene Switcher

**Description**: Trigger a named Advanced Scene Switcher macro, optionally setting
variables first. The variables are applied atomically with the macro run, so the
macro sees them.

**Input**:
- name (string, required): Exact macro name, case-sensitive, as defined in the ASS UI
- variables (array, optional): {name, value} pairs applied before the macro runs.
  Values may be numbers or booleans; ASS stores variables as strings, so they are
  coerced.

**Output**:
- macro: The macro that was run
- variables_set: How many variables were applied
- message: Success confirmation

**Example Input**:
{
  "name": "PersonaShow_sonic_avatar",
  "variables": [{"name": "duration_ms", "value": 4000}]
}

**Note**: Requires the Advanced Scene Switcher plugin. A wrong macro name fails at
the plugin, not here, so check the spelling against the ASS UI. Use
ass_set_variables when you want to change state without firing a macro.`,

	"ass_send_message": `# ass_send_message

**Category**: Advanced Scene Switcher

**Description**: Broadcast a websocket message that ASS macros can react to through
their "Websocket message received" condition. This is the loose-coupling
alternative to naming a macro directly.

**Input**:
- message (string, required): The message to broadcast

**Output**:
- message_sent: The string that was broadcast
- message: Success confirmation

**Example Input**:
{
  "message": "stream_starting"
}

**Note**: Requires the Advanced Scene Switcher plugin. Nothing happens if no macro
has a matching condition, and that is not reported as an error -- the broadcast
succeeded, it simply had no listener.`,

	"ass_set_variables": `# ass_set_variables

**Category**: Advanced Scene Switcher

**Description**: Set several Advanced Scene Switcher variables at once, without
running a macro.

**Input**:
- variables (array, required): {name, value} pairs. Names are case-sensitive.
  Values are coerced to strings, so numbers and booleans may be passed directly.

**Output**:
- variables_set: How many were applied
- message: Success confirmation

**Example Input**:
{
  "variables": [
    {"name": "current_game", "value": "Elden Ring"},
    {"name": "viewer_count", "value": 1200}
  ]
}

**Note**: Requires the Advanced Scene Switcher plugin. ASS stores every variable as
a string; {"value": 42} becomes "42". Use ass_run_macro if the variables should be
visible to a macro that runs immediately afterwards.`,

	"ass_set_variable": `# ass_set_variable

**Category**: Advanced Scene Switcher

**Description**: Set one Advanced Scene Switcher variable. A shorthand for
ass_set_variables with a single entry.

**Input**:
- name (string, required): Variable name, case-sensitive
- value (any, required): Value, coerced to a string

**Output**:
- variable: The name that was set
- value: The coerced string value
- message: Success confirmation

**Example Input**:
{
  "name": "current_game",
  "value": "Elden Ring"
}

**Note**: Requires the Advanced Scene Switcher plugin.`,
}

// GetToolHelpContent returns the help text for a specific tool, or empty if not found.
func GetToolHelpContent(toolName string) (string, bool) {
	help, exists := toolHelpContent[toolName]
	return help, exists
}
