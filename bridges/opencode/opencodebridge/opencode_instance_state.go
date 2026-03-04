package opencodebridge

import (
	"sync"
	"time"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
)

// openCodePartState tracks the bridge-side delivery state of a single OpenCode
// message part (tool call, text chunk, etc.) so that duplicate emissions are avoided.
type openCodePartState struct {
	role                   string
	messageID              string
	partType               string
	callStatus             string
	statusReaction         string
	callSent               bool
	resultSent             bool
	textStreamStarted      bool
	textStreamEnded        bool
	reasoningStreamStarted bool
	reasoningStreamEnded   bool
	streamInputStarted     bool
	streamInputAvailable   bool
	streamOutputAvailable  bool
	streamOutputError      bool
}

// openCodeTurnState tracks whether turn-level stream events (start, step, finish)
// have been emitted for a given message within a session.
type openCodeTurnState struct {
	started  bool
	stepOpen bool
	finished bool
}

// openCodeInstance holds the runtime state for a single OpenCode server connection.
type openCodeInstance struct {
	cfg       OpenCodeInstance
	client    *opencode.Client
	connected bool
	cancel    func()

	disconnectMu    sync.Mutex
	disconnectTimer *time.Timer

	seenMu         sync.Mutex
	seenMsg        map[string]map[string]string                   // session -> message -> role
	seenPart       map[string]map[string]*openCodePartState       // session -> part -> state
	partsByMessage map[string]map[string]map[string]struct{}      // session -> message -> {part IDs}
	turnState      map[string]map[string]*openCodeTurnState       // session -> message -> turn state

	cacheMu      sync.Mutex
	messageCache map[string]*openCodeMessageCache
}

// ---------- seen-message helpers ----------

func (inst *openCodeInstance) isSeen(sessionID, messageID string) bool {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenMsg == nil {
		return false
	}
	_, exists := inst.seenMsg[sessionID][messageID]
	return exists
}

func (inst *openCodeInstance) markSeen(sessionID, messageID, role string) {
	if messageID == "" || sessionID == "" {
		return
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenMsg == nil {
		inst.seenMsg = make(map[string]map[string]string)
	}
	if inst.seenMsg[sessionID] == nil {
		inst.seenMsg[sessionID] = make(map[string]string)
	}
	inst.seenMsg[sessionID][messageID] = role
}

func (inst *openCodeInstance) seenRole(sessionID, messageID string) string {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenMsg == nil {
		return ""
	}
	return inst.seenMsg[sessionID][messageID]
}

// ---------- part-state helpers ----------

// withPartState calls fn while holding the lock, if the part state exists.
func (inst *openCodeInstance) withPartState(sessionID, partID string, fn func(ps *openCodePartState)) {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if parts, ok := inst.seenPart[sessionID]; ok {
		if state, ok := parts[partID]; ok && state != nil {
			fn(state)
		}
	}
}

// readPartState returns a value derived from the part state, or the zero value of T.
func readPartState[T any](inst *openCodeInstance, sessionID, partID string, fn func(ps *openCodePartState) T) T {
	var zero T
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	parts, ok := inst.seenPart[sessionID]
	if !ok {
		return zero
	}
	state := parts[partID]
	if state == nil {
		return zero
	}
	return fn(state)
}

func (inst *openCodeInstance) partState(sessionID, partID string) *openCodePartState {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) *openCodePartState { return ps })
}

func (inst *openCodeInstance) partFlags(sessionID, partID string) (callSent, resultSent bool) {
	type pair struct{ a, b bool }
	p := readPartState(inst, sessionID, partID, func(ps *openCodePartState) pair {
		return pair{ps.callSent, ps.resultSent}
	})
	return p.a, p.b
}

type streamFlags struct{ inputStarted, inputAvailable, outputAvailable, outputError bool }

func (inst *openCodeInstance) partStreamFlags(sessionID, partID string) streamFlags {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) streamFlags {
		return streamFlags{ps.streamInputStarted, ps.streamInputAvailable, ps.streamOutputAvailable, ps.streamOutputError}
	})
}

type textStreamFlags struct{ textStarted, textEnded, reasoningStarted, reasoningEnded bool }

func (inst *openCodeInstance) partTextStreamFlags(sessionID, partID string) textStreamFlags {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) textStreamFlags {
		return textStreamFlags{ps.textStreamStarted, ps.textStreamEnded, ps.reasoningStreamStarted, ps.reasoningStreamEnded}
	})
}

func (inst *openCodeInstance) partCallStatus(sessionID, partID string) string {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) string { return ps.callStatus })
}

func (inst *openCodeInstance) partStatusReaction(sessionID, partID string) string {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) string { return ps.statusReaction })
}

// ---------- part-state setters ----------

func (inst *openCodeInstance) setPartCallSent(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.callSent = true })
}

