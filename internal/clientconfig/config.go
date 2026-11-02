// Package clientconfig defines validated monitoring-client build settings.
package clientconfig

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Baked is injected into release binaries; runtime CLI arguments cannot alter it.
var Baked string

type Config struct {
	RetrySeconds          int `json:"retrySeconds"`
	MaxRetrySeconds       int `json:"maxRetrySeconds"`
	HeartbeatSeconds      int `json:"heartbeatSeconds"`
	ConnectTimeoutSeconds int `json:"connectTimeoutSeconds"`
}

func Default() Config { return Config{10, 60, 15, 5} }
func (c Config) Validate() error {
	if c.RetrySeconds < 1 || c.RetrySeconds > 3600 {
		return fmt.Errorf("retry interval must be 1–3600 seconds")
	}
	if c.MaxRetrySeconds < c.RetrySeconds || c.MaxRetrySeconds > 3600 {
		return fmt.Errorf("maximum retry must be between the retry interval and 3600 seconds")
	}
	if c.HeartbeatSeconds < 5 || c.HeartbeatSeconds > 30 {
		return fmt.Errorf("heartbeat must be 5–30 seconds")
	}
	if c.ConnectTimeoutSeconds < 1 || c.ConnectTimeoutSeconds > 60 {
		return fmt.Errorf("connection timeout must be 1–60 seconds")
	}
	return nil
}
func (c Config) Encode() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b), err
}
func Read() (Config, error) {
	c := Default()
	if Baked != "" {
		b, err := base64.StdEncoding.DecodeString(Baked)
		if err != nil {
			return c, err
		}
		if err := json.Unmarshal(b, &c); err != nil {
			return c, err
		}
	}
	return c, c.Validate()
}
