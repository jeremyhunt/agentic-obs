package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ironystock/agentic-obs/internal/obs"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// screenshotMIMETypes maps an image format to the MIME type a client needs to
// render it. Formats not listed still work; they are simply reported as
// image/<format>, which is right for every common case.
var screenshotMIMETypes = map[string]string{
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"tif":  "image/tiff",
	"tiff": "image/tiff",
	"svg":  "image/svg+xml",
	"ico":  "image/vnd.microsoft.icon",
}

// mimeTypeFor reports the MIME type for an image format.
func mimeTypeFor(format string) string {
	if mime, ok := screenshotMIMETypes[format]; ok {
		return mime
	}
	return "image/" + format
}

// supportedScreenshotFormats asks OBS which formats it can produce.
//
// This began as a hardcoded list of png, jpg and bmp, which was wrong the first
// time it met a real OBS: webp was accepted and the tool refused it. The set
// comes from Qt's image writers and so varies by build, which makes any list
// written here a guess that ages. obs-websocket reports its own in GetVersion,
// so ask. (FB-73)
//
// An empty result means the question could not be answered, and the caller's
// format is passed through for OBS itself to accept or reject -- refusing on the
// strength of a failed lookup would be worse than the guess it replaced.
func (s *Server) supportedScreenshotFormats() []string {
	status, err := s.obsClient.GetOBSStatus()
	if err != nil || status == nil {
		return nil
	}
	return status.SupportedImageFormats
}

// TakeScreenshotInput is the input for a one-shot capture.
type TakeScreenshotInput struct {
	SourceName string `json:"source_name" jsonschema:"Name of a source or scene to capture"`
	Format     string `json:"format,omitempty" jsonschema:"Image format: png (default), jpg, bmp, webp -- see supported_image_formats in get_obs_status"`
	Width      int    `json:"width,omitempty" jsonschema:"Resize width in pixels; omit for the source's own size"`
	Height     int    `json:"height,omitempty" jsonschema:"Resize height in pixels; omit for the source's own size"`
	Quality    int    `json:"quality,omitempty" jsonschema:"Compression quality 1-100, for jpg"`
	SavePath   string `json:"save_path,omitempty" jsonschema:"Also write the image to this path"`
}

// handleTakeScreenshot captures a source or scene and returns the image.
//
// This is what closes the look-and-adjust loop. The only capture path before it
// was a cadence source storing base64 frames in SQLite, so seeing the result of
// a change meant creating a source, waiting for a tick, reading a resource and
// deleting the source again -- four calls and a delay to answer "did that land
// where I meant". Both setup scripts in the workspace call GetSourceScreenshot
// directly for exactly this reason. (FB-73)
func (s *Server) handleTakeScreenshot(ctx context.Context, request *mcpsdk.CallToolRequest, input TakeScreenshotInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	fail := func(err error) (*mcpsdk.CallToolResult, any, error) {
		s.recordAction("take_screenshot", "Take screenshot", input, nil, false, time.Since(start))
		return nil, nil, err
	}

	format := input.Format
	if format == "" {
		format = "png"
	}
	// Validate against what this OBS build actually supports, when it will say.
	if supported := s.supportedScreenshotFormats(); len(supported) > 0 {
		if !slices.Contains(supported, format) {
			return fail(fmt.Errorf("OBS cannot produce %q; it supports %s",
				format, strings.Join(supported, ", ")))
		}
	}
	mimeType := mimeTypeFor(format)

	log.Printf("Capturing '%s' as %s", input.SourceName, format)

	encoded, err := s.obsClient.TakeSourceScreenshot(obs.ScreenshotOptions{
		SourceName: input.SourceName,
		Format:     format,
		Width:      input.Width,
		Height:     input.Height,
		Quality:    screenshotQuality(input.Quality),
	})
	if err != nil {
		return fail(fmt.Errorf("failed to capture '%s': %w", input.SourceName, err))
	}

	// The client hands back base64 text; this is where it stops being text.
	//
	// ImageContent.Data is []byte and encoding/json base64s it on the way out,
	// so putting the encoded string's bytes here would double-encode. That
	// failure is silent -- the payload still looks like a valid image block,
	// and the client decodes it to noise.
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fail(fmt.Errorf("OBS returned image data that is not valid base64: %w", err))
	}

	result := map[string]interface{}{
		"source_name": input.SourceName,
		"format":      format,
		"bytes":       len(raw),
	}

	if input.SavePath != "" {
		// Reported rather than swallowed: a caller told the file was written
		// will go looking for it later.
		if err := os.WriteFile(input.SavePath, raw, 0o644); err != nil {
			return fail(fmt.Errorf("captured '%s' but could not write %s: %w", input.SourceName, input.SavePath, err))
		}
		result["saved_to"] = input.SavePath
	}

	s.recordAction("take_screenshot", "Take screenshot", input, result, true, time.Since(start))

	// Saving is in addition to returning, never instead of it: an agent that
	// asked for a file still wants to look at what it got.
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.ImageContent{Data: raw, MIMEType: mimeType},
		},
	}, result, nil
}

// screenshotQuality maps "unset" to the client's sentinel for "library default".
func screenshotQuality(q int) int {
	if q <= 0 {
		return -1
	}
	return q
}
