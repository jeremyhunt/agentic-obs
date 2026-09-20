package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pngMagic is the first eight bytes of every PNG file.
var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// take_screenshot returns the image in the same turn, which is what closes the
// look-and-adjust loop. The only capture path before this was a cadence source
// storing frames in SQLite, so seeing what you just changed meant creating a
// source, waiting a tick, reading a resource and deleting the source. (FB-73)

func TestTakeScreenshot(t *testing.T) {
	t.Run("returns image content, not a description of one", func(t *testing.T) {
		server, _ := testServer(t)

		res, _, err := server.handleTakeScreenshot(context.Background(), nil, TakeScreenshotInput{
			SourceName: "Scene 1",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		require.Len(t, res.Content, 1, "one image, so the agent sees it rather than a payload to decode")

		img, ok := res.Content[0].(*mcpsdk.ImageContent)
		require.True(t, ok, "content must be an image; text would make the agent parse base64 by hand")
		assert.Equal(t, "image/png", img.MIMEType)
	})

	t.Run("carries decoded bytes, not base64 text", func(t *testing.T) {
		server, _ := testServer(t)

		res, _, err := server.handleTakeScreenshot(context.Background(), nil, TakeScreenshotInput{
			SourceName: "Scene 1",
		})
		require.NoError(t, err)

		img := res.Content[0].(*mcpsdk.ImageContent)

		// The SDK field is []byte and encoding/json base64s it on the way out.
		// Putting the base64 string's bytes in there would double-encode, and
		// the failure is silent: the payload still looks like a valid image
		// content block, and the client decodes it to gibberish.
		assert.True(t, bytes.HasPrefix(img.Data, pngMagic),
			"Data must be the decoded image; got %q...", firstBytes(img.Data, 12))
	})

	t.Run("honours the requested format", func(t *testing.T) {
		server, _ := testServer(t)

		res, _, err := server.handleTakeScreenshot(context.Background(), nil, TakeScreenshotInput{
			SourceName: "Scene 1",
			Format:     "jpg",
		})
		require.NoError(t, err)

		img := res.Content[0].(*mcpsdk.ImageContent)
		assert.Equal(t, "image/jpeg", img.MIMEType,
			"the MIME type has to follow the format, or a client renders the wrong thing")
	})

	t.Run("accepts any format this OBS build reports", func(t *testing.T) {
		server, _ := testServer(t)

		// webp was rejected by the first version of this tool, which carried a
		// hardcoded png/jpg/bmp list. Real OBS accepts it. The supported set
		// comes from Qt's image writers and varies by build, so it is asked for
		// rather than assumed. (FB-73)
		res, _, err := server.handleTakeScreenshot(context.Background(), nil, TakeScreenshotInput{
			SourceName: "Scene 1",
			Format:     "webp",
		})
		require.NoError(t, err)
		img := res.Content[0].(*mcpsdk.ImageContent)
		assert.Equal(t, "image/webp", img.MIMEType)
	})

	t.Run("rejects a format this OBS build does not report", func(t *testing.T) {
		server, _ := testServer(t)

		_, _, err := server.handleTakeScreenshot(context.Background(), nil, TakeScreenshotInput{
			SourceName: "Scene 1",
			Format:     "heif",
		})
		require.Error(t, err)
		// Naming what is available beats saying no: the caller can pick.
		assert.Contains(t, err.Error(), "png")
	})

	t.Run("writes a file when asked, and still returns the image", func(t *testing.T) {
		server, _ := testServer(t)
		path := filepath.Join(t.TempDir(), "shot.png")

		res, result, err := server.handleTakeScreenshot(context.Background(), nil, TakeScreenshotInput{
			SourceName: "Scene 1",
			SavePath:   path,
		})
		require.NoError(t, err)

		written, err := os.ReadFile(path)
		require.NoError(t, err, "save_path must actually write a file")
		assert.True(t, bytes.HasPrefix(written, pngMagic), "the file must be a decoded image")

		// Saving is in addition to returning, not instead of it: an agent that
		// asked for a file still wants to look at the result.
		require.Len(t, res.Content, 1)
		meta, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, path, meta["saved_to"])
	})

	t.Run("an unwritable save path is reported", func(t *testing.T) {
		server, _ := testServer(t)

		_, _, err := server.handleTakeScreenshot(context.Background(), nil, TakeScreenshotInput{
			SourceName: "Scene 1",
			SavePath:   filepath.Join(t.TempDir(), "no-such-dir", "shot.png"),
		})
		// Silently not writing the file the caller asked for is worse than
		// failing: they would go looking for it later.
		require.Error(t, err)
	})

	t.Run("reports the source that could not be captured", func(t *testing.T) {
		server, mock := testServer(t)
		mock.ErrorOnTakeScreenshot = assertAnError{}

		_, _, err := server.handleTakeScreenshot(context.Background(), nil, TakeScreenshotInput{
			SourceName: "Ghost",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Ghost")
	})
}

// TestTakeScreenshotDecodesWhatTheClientReturns pins the boundary: the client
// hands back base64 text, and this tool is where it stops being text.
func TestTakeScreenshotDecodesWhatTheClientReturns(t *testing.T) {
	server, mock := testServer(t)

	raw := []byte("\x89PNG\r\n\x1a\nnot really a png but shaped like one")
	mock.SetMockScreenshotData(base64.StdEncoding.EncodeToString(raw))

	res, _, err := server.handleTakeScreenshot(context.Background(), nil, TakeScreenshotInput{
		SourceName: "Scene 1",
	})
	require.NoError(t, err)

	img := res.Content[0].(*mcpsdk.ImageContent)
	assert.Equal(t, raw, img.Data)
}

type assertAnError struct{}

func (assertAnError) Error() string { return "screenshot unavailable" }

func firstBytes(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
