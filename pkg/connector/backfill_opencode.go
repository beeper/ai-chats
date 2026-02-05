package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

var _ bridgev2.BackfillingNetworkAPI = (*AIClient)(nil)

func (oc *AIClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if oc == nil || oc.opencodeBridge == nil {
		return nil, nil
	}
	if params.Portal == nil {
		return nil, nil
	}
	meta := portalMeta(params.Portal)
	if meta == nil || !meta.IsOpenCodeRoom {
		return nil, nil
	}
	return oc.opencodeBridge.FetchMessages(ctx, params)
}
