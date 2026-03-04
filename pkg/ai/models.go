package ai

import "strings"

var modelRegistry = map[string]map[string]Model{}

func RegisterModels(provider string, models []Model) {
	key := strings.TrimSpace(provider)
	if key == "" {
		return
	}
	if modelRegistry[key] == nil {
		modelRegistry[key] = map[string]Model{}
	}
	for _, model := range models {
		modelRegistry[key][model.ID] = model
	}
}

func GetModel(provider, modelID string) (Model, bool) {
	models, ok := modelRegistry[provider]
	if !ok {
		return Model{}, false
	}
	model, ok := models[modelID]
	return model, ok
}

func GetProviders() []string {
	out := make([]string, 0, len(modelRegistry))
	for provider := range modelRegistry {
		out = append(out, provider)
	}
	return out
}

func GetModels(provider string) []Model {
	models, ok := modelRegistry[provider]
	if !ok {
		return nil
	}
	out := make([]Model, 0, len(models))
	for _, model := range models {
		out = append(out, model)
	}
	return out
}

func CalculateCost(model Model, usage Usage) UsageCost {
	usage.Cost.Input = (model.Cost.Input / 1_000_000) * float64(usage.Input)
	usage.Cost.Output = (model.Cost.Output / 1_000_000) * float64(usage.Output)
	usage.Cost.CacheRead = (model.Cost.CacheRead / 1_000_000) * float64(usage.CacheRead)
	usage.Cost.CacheWrite = (model.Cost.CacheWrite / 1_000_000) * float64(usage.CacheWrite)
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
	return usage.Cost
}

func SupportsXhigh(model Model) bool {
	if strings.Contains(model.ID, "gpt-5.2") || strings.Contains(model.ID, "gpt-5.3") {
		return true
	}
	if model.API == APIAnthropicMessages {
		return strings.Contains(model.ID, "opus-4-6") || strings.Contains(model.ID, "opus-4.6")
	}
	return false
}

func ModelsAreEqual(a, b *Model) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.Provider == b.Provider
}
