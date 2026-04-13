package opencode

import (
	"sort"
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

type openCodeMessageState struct {
	role string
	turn *openCodeTurnState
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

	seenMu        sync.Mutex
	knownSessions map[string]struct{}

	cacheMu        sync.Mutex
	sessionRuntime map[string]*openCodeSessionRuntime
}

func (inst *openCodeInstance) rememberSession(sessionID string) {
	if inst == nil || sessionID == "" {
		return
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if inst.knownSessions == nil {
		inst.knownSessions = make(map[string]struct{})
	}
	inst.knownSessions[sessionID] = struct{}{}
}

func (inst *openCodeInstance) forgetSession(sessionID string) {
	if inst == nil || sessionID == "" {
		return
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	delete(inst.knownSessions, sessionID)
}

func (inst *openCodeInstance) sessionIDs() []string {
	if inst == nil {
		return nil
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	out := make([]string, 0, len(inst.knownSessions))
	for sessionID := range inst.knownSessions {
		out = append(out, sessionID)
	}
	sort.Strings(out)
	return out
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
	return inst.messageStateForLocked(sessionID, messageID) != nil
}

func (inst *openCodeInstance) markSeen(sessionID, messageID, role string) {
	if messageID == "" || sessionID == "" {
		return
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	inst.ensureMessageStateLocked(sessionID, messageID).role = role
}

func (inst *openCodeInstance) seenRole(sessionID, messageID string) string {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	state := inst.messageStateForLocked(sessionID, messageID)
	if state == nil {
		return ""
	}
	return state.role
}

// ---------- part-state helpers ----------

// withPartState calls fn while holding the lock, if the part state exists.
func (inst *openCodeInstance) withPartState(sessionID, partID string, fn func(ps *openCodePartState)) {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	runtime := inst.sessionRuntimeForSeen(sessionID)
	if runtime == nil || runtime.parts == nil {
		return
	}
	if state := runtime.parts[partID]; state != nil {
		fn(state)
	}
}

// readPartState returns a value derived from the part state, or the zero value of T.
func readPartState[T any](inst *openCodeInstance, sessionID, partID string, fn func(ps *openCodePartState) T) T {
	var zero T
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	runtime := inst.sessionRuntimeForSeen(sessionID)
	if runtime == nil || runtime.parts == nil {
		return zero
	}
	state := runtime.parts[partID]
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
	runtime := inst.ensureSessionRuntime(sessionID)
	if runtime.parts == nil {
		runtime.parts = make(map[string]*openCodePartState)
	}
	state := runtime.parts[partID]
	if state == nil {
		state = &openCodePartState{role: role, messageID: messageID, partType: partType}
		runtime.parts[partID] = state
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
		msgState := inst.ensureMessageStateLocked(sessionID, messageID)
		if role != "" {
			msgState.role = role
		}
	}
	return state
}

func (inst *openCodeInstance) messageParts(sessionID, messageID string) map[string]*openCodePartState {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	result := make(map[string]*openCodePartState)
	msgState := inst.messageStateForLocked(sessionID, messageID)
	runtime := inst.sessionRuntimeForSeen(sessionID)
	if msgState == nil || runtime == nil || runtime.parts == nil {
		return result
	}
	for partID, state := range runtime.parts {
		if state == nil {
			continue
		}
		if state.messageID == messageID {
			result[partID] = state
		}
	}
	return result
}

func (inst *openCodeInstance) removePart(sessionID, messageID, partID string) {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	if runtime := inst.sessionRuntimeForSeen(sessionID); runtime != nil && runtime.parts != nil {
		delete(runtime.parts, partID)
	}
	inst.pruneMessageStateLocked(sessionID, messageID)
}

// ---------- turn-state helpers ----------

func (inst *openCodeInstance) ensureTurnState(sessionID, messageID string) *openCodeTurnState {
	if sessionID == "" || messageID == "" {
		return nil
	}
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	msgState := inst.ensureMessageStateLocked(sessionID, messageID)
	state := msgState.turn
	if state == nil {
		state = &openCodeTurnState{}
		msgState.turn = state
	}
	return state
}

func (inst *openCodeInstance) turnStateFor(sessionID, messageID string) *openCodeTurnState {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	msgState := inst.messageStateForLocked(sessionID, messageID)
	if msgState == nil {
		return nil
	}
	return msgState.turn
}

func (inst *openCodeInstance) removeTurnState(sessionID, messageID string) {
	inst.seenMu.Lock()
	defer inst.seenMu.Unlock()
	msgState := inst.messageStateForLocked(sessionID, messageID)
	if msgState == nil {
		return
	}
	msgState.turn = nil
	inst.pruneMessageStateLocked(sessionID, messageID)
}

func (inst *openCodeInstance) ensureMessageStateLocked(sessionID, messageID string) *openCodeMessageState {
	if sessionID == "" || messageID == "" {
		return nil
	}
	runtime := inst.ensureSessionRuntime(sessionID)
	if runtime.messages == nil {
		runtime.messages = make(map[string]*openCodeMessageState)
	}
	msgState := runtime.messages[messageID]
	if msgState == nil {
		msgState = &openCodeMessageState{}
		runtime.messages[messageID] = msgState
	}
	return msgState
}

func (inst *openCodeInstance) messageStateForLocked(sessionID, messageID string) *openCodeMessageState {
	runtime := inst.sessionRuntimeForSeen(sessionID)
	if runtime == nil || runtime.messages == nil {
		return nil
	}
	return runtime.messages[messageID]
}

func (inst *openCodeInstance) pruneMessageStateLocked(sessionID, messageID string) {
	runtime := inst.sessionRuntimeForSeen(sessionID)
	if runtime == nil || runtime.messages == nil {
		return
	}
	msgState := runtime.messages[messageID]
	if msgState == nil {
		return
	}
	if msgState.turn != nil || inst.messageHasPartsLocked(sessionID, messageID) || msgState.role != "" {
		return
	}
	delete(runtime.messages, messageID)
}

func (inst *openCodeInstance) messageHasPartsLocked(sessionID, messageID string) bool {
	runtime := inst.sessionRuntimeForSeen(sessionID)
	if runtime == nil || runtime.parts == nil {
		return false
	}
	for _, state := range runtime.parts {
		if state != nil && state.messageID == messageID {
			return true
		}
	}
	return false
}

func (inst *openCodeInstance) sessionRuntimeForSeen(sessionID string) *openCodeSessionRuntime {
	if sessionID == "" {
		return nil
	}
	inst.cacheMu.Lock()
	runtime := inst.sessionRuntime[sessionID]
	inst.cacheMu.Unlock()
	return runtime
}
