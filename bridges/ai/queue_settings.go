package ai

import airuntime "github.com/beeper/agentremote/pkg/runtime"

type queueResolveParams struct {
	cfg        *Config
	inlineMode airuntime.QueueMode
	inlineOpts airuntime.QueueInlineOptions
}

func resolveQueueSettings(params queueResolveParams) airuntime.QueueSettings {
	cfg := params.cfg
	queueCfg := (*QueueConfig)(nil)
	if cfg != nil && cfg.Messages != nil {
		queueCfg = cfg.Messages.Queue
	}

	resolvedMode := params.inlineMode
	if resolvedMode == "" && queueCfg != nil {
		if mode, ok := airuntime.NormalizeQueueMode(queueCfg.Mode); ok {
			resolvedMode = mode
		}
	}
	if resolvedMode == "" {
		resolvedMode = airuntime.DefaultQueueMode
	}

	debounce := (*int)(nil)
	if params.inlineOpts.DebounceMs != nil {
		debounce = params.inlineOpts.DebounceMs
	} else if queueCfg != nil && queueCfg.DebounceMs != nil {
		debounce = queueCfg.DebounceMs
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
	} else if queueCfg != nil && queueCfg.Cap != nil {
		capValue = queueCfg.Cap
	}
	cap := airuntime.DefaultQueueCap
	if capValue != nil {
		if *capValue > 0 {
			cap = *capValue
		}
	}

	dropPolicy := airuntime.QueueDropPolicy("")
	if params.inlineOpts.DropPolicy != nil {
		dropPolicy = *params.inlineOpts.DropPolicy
	} else if queueCfg != nil {
		if policy, ok := airuntime.NormalizeQueueDropPolicy(queueCfg.Drop); ok {
			dropPolicy = policy
		}
	}
	if dropPolicy == "" {
		dropPolicy = airuntime.DefaultQueueDrop
	}

	return airuntime.QueueSettings{
		Mode:       resolvedMode,
		DebounceMs: debounceMs,
		Cap:        cap,
		DropPolicy: dropPolicy,
	}
}
