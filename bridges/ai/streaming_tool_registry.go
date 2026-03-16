package ai

import (
	"sort"
	"strings"
)

type streamToolRegistry struct {
	byKey      map[string]*activeToolCall
	aliasToKey map[string]string
}

func newStreamToolRegistry() *streamToolRegistry {
	return &streamToolRegistry{
		byKey:      make(map[string]*activeToolCall),
		aliasToKey: make(map[string]string),
	}
}

func streamToolItemKey(itemID string) string {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return ""
	}
	return "item:" + itemID
}

func streamToolCallKey(callID string) string {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return ""
	}
	return "call:" + callID
}

func streamToolApprovalKey(approvalID string) string {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return ""
	}
	return "approval:" + approvalID
}

func (r *streamToolRegistry) canonicalKey(key string) string {
	if r == nil {
		return ""
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	seen := map[string]struct{}{}
	for {
		next, ok := r.aliasToKey[key]
		if !ok || next == "" || next == key {
			return key
		}
		if _, exists := seen[key]; exists {
			return key
		}
		seen[key] = struct{}{}
		key = next
	}
}

func (r *streamToolRegistry) Lookup(key string) *activeToolCall {
	if r == nil {
		return nil
	}
	key = r.canonicalKey(key)
	if key == "" {
		return nil
	}
	return r.byKey[key]
}

func (r *streamToolRegistry) Upsert(key string, create func(string) *activeToolCall) (*activeToolCall, bool) {
	if r == nil {
		return nil, false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = streamToolCallKey(NewCallID())
	}
	key = r.canonicalKey(key)
	if tool, ok := r.byKey[key]; ok && tool != nil {
		return tool, false
	}
	tool := create(key)
	if tool == nil {
		return nil, false
	}
	tool.registryKey = key
	r.byKey[key] = tool
	return tool, true
}

func (r *streamToolRegistry) BindAlias(alias string, tool *activeToolCall) {
	if r == nil || tool == nil {
		return
	}
	alias = strings.TrimSpace(alias)
	if alias == "" || strings.TrimSpace(tool.registryKey) == "" {
		return
	}
	r.aliasToKey[alias] = tool.registryKey
}

func (r *streamToolRegistry) SortedKeys() []string {
	if r == nil {
		return nil
	}
	keys := make([]string, 0, len(r.byKey))
	for key, tool := range r.byKey {
		if tool == nil {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