func (inst *openCodeInstance) setPartTextStreamStarted(sessionID, partID, kind string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) {
		if kind == "reasoning" {
			ps.reasoningStreamStarted = true
		} else {
			ps.textStreamStarted = true
		}
	})
}

func (inst *openCodeInstance) setPartTextStreamEnded(sessionID, partID, kind string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) {
		if kind == "reasoning" {
			ps.reasoningStreamEnded = true
		} else {
			ps.textStreamEnded = true
		}
	})
}

func (inst *openCodeInstance) setPartStreamInputStarted(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.streamInputStarted = true })
}

func (inst *openCodeInstance) setPartStreamInputAvailable(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.streamInputAvailable = true })
}

func (inst *openCodeInstance) setPartStreamOutputAvailable(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.streamOutputAvailable = true })
}

func (inst *openCodeInstance) setPartStreamOutputError(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.streamOutputError = true })
}

func (inst *openCodeInstance) setPartCallStatus(sessionID, partID, status string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.callStatus = status })
}

func (inst *openCodeInstance) setPartStatusReaction(sessionID, partID, reaction string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.statusReaction = reaction })
}

func (inst *openCodeInstance) setPartResultSent(sessionID, partID string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) { ps.resultSent = true })
}

func (inst *openCodeInstance) ensurePartState(sessionID, messageID, partID, role, partType string) *openCodePartState {
	if sessionID == "" || partID == "" {
		return nil
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.seenPart == nil {
		inst.seenPart = make(map[string]map[string]*openCodePartState)
	}
	parts := inst.seenPart[sessionID]
	if parts == nil {
		parts = make(map[string]*openCodePartState)
		inst.seenPart[sessionID] = parts
	}
	state := parts[partID]
	if state == nil {
		state = &openCodePartState{role: role, messageID: messageID, partType: partType}
		parts[partID] = state
	} else {
		if role != "" {
			state.role = role
		}
		if messageID != "" {
			state.messageID = messageID
		}
		if partType != "" {
			state.partType = partType
		}
	}
	if messageID != "" {
		if inst.partsByMessage == nil {
			inst.partsByMessage = make(map[string]map[string]map[string]struct{})
		}
		if inst.partsByMessage[sessionID] == nil {
			inst.partsByMessage[sessionID] = make(map[string]map[string]struct{})
		}
		if inst.partsByMessage[sessionID][messageID] == nil {
			inst.partsByMessage[sessionID][messageID] = make(map[string]struct{})
		}
		inst.partsByMessage[sessionID][messageID][partID] = struct{}{}
	}
	return state
}

func (inst *openCodeInstance) messageParts(sessionID, messageID string) map[string]*openCodePartState {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	result := make(map[string]*openCodePartState)
	if inst.partsByMessage == nil || inst.seenPart == nil {
		return result
	}
	partSet := inst.partsByMessage[sessionID][messageID]
	for partID := range partSet {
		if state, ok := inst.seenPart[sessionID][partID]; ok {
			result[partID] = state
		} else {
			result[partID] = &openCodePartState{}
		}
	}
	return result
}

func (inst *openCodeInstance) removePart(sessionID, messageID, partID string) {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if parts, ok := inst.seenPart[sessionID]; ok {
		delete(parts, partID)
	}
	if msgMap, ok := inst.partsByMessage[sessionID]; ok {
		if partSet, ok := msgMap[messageID]; ok {
			delete(partSet, partID)
			if len(partSet) == 0 {
				delete(msgMap, messageID)
			}
		}
		if len(msgMap) == 0 {
			delete(inst.partsByMessage, sessionID)
		}
	}
}

// ---------- turn-state helpers ----------

func (inst *openCodeInstance) ensureTurnState(sessionID, messageID string) *openCodeTurnState {
	if sessionID == "" || messageID == "" {
		return nil
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.turnState == nil {
		inst.turnState = make(map[string]map[string]*openCodeTurnState)
	}
	sess := inst.turnState[sessionID]
	if sess == nil {
		sess = make(map[string]*openCodeTurnState)
		inst.turnState[sessionID] = sess
	}
	state := sess[messageID]
	if state == nil {
		state = &openCodeTurnState{}
		sess[messageID] = state
	}
	return state
}

func (inst *openCodeInstance) turnStateFor(sessionID, messageID string) *openCodeTurnState {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.turnState == nil {
		return nil
	}
	return inst.turnState[sessionID][messageID]
}

func (inst *openCodeInstance) removeTurnState(sessionID, messageID string) {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.turnState == nil {
		return
	}
	sess := inst.turnState[sessionID]
	if sess == nil {
		return
	}
	delete(sess, messageID)
	if len(sess) == 0 {
		delete(inst.turnState, sessionID)
	}
}
