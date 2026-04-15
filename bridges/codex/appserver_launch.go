package codex

import (
	"fmt"
	"strings"

	"github.com/beeper/agentremote/managedruntime"
)

type appServerLaunch struct {
	Args         []string
	WebSocketURL string
}

func (cc *CodexConnector) resolveAppServerLaunch() (appServerLaunch, error) {
	listen := ""
	if cc != nil && cc.Config.Codex != nil {
		listen = strings.TrimSpace(cc.Config.Codex.Listen)
	}
	if listen == "" {
		wsURL, err := managedruntime.AllocateLoopbackURL("ws")
		if err != nil {
			return appServerLaunch{}, err
		}
		return appServerLaunch{
			Args:         []string{"app-server", "--listen", wsURL},
			WebSocketURL: wsURL,
		}, nil
	}

	switch {
	case strings.HasPrefix(strings.ToLower(listen), "ws://"):
		return appServerLaunch{
			Args:         []string{"app-server", "--listen", listen},
			WebSocketURL: listen,
		}, nil
	default:
		return appServerLaunch{}, fmt.Errorf("unsupported codex.listen value %q (expected ws://IP:PORT, or blank for auto loopback websocket)", listen)
	}
}
