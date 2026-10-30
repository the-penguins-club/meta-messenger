// Package mobile exposes the Messenger engine through gomobile's Android JNI binding.
package mobile

import (
	"github.com/rs/zerolog"
	globalLog "github.com/rs/zerolog/log"
	"messenger.local/engine/internal/engine"
)

type Listener interface{ OnEvent(json string) }
type Client struct{ runtime *engine.Runtime }

func Open(dataDir string, listener Listener) (*Client, error) {
	zerolog.SetGlobalLevel(zerolog.Disabled)
	globalLog.Logger = zerolog.Nop()
	r, err := engine.OpenRuntime(dataDir, func(data string) {
		if listener != nil {
			listener.OnEvent(data)
		}
	})
	if err != nil {
		return nil, err
	}
	return &Client{runtime: r}, nil
}

// Call runs off the Android main thread. The request/response JSON matches the CLI protocol.
func (c *Client) Call(request string) string { return c.runtime.Request(request) }
func (c *Client) Close()                     { c.runtime.Close() }
