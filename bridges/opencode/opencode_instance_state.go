package opencode

import (
	"sync"
	"time"

	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/bridges/opencode/api"
)

// openCodePartState tracks the bridge-side delivery state of a single OpenCode
// message part (tool call, text chunk, etc.) so that duplicate emissions are avoided.
type openCodePartState struct {
	role                   string
	messageID              string
	partType               string
	callStatus             string
	callSent               bool
	resultSent             bool
	textStreamStarted      bool
	textStreamEnded        bool
	reasoningStreamStarted bool
	reasoningStreamEnded   bool
	textContent            string
	reasoningContent       string
	streamInputStarted     bool
	streamInputAvailable   bool
	streamOutputAvailable  bool
	streamOutputError      bool
	artifactStreamSent     bool
	dataStreamSent         bool
}

// openCodeTurnState tracks whether turn-level stream events (start, step, finish)
// have been emitted for a given message within a session.
type openCodeTurnState struct {
	started  bool
	stepOpen bool
	finished bool
}

type queuedUserMessage struct {
	sessionID string
	eventID   id.EventID
	parts     []api.PartInput
}

type openCodeSessionQueue struct {
	active bool
	items  []*queuedUserMessage
}

// openCodeInstance holds the runtime state for a single OpenCode server connection.
type openCodeInstance struct {
	cfg       OpenCodeInstance
	password  string
	client    *api.Client
	process   *managedOpenCodeProcess
	connected bool
	cancel    func()

	disconnectMu    sync.Mutex
	disconnectTimer *time.Timer
	queueMu         sync.Mutex

	seenMu         sync.Mutex
	seenMsg        map[string]map[string]string              // session -> message -> role
	seenPart       map[string]map[string]*openCodePartState  // session -> part -> state
	partsByMessage map[string]map[string]map[string]struct{} // session -> message -> {part IDs}
	turnState      map[string]map[string]*openCodeTurnState  // session -> message -> turn state

	cacheMu      sync.Mutex
	messageCache map[string]*openCodeMessageCache
	sendQueue    map[string]*openCodeSessionQueue
}

// cancelAndStopTimer cancels the instance's event loop and stops its disconnect timer.
func (inst *openCodeInstance) cancelAndStopTimer() {
	if inst.cancel != nil {
		inst.cancel()
	}
	inst.cancel = nil
	inst.disconnectMu.Lock()
	inst.connected = false
	if inst.disconnectTimer != nil {
		inst.disconnectTimer.Stop()
		inst.disconnectTimer = nil
	}
	inst.disconnectMu.Unlock()
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

// forKind returns the started/ended flags for the given kind ("text" or "reasoning").
func (f textStreamFlags) forKind(kind string) (started, ended bool) {
	if kind == "reasoning" {
		return f.reasoningStarted, f.reasoningEnded
	}
	return f.textStarted, f.textEnded
}

func (inst *openCodeInstance) partTextStreamFlags(sessionID, partID string) textStreamFlags {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) textStreamFlags {
		return textStreamFlags{ps.textStreamStarted, ps.textStreamEnded, ps.reasoningStreamStarted, ps.reasoningStreamEnded}
	})
}

func (inst *openCodeInstance) partTextContent(sessionID, partID, kind string) string {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) string {
		if kind == "reasoning" {
			return ps.reasoningContent
		}
		return ps.textContent
	})
}

func (inst *openCodeInstance) partCallStatus(sessionID, partID string) string {
	return readPartState(inst, sessionID, partID, func(ps *openCodePartState) string { return ps.callStatus })
}

// ---------- part-state setters ----------

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

func (inst *openCodeInstance) appendPartTextContent(sessionID, partID, kind, delta string) {
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) {
		if kind == "reasoning" {
			ps.reasoningContent += delta
		} else {
			ps.textContent += delta
		}
	})
}

func (inst *openCodeInstance) markPartArtifactStreamSent(sessionID, partID string) bool {
	changed := false
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) {
		if !ps.artifactStreamSent {
			ps.artifactStreamSent = true
			changed = true
		}
	})
	return changed
}

func (inst *openCodeInstance) markPartDataStreamSent(sessionID, partID string) bool {
	changed := false
	inst.withPartState(sessionID, partID, func(ps *openCodePartState) {
		if !ps.dataStreamSent {
			ps.dataStreamSent = true
			changed = true
		}
	})
	return changed
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
