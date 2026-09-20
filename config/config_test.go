package config

import (
	"testing"
)

// TestApplyEnvOverridesAliasPrecedence checks that the canonical OBS_* variables
// win over the OBS_WEBSOCKET_* and OBS_API_* aliases, and that the aliases are
// honoured when the canonical name is unset. (FB-52)
func TestApplyEnvOverridesAliasPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantHost string
		wantPort string
		wantPass string
	}{
		{
			name:     "canonical wins over both aliases",
			env:      map[string]string{"OBS_HOST": "canon", "OBS_WEBSOCKET_HOST": "ws", "OBS_API_HOST": "api"},
			wantHost: "canon",
		},
		{
			name:     "websocket alias wins over api alias",
			env:      map[string]string{"OBS_WEBSOCKET_HOST": "ws", "OBS_API_HOST": "api"},
			wantHost: "ws",
		},
		{
			name:     "api alias used when it is the only one set",
			env:      map[string]string{"OBS_API_HOST": "api"},
			wantHost: "api",
		},
		{
			name:     "port and password follow the same order",
			env:      map[string]string{"OBS_API_PORT": "4466", "OBS_WEBSOCKET_PASSWORD": "secret"},
			wantPort: "4466",
			wantPass: "secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			c := &Config{}
			c.ApplyEnvOverrides()

			if tt.wantHost != "" && c.OBSHost != tt.wantHost {
				t.Errorf("OBSHost = %q, want %q", c.OBSHost, tt.wantHost)
			}
			if tt.wantPort != "" && c.OBSPort != tt.wantPort {
				t.Errorf("OBSPort = %q, want %q", c.OBSPort, tt.wantPort)
			}
			if tt.wantPass != "" && c.OBSPassword != tt.wantPass {
				t.Errorf("OBSPassword = %q, want %q", c.OBSPassword, tt.wantPass)
			}
		})
	}
}

// TestApplyEnvOverridesNoEnvLeavesConfigAlone guards against an alias lookup
// clobbering values that came from the database.
func TestApplyEnvOverridesNoEnvLeavesConfigAlone(t *testing.T) {
	for _, k := range []string{
		"OBS_HOST", "OBS_PORT", "OBS_PASSWORD",
		"OBS_WEBSOCKET_HOST", "OBS_WEBSOCKET_PORT", "OBS_WEBSOCKET_PASSWORD",
		"OBS_API_HOST", "OBS_API_PORT", "OBS_API_PASSWORD",
	} {
		t.Setenv(k, "")
	}

	c := &Config{OBSHost: "stored", OBSPort: "4455", OBSPassword: "stored-pass"}
	c.ApplyEnvOverrides()

	if c.OBSHost != "stored" || c.OBSPort != "4455" || c.OBSPassword != "stored-pass" {
		t.Errorf("stored config was modified: %+v", *c)
	}
}
