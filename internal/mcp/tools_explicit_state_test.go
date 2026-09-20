package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every toggle gains an optional explicit state, finishing what FB-55 started on
// source visibility.
//
// The argument is the same each time and it is about retries, not ergonomics: a
// toggle's result depends on state the caller did not check, so repeating a call
// that may already have landed leaves the opposite of what was asked for. On
// audio that is a silent stream; on the virtual camera it is a dead output. Each
// tool keeps its toggle behaviour when the field is omitted, so nothing that
// works today changes. (FB-68)

func TestToggleInputMuteAcceptsAnExplicitState(t *testing.T) {
	t.Run("muted=true twice leaves it muted", func(t *testing.T) {
		server, mock := testServer(t)
		muted := true

		for i := 0; i < 2; i++ {
			_, _, err := server.handleToggleInputMute(context.Background(), nil, ToggleInputMuteInput{
				InputName: "Microphone", Muted: &muted,
			})
			require.NoError(t, err)
		}

		got, err := mock.GetInputMute("Microphone")
		require.NoError(t, err)
		assert.True(t, got, "asking twice for muted=true must leave it muted")
	})

	t.Run("omitting the state still toggles", func(t *testing.T) {
		server, mock := testServer(t)

		before, err := mock.GetInputMute("Microphone")
		require.NoError(t, err)

		_, _, err = server.handleToggleInputMute(context.Background(), nil, ToggleInputMuteInput{
			InputName: "Microphone",
		})
		require.NoError(t, err)

		after, err := mock.GetInputMute("Microphone")
		require.NoError(t, err)
		assert.NotEqual(t, before, after, "with no explicit state the tool must still flip")
	})

	t.Run("reports the resulting state", func(t *testing.T) {
		server, _ := testServer(t)
		muted := true

		_, result, err := server.handleToggleInputMute(context.Background(), nil, ToggleInputMuteInput{
			InputName: "Microphone", Muted: &muted,
		})
		require.NoError(t, err)

		res, ok := result.(map[string]interface{})
		require.True(t, ok, "the result must report the state, not just a message")
		assert.Equal(t, true, res["muted"])
	})
}

func TestToggleVirtualCamAcceptsAnExplicitState(t *testing.T) {
	t.Run("active=true twice leaves it active", func(t *testing.T) {
		server, mock := testServer(t)
		active := true

		for i := 0; i < 2; i++ {
			_, _, err := server.handleToggleVirtualCam(context.Background(), nil, ToggleOutputInput{Active: &active})
			require.NoError(t, err)
		}

		status, err := mock.GetVirtualCamStatus()
		require.NoError(t, err)
		assert.True(t, status.Active, "asking twice for active=true must leave it running")
	})

	t.Run("active=false stops it", func(t *testing.T) {
		server, mock := testServer(t)
		on, off := true, false

		_, _, err := server.handleToggleVirtualCam(context.Background(), nil, ToggleOutputInput{Active: &on})
		require.NoError(t, err)
		_, _, err = server.handleToggleVirtualCam(context.Background(), nil, ToggleOutputInput{Active: &off})
		require.NoError(t, err)

		status, err := mock.GetVirtualCamStatus()
		require.NoError(t, err)
		assert.False(t, status.Active)
	})

	t.Run("omitting the state still toggles", func(t *testing.T) {
		server, mock := testServer(t)

		before, err := mock.GetVirtualCamStatus()
		require.NoError(t, err)

		_, _, err = server.handleToggleVirtualCam(context.Background(), nil, ToggleOutputInput{})
		require.NoError(t, err)

		after, err := mock.GetVirtualCamStatus()
		require.NoError(t, err)
		assert.NotEqual(t, before.Active, after.Active)
	})
}

func TestToggleReplayBufferAcceptsAnExplicitState(t *testing.T) {
	t.Run("active=true twice leaves it active", func(t *testing.T) {
		server, mock := testServer(t)
		active := true

		for i := 0; i < 2; i++ {
			_, _, err := server.handleToggleReplayBuffer(context.Background(), nil, ToggleOutputInput{Active: &active})
			require.NoError(t, err)
		}

		status, err := mock.GetReplayBufferStatus()
		require.NoError(t, err)
		assert.True(t, status.Active)
	})

	t.Run("omitting the state still toggles", func(t *testing.T) {
		server, mock := testServer(t)

		before, err := mock.GetReplayBufferStatus()
		require.NoError(t, err)

		_, _, err = server.handleToggleReplayBuffer(context.Background(), nil, ToggleOutputInput{})
		require.NoError(t, err)

		after, err := mock.GetReplayBufferStatus()
		require.NoError(t, err)
		assert.NotEqual(t, before.Active, after.Active)
	})
}
