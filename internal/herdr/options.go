package herdr

import "time"

// Option overrides a default that New took from the environment.
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
