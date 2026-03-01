package connector

import "strings"

import airuntime "github.com/beeper/ai-bridge/pkg/runtime"

type queueResolveParams struct {
	cfg        *Config
	channel    string
	session    *sessionEntry
	inlineMode QueueMode
	inlineOpts QueueInlineOptions
}

func resolveQueueSettings(params queueResolveParams) QueueSettings {
	channel := strings.TrimSpace(strings.ToLower(params.channel))
	cfg := params.cfg
	queueCfg := (*QueueConfig)(nil)
	if cfg != nil && cfg.Messages != nil {
		queueCfg = cfg.Messages.Queue
	}

	resolvedMode := params.inlineMode
	if resolvedMode == "" && params.session != nil {
		if mode, ok := airuntime.NormalizeQueueMode(params.session.QueueMode); ok {
			resolvedMode = mode
		}
	}
	if resolvedMode == "" && queueCfg != nil {
		if channel != "" && queueCfg.ByChannel != nil {
			if raw, ok := queueCfg.ByChannel[channel]; ok {
				if mode, ok := airuntime.NormalizeQueueMode(raw); ok {
					resolvedMode = mode
				}
			}
		}
		if resolvedMode == "" {
			if mode, ok := airuntime.NormalizeQueueMode(queueCfg.Mode); ok {
				resolvedMode = mode
			}
		}
	}
	if resolvedMode == "" {
		resolvedMode = airuntime.DefaultQueueMode
	}

	debounce := (*int)(nil)
	if params.inlineOpts.DebounceMs != nil {
		debounce = params.inlineOpts.DebounceMs
	} else if params.session != nil && params.session.QueueDebounceMs != nil {
		debounce = params.session.QueueDebounceMs
	} else if queueCfg != nil {
		if channel != "" && queueCfg.DebounceMsByChannel != nil {
			if v, ok := queueCfg.DebounceMsByChannel[channel]; ok {
				debounce = &v
			}
		}
		if debounce == nil && queueCfg.DebounceMs != nil {
			debounce = queueCfg.DebounceMs
		}
	}

	debounceMs := airuntime.DefaultQueueDebounceMs
	if debounce != nil {
		debounceMs = *debounce
		if debounceMs < 0 {
			debounceMs = 0
		}
	}

	capValue := (*int)(nil)
	if params.inlineOpts.Cap != nil {
		capValue = params.inlineOpts.Cap
	} else if params.session != nil && params.session.QueueCap != nil {
		capValue = params.session.QueueCap
	} else if queueCfg != nil && queueCfg.Cap != nil {
		capValue = queueCfg.Cap
	}
	cap := airuntime.DefaultQueueCap
	if capValue != nil {
		if *capValue > 0 {
			cap = *capValue
		}
	}

	dropPolicy := QueueDropPolicy("")
	if params.inlineOpts.DropPolicy != nil {
		dropPolicy = *params.inlineOpts.DropPolicy
	} else if params.session != nil {
		if policy, ok := airuntime.NormalizeQueueDropPolicy(params.session.QueueDrop); ok {
			dropPolicy = policy
		}
	} else if queueCfg != nil {
		if policy, ok := airuntime.NormalizeQueueDropPolicy(queueCfg.Drop); ok {
			dropPolicy = policy
		}
	}
	if dropPolicy == "" {
		dropPolicy = airuntime.DefaultQueueDrop
	}

	return QueueSettings{
		Mode:       resolvedMode,
		DebounceMs: debounceMs,
		Cap:        cap,
		DropPolicy: dropPolicy,
	}
}
