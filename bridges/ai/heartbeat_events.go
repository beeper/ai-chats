package ai

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"maunium.net/go/mautrix/bridgev2"
)

type HeartbeatIndicatorType string

const (
	HeartbeatIndicatorOK    HeartbeatIndicatorType = "ok"
	HeartbeatIndicatorAlert HeartbeatIndicatorType = "alert"
	HeartbeatIndicatorError HeartbeatIndicatorType = "error"
)

type HeartbeatEventPayload struct {
	TS            int64                   `json:"ts"`
	Status        string                  `json:"status"`
	To            string                  `json:"to,omitempty"`
	Preview       string                  `json:"preview,omitempty"`
	DurationMs    int64                   `json:"durationMs,omitempty"`
	HasMedia      bool                    `json:"hasMedia,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
	Channel       string                  `json:"channel,omitempty"`
	Silent        bool                    `json:"silent,omitempty"`
	IndicatorType *HeartbeatIndicatorType `json:"indicatorType,omitempty"`
}

func resolveIndicatorType(status string) *HeartbeatIndicatorType {
	switch status {
	case "ok-empty", "ok-token":
		v := HeartbeatIndicatorOK
		return &v
	case "sent":
		v := HeartbeatIndicatorAlert
		return &v
	case "failed":
		v := HeartbeatIndicatorError
		return &v
	default:
		return nil
	}
}

var heartbeatEvents struct {
	mu          sync.Mutex
	lastByLogin map[string]*HeartbeatEventPayload
	persist     map[string]*heartbeatEventPersister
}

type heartbeatEventPersister struct {
	login *bridgev2.UserLogin
	ch    chan *HeartbeatEventPayload // size=1, latest-wins
}

func heartbeatLoginKey(login *bridgev2.UserLogin) string {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return ""
	}
	return string(login.Bridge.DB.BridgeID) + "|" + string(login.ID)
}

func (p *heartbeatEventPersister) offer(evt *HeartbeatEventPayload) {
	if p == nil || evt == nil {
		return
	}
	evtCopy := *evt
	select {
	case p.ch <- &evtCopy:
		return
	default:
		// channel is full, replace existing value (latest-wins)
		select {
		case <-p.ch:
		default:
		}
		select {
		case p.ch <- &evtCopy:
		default:
		}
	}
}

func (p *heartbeatEventPersister) run() {
	if p == nil || p.login == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error().Str("panic", fmt.Sprint(r)).Msg("heartbeat event persistence worker panicked")
		}
	}()

	for evt := range p.ch {
		if evt == nil {
			continue
		}
		// Coalesce bursts: if multiple events queued, keep only the latest before writing.
		for {
			select {
			case next := <-p.ch:
				if next != nil {
					evt = next
				}
			default:
				goto write
			}
		}

	write:
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		if client, ok := p.login.Client.(*AIClient); ok && client != nil {
			_ = client.updateLoginState(ctx, func(state *loginRuntimeState) bool {
				if prev := state.LastHeartbeatEvent; prev != nil {
					if prev.TS == evt.TS && prev.Status == evt.Status && prev.Reason == evt.Reason && prev.To == evt.To && prev.Channel == evt.Channel && prev.Preview == evt.Preview {
						return false
					}
				}
				state.LastHeartbeatEvent = cloneHeartbeatEvent(evt)
				return true
			})
		}
		cancel()
	}
}

func (oc *AIClient) emitHeartbeatEvent(evt *HeartbeatEventPayload) {
	if evt == nil {
		return
	}
	if oc == nil || oc.UserLogin == nil {
		return
	}

	evtCopy := *evt

	loginKey := heartbeatLoginKey(oc.UserLogin)
	if loginKey == "" {
		return
	}

	heartbeatEvents.mu.Lock()
	if heartbeatEvents.lastByLogin == nil {
		heartbeatEvents.lastByLogin = make(map[string]*HeartbeatEventPayload)
	}
	heartbeatEvents.lastByLogin[loginKey] = &evtCopy

	if heartbeatEvents.persist == nil {
		heartbeatEvents.persist = make(map[string]*heartbeatEventPersister)
	}
	p := heartbeatEvents.persist[loginKey]
	if p == nil {
		p = &heartbeatEventPersister{
			login: oc.UserLogin,
			ch:    make(chan *HeartbeatEventPayload, 1),
		}
		heartbeatEvents.persist[loginKey] = p
		go p.run()
	} else if p.login == nil {
		// Shouldn't happen, but don't crash if it does.
		p.login = oc.UserLogin
	}
	heartbeatEvents.mu.Unlock()

	// Persist last-heartbeat best-effort with bounded concurrency (latest-wins per login).
	p.offer(&evtCopy)
}

func seedLastHeartbeatEvent(login *bridgev2.UserLogin, evt *HeartbeatEventPayload) {
	loginKey := heartbeatLoginKey(login)
	if loginKey == "" || evt == nil {
		return
	}
	evtCopy := *evt
	heartbeatEvents.mu.Lock()
	if heartbeatEvents.lastByLogin == nil {
		heartbeatEvents.lastByLogin = make(map[string]*HeartbeatEventPayload)
	}
	heartbeatEvents.lastByLogin[loginKey] = &evtCopy
	heartbeatEvents.mu.Unlock()
}

func getLastHeartbeatEventForLogin(login *bridgev2.UserLogin) *HeartbeatEventPayload {
	if login == nil {
		return nil
	}
	heartbeatEvents.mu.Lock()
	last := (*HeartbeatEventPayload)(nil)
	if heartbeatEvents.lastByLogin != nil {
		last = heartbeatEvents.lastByLogin[heartbeatLoginKey(login)]
	}
	heartbeatEvents.mu.Unlock()

	if last == nil {
			if client, ok := login.Client.(*AIClient); ok && client != nil {
				state := client.loginStateSnapshot(context.Background())
				if state.LastHeartbeatEvent != nil {
					seedLastHeartbeatEvent(login, state.LastHeartbeatEvent)
					return cloneHeartbeatEvent(state.LastHeartbeatEvent)
				}
			}
		return nil
	}
	eventsCopy := *last
	return &eventsCopy
}
