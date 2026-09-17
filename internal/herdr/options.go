package herdr

import (
	"log/slog"
	"time"
)

// Option overrides a New default.
type Option func(*Client)

func WithSocket(path string) Option {
	return func(c *Client) { c.socket = path }
}

func WithTimeouts(command, rpc time.Duration) Option {
	return func(c *Client) {
		c.cmdTimeout = command
		c.rpcTimeout = rpc
	}
}

func WithLogger(log *slog.Logger) Option {
	return func(c *Client) {
		if log != nil {
			c.log = log
		}
	}
}
