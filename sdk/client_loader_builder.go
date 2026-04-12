package sdk

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

type TypedClientLoaderSpec[C bridgev2.NetworkAPI] struct {
	LoadUserLoginConfig[C]
	Accept func(*bridgev2.UserLogin) (ok bool, reason string)
}

func TypedClientLoader[C bridgev2.NetworkAPI](spec TypedClientLoaderSpec[C]) func(context.Context, *bridgev2.UserLogin) error {
	return func(_ context.Context, login *bridgev2.UserLogin) error {
		if spec.Accept != nil {
			ok, reason := spec.Accept(login)
			if !ok {
				if strings.TrimSpace(reason) == "" {
					reason = "This login is not supported."
				}
				login.Client = resolveMakeBroken(spec.MakeBroken)(login, reason)
				return nil
			}
		}
		return LoadUserLogin(login, spec.LoadUserLoginConfig)
	}
}
