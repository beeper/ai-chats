package connector

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/shared/calc"
	"github.com/beeper/ai-bridge/pkg/shared/media"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
	"github.com/beeper/ai-bridge/pkg/textfs"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

// ToolDefinition defines a tool that can be used by the AI
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, args map[string]any) (string, error)
}

var imageFetchHTTPClient = &http.Client{Timeout: 30 * time.Second}

var validVoices = map[string]bool{
	"alloy": true, "ash": true, "coral": true, "echo": true,
	"fable": true, "onyx": true, "nova": true, "sage": true, "shimmer": true,
}

var validTTSModels = map[string]bool{"tts-1": true, "tts-1-hd": true}

// BridgeToolContext provides bridge-specific context for tool execution
type BridgeToolContext struct {
	Client        *AIClient
	Portal        *bridgev2.Portal
	Meta          *PortalMetadata
	SourceEventID id.EventID // The triggering message's event ID (for reactions/replies)
	SenderID      string     // The triggering sender ID (owner-only tool gating)
}

// bridgeToolContextKey is the context key for BridgeToolContext
type bridgeToolContextKey struct{}

// WithBridgeToolContext adds bridge context to a context
func WithBridgeToolContext(ctx context.Context, btc *BridgeToolContext) context.Context {
	return context.WithValue(ctx, bridgeToolContextKey{}, btc)
}

// GetBridgeToolContext retrieves bridge context from a context
func GetBridgeToolContext(ctx context.Context) *BridgeToolContext {
	return contextValue[*BridgeToolContext](ctx, bridgeToolContextKey{})
}

// BuiltinTools returns the list of available builtin tools
func BuiltinTools() []ToolDefinition {
	return buildBuiltinToolDefinitions()
}

// ToolNameMessage is the name of the message tool.
const ToolNameMessage = toolspec.MessageName

// ToolNameTTS is the name of the text-to-speech tool.
const ToolNameTTS = toolspec.TTSName

// ToolNameWebFetch is the name of the web fetch tool.
const ToolNameWebFetch = toolspec.WebFetchName

// ToolNameImage is the OpenClaw-compatible image analysis tool.
const ToolNameImage = toolspec.ImageName

// ToolNameImageGenerate is the image generation tool (non-OpenClaw).
const ToolNameImageGenerate = toolspec.ImageGenerateName

// ToolNameSessionStatus is the name of the session status tool.
const ToolNameSessionStatus = toolspec.SessionStatusName

// ToolNameCron is the name of the cron tool.
const ToolNameCron = toolspec.CronName

// Memory tool names (matching OpenClaw interface)
const (
	ToolNameMemorySearch  = toolspec.MemorySearchName
	ToolNameMemoryGet     = toolspec.MemoryGetName
	ToolNameGravatarFetch = toolspec.GravatarFetchName
	ToolNameGravatarSet   = toolspec.GravatarSetName
	ToolNameBeeperDocs    = toolspec.BeeperDocsName
	ToolNameRead          = toolspec.ReadName
	ToolNameApplyPatch    = toolspec.ApplyPatchName
	ToolNameWrite         = toolspec.WriteName
	ToolNameEdit          = toolspec.EditName
)

type memorySearchOutput struct {
	Results   []memory.SearchResult  `json:"results"`
	Provider  string                 `json:"provider,omitempty"`
	Model     string                 `json:"model,omitempty"`
	Fallback  *memory.FallbackStatus `json:"fallback,omitempty"`
	Citations string                 `json:"citations,omitempty"`
	Disabled  bool                   `json:"disabled,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

type memoryGetOutput struct {
	Path     string `json:"path"`
	Text     string `json:"text"`
	Disabled bool   `json:"disabled,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ImageResultPrefix is the prefix used to identify image results that need media sending.
const ImageResultPrefix = "IMAGE:"

// ImagesResultPrefix is the prefix used to identify multi-image results.
const ImagesResultPrefix = "IMAGES:"

// DefaultImageModel is the default model for image generation.
const DefaultImageModel = "google/gemini-3-pro-image-preview"

// DefaultOpenAIImageModel is the default direct OpenAI image model.
const DefaultOpenAIImageModel = "gpt-image-1"

// DefaultGeminiImageModel is the default direct Gemini image model.
const DefaultGeminiImageModel = "gemini-3-pro-image-preview"

// TTSResultPrefix is the prefix used to identify TTS results that need audio sending.
const TTSResultPrefix = "AUDIO:"

// normalizeMessageAction coerces message actions to canonical lowercase form.
func normalizeMessageAction(action string) string {
	return strings.ToLower(strings.TrimSpace(action))
}

// normalizeMessageArgs normalizes canonical message arguments in-place.
func normalizeMessageArgs(args map[string]any) {
	if args == nil {
		return
	}
	if raw, ok := args["message_id"]; ok {
		if value, ok := raw.(string); ok {
			args["message_id"] = normalizeMessageID(value)
		}
	}
}

func firstNonEmptyString(values ...any) string {
	for _, raw := range values {
		switch v := raw.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func normalizeMimeString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if semi := strings.IndexByte(value, ';'); semi >= 0 {
		value = value[:semi]
	}
	return strings.TrimSpace(value)
}

func messageTypeForMIME(mimeType string) event.MessageType {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return event.MsgImage
	case strings.HasPrefix(mimeType, "audio/"):
		return event.MsgAudio
	case strings.HasPrefix(mimeType, "video/"):
		return event.MsgVideo
	default:
		return event.MsgFile
	}
}

func resolveMessageMedia(ctx context.Context, btc *BridgeToolContext, bufferInput, mediaInput string) ([]byte, string, error) {
	if bufferInput != "" {
		return media.DecodeBase64(bufferInput)
	}
	if mediaInput == "" {
		return nil, "", errors.New("missing media input")
	}
	trimmed := strings.TrimSpace(mediaInput)
	if strings.HasPrefix(trimmed, "data:") {
		return nil, "", errors.New("data URLs are not supported for media; use buffer instead")
	}

	resolved, err := resolveSandboxedMediaPath(trimmed)
	if err != nil {
		return nil, "", err
	}

	b64Data, mimeType, err := btc.Client.downloadAndEncodeMedia(ctx, resolved, nil, 50)
	if err != nil {
		return nil, "", fmt.Errorf("couldn't load the media: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return nil, "", fmt.Errorf("couldn't decode the media: %w", err)
	}
	return data, mimeType, nil
}

func resolveSandboxedMediaPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("missing media input")
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "mxc://") {
		return trimmed, nil
	}
	if strings.HasPrefix(trimmed, "~") {
		return "", errors.New("media path must be relative to the workspace (no ~)")
	}

	pathValue := trimmed
	if strings.HasPrefix(trimmed, "file://") {
		parsed, err := fileURLToPath(trimmed)
		if err != nil {
			return "", err
		}
		pathValue = parsed
	}

	workspaceRoot := resolvePromptWorkspaceDir()
	if strings.TrimSpace(workspaceRoot) == "" {
		return "", errors.New("workspace root is not configured for local media access")
	}
	rootAbs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("couldn't resolve the workspace root: %w", err)
	}
	rootAbs = filepath.Clean(rootAbs)

	resolved := pathValue
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(rootAbs, resolved)
	}
	resolved = filepath.Clean(resolved)
	absResolved, err := filepath.Abs(resolved)
	if err == nil {
		resolved = absResolved
	}

	rel, err := filepath.Rel(rootAbs, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("media path must be within the workspace")
	}
	return resolved, nil
}

func resolveMessageFilename(args map[string]any, mediaInput, mimeType string) string {
	if v, ok := args["filename"].(string); ok && strings.TrimSpace(v) != "" {
		return ensureFilenameExtension(strings.TrimSpace(v), mimeType)
	}

	if mediaInput != "" && !strings.HasPrefix(mediaInput, "data:") {
		if parsed, err := url.Parse(mediaInput); err == nil && parsed.Path != "" {
			base := path.Base(parsed.Path)
			if base != "" && base != "." && base != "/" {
				return ensureFilenameExtension(base, mimeType)
			}
		}
		base := filepath.Base(mediaInput)
		if base != "" && base != "." && base != string(filepath.Separator) {
			return ensureFilenameExtension(base, mimeType)
		}
	}

	ext := extensionFromMIME(mimeType)
	if ext == "" {
		ext = ".bin"
	}
	return "file" + ext
}

func ensureFilenameExtension(fileName, mimeType string) string {
	if strings.TrimSpace(fileName) == "" {
		return fileName
	}
	if filepath.Ext(fileName) != "" {
		return fileName
	}
	ext := extensionFromMIME(mimeType)
	if ext == "" {
		return fileName
	}
	return fileName + ext
}

func extensionFromMIME(mimeType string) string {
	if mimeType == "" {
		return ""
	}
	exts, err := mime.ExtensionsByType(mimeType)
	if err != nil || len(exts) == 0 {
		return ""
	}
	return exts[0]
}

func expandUserPath(value string) string {
	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return value
		}
		trimmed := strings.TrimPrefix(value, "~")
		if trimmed == "" {
			return home
		}
		if strings.HasPrefix(trimmed, string(filepath.Separator)) {
			return filepath.Join(home, trimmed[1:])
		}
		return filepath.Join(home, trimmed)
	}
	return value
}

// executeMessage handles the message tool for sending messages and channel actions.
// Matches OpenClaw's message tool pattern with full action support.
func executeMessage(ctx context.Context, args map[string]any) (string, error) {
	action, ok := args["action"].(string)
	if !ok || action == "" {
		return "", errors.New("missing or invalid 'action' argument")
	}

	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("message tool requires bridge context")
	}

	action = normalizeMessageAction(action)
	if action == "" {
		return "", errors.New("missing or invalid 'action' argument")
	}
	normalizeMessageArgs(args)

	switch action {
	case "send":
		return executeMessageSend(ctx, args, btc)
	case "react":
		return executeMessageReact(ctx, args, btc)
	case "reactions":
		return executeMessageReactions(ctx, args, btc)
	case "edit":
		return executeMessageEdit(ctx, args, btc)
	case "delete":
		return executeMessageDelete(ctx, args, btc)
	case "reply":
		return executeMessageReply(ctx, args, btc)
	case "pin":
		return executeMessagePin(ctx, args, btc, true)
	case "unpin":
		return executeMessagePin(ctx, args, btc, false)
	case "list-pins":
		return executeMessageListPins(ctx, btc)
	case "thread-reply":
		return executeMessageThreadReply(ctx, args, btc)
	case "search":
		return executeMessageSearch(ctx, args, btc)
	case "read":
		return executeMessageRead(ctx, args, btc)
	case "member-info":
		return executeMessageMemberInfo(ctx, args, btc)
	case "channel-info":
		return executeMessageChannelInfo(ctx, args, btc)
	case "channel-edit":
		return executeMessageChannelEdit(ctx, args, btc)
	case "focus":
		return executeMessageFocus(ctx, args, btc)
	case "desktop-list-chats":
		return executeMessageDesktopListChats(ctx, args, btc)
	case "desktop-search-chats":
		return executeMessageDesktopSearchChats(ctx, args, btc)
	case "desktop-search-messages":
		return executeMessageDesktopSearchMessages(ctx, args, btc)
	case "desktop-create-chat":
		return executeMessageDesktopCreateChat(ctx, args, btc)
	case "desktop-archive-chat":
		return executeMessageDesktopArchiveChat(ctx, args, btc)
	case "desktop-set-reminder":
		return executeMessageDesktopSetReminder(ctx, args, btc)
	case "desktop-clear-reminder":
		return executeMessageDesktopClearReminder(ctx, args, btc)
	case "desktop-upload-asset":
		return executeMessageDesktopUploadAsset(ctx, args, btc)
	case "desktop-download-asset":
		return executeMessageDesktopDownloadAsset(ctx, args, btc)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// executeMessageReact handles the react action of the message tool.
// Supports adding reactions (with emoji) and removing reactions (with remove:true or empty emoji).
func executeMessageReact(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	emoji, _ := args["emoji"].(string)
	remove, _ := args["remove"].(bool)

	// Check if this is a removal request (remove:true or empty emoji)
	if remove || emoji == "" {
		return executeMessageReactRemove(ctx, args, btc)
	}

	// Get target message ID (optional - defaults to triggering message)
	var targetEventID id.EventID
	if msgID, ok := args["message_id"].(string); ok && msgID != "" {
		targetEventID = id.EventID(msgID)
	} else if btc.SourceEventID != "" {
		// Default to the triggering message (like clawdbot's currentMessageId)
		targetEventID = btc.SourceEventID
	}

	// If no target available, return error
	if targetEventID == "" {
		return "", errors.New("action=react requires 'message_id' parameter (no triggering message available)")
	}

	// Send reaction
	btc.Client.sendReaction(ctx, btc.Portal, targetEventID, emoji)

	return jsonActionResult("react", map[string]any{
		"emoji":      emoji,
		"message_id": targetEventID,
		"status":     "sent",
	})
}

// executeMessageSend handles the send action of the message tool.
func executeMessageSend(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	if handled, desktopResult, err := maybeExecuteMessageSendDesktop(ctx, args, btc); handled {
		return desktopResult, err
	}

	message, _ := args["message"].(string)
	message = strings.TrimSpace(message)
	caption, _ := args["caption"].(string)
	caption = strings.TrimSpace(caption)
	if caption != "" && message != "" && caption != message {
		caption = message + "\n\n" + caption
	} else if caption == "" {
		caption = message
	}

	bufferInput, _ := args["buffer"].(string)
	bufferInput = strings.TrimSpace(bufferInput)
	mediaInput := firstNonEmptyString(args["media"], args["path"])

	var relatesTo map[string]any
	if replyID, ok := args["message_id"].(string); ok && strings.TrimSpace(replyID) != "" {
		relatesTo = map[string]any{
			"m.in_reply_to": map[string]any{
				"event_id": strings.TrimSpace(replyID),
			},
		}
	}
	if threadID, ok := args["thread_id"].(string); ok && strings.TrimSpace(threadID) != "" {
		relatesTo = map[string]any{
			"rel_type": "m.thread",
			"event_id": strings.TrimSpace(threadID),
		}
	}

	if bufferInput == "" && mediaInput == "" {
		if message == "" {
			return "", errors.New("action=send requires 'message' parameter")
		}
		respID, err := sendFormattedMessage(ctx, btc, message, relatesTo, "failed to send message")
		if err != nil {
			return "", err
		}
		return jsonActionResult("send", map[string]any{
			"event_id": respID,
			"status":   "sent",
		})
	}

	dryRun, _ := args["dryRun"].(bool)
	if dryRun {
		return jsonActionResult("send", map[string]any{
			"status": "dry_run",
		})
	}

	data, detectedMime, err := resolveMessageMedia(ctx, btc, bufferInput, mediaInput)
	if err != nil {
		return "", err
	}

	mimeType := normalizeMimeString(firstNonEmptyString(args["mimeType"], detectedMime))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	fileName := resolveMessageFilename(args, mediaInput, mimeType)
	if caption == "" {
		caption = fileName
	}

	msgType := messageTypeForMIME(mimeType)
	asVoice, _ := args["asVoice"].(bool)
	gifPlayback, _ := args["gifPlayback"].(bool)

	intent := btc.Client.getModelIntent(ctx, btc.Portal)
	if intent == nil {
		return "", errors.New("failed to get model intent")
	}

	uri, file, err := intent.UploadMedia(ctx, btc.Portal.MXID, data, fileName, mimeType)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	info := map[string]any{
		"mimetype": mimeType,
		"size":     len(data),
	}
	if gifPlayback && msgType == event.MsgVideo {
		info["fi.mau.gif"] = true
		info["is_animated"] = true
	}
	if mimeType == "image/gif" {
		info["is_animated"] = true
	}

	rawContent := map[string]any{
		"msgtype":    msgType,
		"body":       caption,
		"info":       info,
		"m.mentions": map[string]any{},
	}
	if relatesTo != nil {
		rawContent["m.relates_to"] = relatesTo
	}
	if fileName != "" {
		rawContent["filename"] = fileName
	}
	if file != nil {
		rawContent["file"] = file
	} else {
		rawContent["url"] = string(uri)
	}
	if msgType == event.MsgImage {
		if w, h := analyzeImage(data); w > 0 && h > 0 {
			info["w"] = w
			info["h"] = h
		}
	}

	if msgType == event.MsgVideo {
		if w, h, dur := analyzeVideo(ctx, data); w > 0 && h > 0 {
			info["w"] = w
			info["h"] = h
			if dur > 0 {
				info["duration"] = dur
			}
		}
	}

	if msgType == event.MsgAudio {
		if durationMs, waveform := analyzeAudio(data, mimeType); durationMs > 0 || len(waveform) > 0 {
			if durationMs > 0 {
				info["duration"] = durationMs
			}
			rawContent["org.matrix.msc1767.audio"] = map[string]any{
				"duration": durationMs,
				"waveform": waveform,
			}
		}
		if asVoice {
			rawContent["org.matrix.msc3245.voice"] = map[string]any{}
		}
	}

	eventContent := &event.Content{Raw: rawContent}
	resp, err := intent.SendMessage(ctx, btc.Portal.MXID, event.EventMessage, eventContent, nil)
	if err != nil {
		return "", fmt.Errorf("couldn't send the media message: %w", err)
	}

	return jsonActionResult("send", map[string]any{
		"event_id":  resp.EventID,
		"status":    "sent",
		"mime_type": mimeType,
		"msgtype":   msgType,
	})
}

// executeMessageEdit handles the edit action - edits an existing message.
func executeMessageEdit(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	if handled, desktopResult, err := maybeExecuteMessageEditDesktop(ctx, args, btc); handled {
		return desktopResult, err
	}

	messageID, ok := args["message_id"].(string)
	if !ok || messageID == "" {
		return "", errors.New("action=edit requires 'message_id' parameter")
	}
	message, ok := args["message"].(string)
	if !ok || message == "" {
		return "", errors.New("action=edit requires 'message' parameter")
	}

	intent := btc.Client.getModelIntent(ctx, btc.Portal)
	if intent == nil {
		return "", errors.New("failed to get model intent")
	}

	targetEventID := id.EventID(messageID)
	rendered := format.RenderMarkdown(message, true, true)

	// Send edit with m.replace relation
	eventContent := &event.Content{
		Raw: map[string]any{
			"msgtype":        event.MsgText,
			"body":           "* " + rendered.Body,
			"format":         rendered.Format,
			"formatted_body": "* " + rendered.FormattedBody,
			"m.new_content": map[string]any{
				"msgtype":        event.MsgText,
				"body":           rendered.Body,
				"format":         rendered.Format,
				"formatted_body": rendered.FormattedBody,
				"m.mentions":     map[string]any{},
			},
			"m.relates_to": map[string]any{
				"rel_type": RelReplace,
				"event_id": targetEventID.String(),
			},
			"m.mentions": map[string]any{},
		},
	}

	resp, err := intent.SendMessage(ctx, btc.Portal.MXID, event.EventMessage, eventContent, nil)
	if err != nil {
		return "", fmt.Errorf("couldn't edit the message: %w", err)
	}

	return jsonActionResult("edit", map[string]any{
		"event_id":  resp.EventID,
		"edited_id": targetEventID,
		"status":    "sent",
	})
}

// executeMessageDelete handles the delete action - redacts a message.
func executeMessageDelete(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	messageID, ok := args["message_id"].(string)
	if !ok || messageID == "" {
		return "", errors.New("action=delete requires 'message_id' parameter")
	}

	intent := btc.Client.getModelIntent(ctx, btc.Portal)
	if intent == nil {
		return "", errors.New("failed to get model intent")
	}

	targetEventID := id.EventID(messageID)

	// Send redaction event
	_, err := intent.SendMessage(ctx, btc.Portal.MXID, event.EventRedaction, &event.Content{
		Parsed: &event.RedactionEventContent{
			Redacts: targetEventID,
		},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("couldn't delete the message: %w", err)
	}

	return jsonActionResult("delete", map[string]any{
		"deleted_id": targetEventID,
		"status":     "deleted",
	})
}

// executeMessageReply handles the reply action - sends a message as a reply to another.
func executeMessageReply(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	if handled, desktopResult, err := maybeExecuteMessageReplyDesktop(ctx, args, btc); handled {
		return desktopResult, err
	}

	messageID, ok := args["message_id"].(string)
	if !ok || messageID == "" {
		return "", errors.New("action=reply requires 'message_id' parameter")
	}
	message, ok := args["message"].(string)
	if !ok || message == "" {
		return "", errors.New("action=reply requires 'message' parameter")
	}

	targetEventID := id.EventID(messageID)
	respID, err := sendFormattedMessage(ctx, btc, message, map[string]any{
		"m.in_reply_to": map[string]any{
			"event_id": targetEventID.String(),
		},
	}, "failed to send reply")
	if err != nil {
		return "", err
	}

	return jsonActionResult("reply", map[string]any{
		"event_id": respID,
		"reply_to": targetEventID,
		"status":   "sent",
	})
}

// executeMessagePin handles pin/unpin actions - updates room pinned events.
func executeMessagePin(ctx context.Context, args map[string]any, btc *BridgeToolContext, pin bool) (string, error) {
	messageID, ok := args["message_id"].(string)
	if !ok || messageID == "" {
		action := "pin"
		if !pin {
			action = "unpin"
		}
		return "", fmt.Errorf("action=%s requires 'message_id' parameter", action)
	}

	targetEventID := id.EventID(messageID)
	bot := btc.Client.UserLogin.Bridge.Bot

	pinnedEvents := getPinnedEventIDs(ctx, btc)

	// Modify pinned events
	if pin {
		// Add to pinned if not already there
		if !slices.Contains(pinnedEvents, targetEventID.String()) {
			pinnedEvents = append(pinnedEvents, targetEventID.String())
		}
	} else {
		// Remove from pinned
		var newPinned []string
		for _, evtID := range pinnedEvents {
			if evtID != targetEventID.String() {
				newPinned = append(newPinned, evtID)
			}
		}
		pinnedEvents = newPinned
	}

	// Convert to id.EventID slice
	pinnedIDs := make([]id.EventID, len(pinnedEvents))
	for i, evtID := range pinnedEvents {
		pinnedIDs[i] = id.EventID(evtID)
	}

	// Update pinned events state
	_, err := bot.SendState(ctx, btc.Portal.MXID, event.StatePinnedEvents, "", &event.Content{
		Parsed: &event.PinnedEventsEventContent{
			Pinned: pinnedIDs,
		},
	}, time.Time{})
	if err != nil {
		action := "pin"
		if !pin {
			action = "unpin"
		}
		return "", fmt.Errorf("couldn't %s the message: %w", action, err)
	}

	action := "pin"
	if !pin {
		action = "unpin"
	}
	return jsonActionResult(action, map[string]any{
		"message_id":   targetEventID,
		"status":       "ok",
		"pinned_count": len(pinnedEvents),
	})
}

// executeMessageListPins handles list-pins action - returns currently pinned messages.
func executeMessageListPins(ctx context.Context, btc *BridgeToolContext) (string, error) {
	pinnedEvents := getPinnedEventIDs(ctx, btc)

	// Build JSON response
	return jsonActionResult("list-pins", map[string]any{
		"pinned": pinnedEvents,
		"count":  len(pinnedEvents),
	})
}

// executeMessageThreadReply handles thread-reply action - sends a message in a thread.
func executeMessageThreadReply(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	// thread_id is the root message of the thread
	threadID, ok := args["thread_id"].(string)
	if !ok || threadID == "" {
		// Fall back to message_id for thread root
		threadID, ok = args["message_id"].(string)
		if !ok || threadID == "" {
			return "", errors.New("action=thread-reply requires 'thread_id' or 'message_id' parameter")
		}
	}
	message, ok := args["message"].(string)
	if !ok || message == "" {
		return "", errors.New("action=thread-reply requires 'message' parameter")
	}

	threadRootID := id.EventID(threadID)
	respID, err := sendFormattedMessage(ctx, btc, message, map[string]any{
		"rel_type": "m.thread",
		"event_id": threadRootID.String(),
	}, "failed to send thread reply")
	if err != nil {
		return "", err
	}

	return jsonActionResult("thread-reply", map[string]any{
		"event_id":  respID,
		"thread_id": threadRootID,
		"status":    "sent",
	})
}

// executeMessageSearch searches messages in the current chat.
func executeMessageSearch(ctx context.Context, args map[string]any, btc *BridgeToolContext) (string, error) {
	if handled, desktopResult, err := maybeExecuteMessageSearchDesktop(ctx, args, btc); handled {
		return desktopResult, err
	}

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", errors.New("action=search requires 'query' parameter")
	}

	// Get limit (default 20)
	limit := 20
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 100 {
			limit = 100 // Cap at 100 results
		}
	}

	// Get messages from database
	// Fetch more than needed since we'll filter
	messages, err := btc.Client.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, btc.Portal.PortalKey, 1000)
	if err != nil {
		return "", fmt.Errorf("couldn't load messages: %w", err)
	}

	// Search through messages
	queryLower := strings.ToLower(query)
	var results []map[string]any

	for _, msg := range messages {
		if len(results) >= limit {
			break
		}

		// Get message body from metadata
		msgMeta, ok := msg.Metadata.(*MessageMetadata)
		if ok && msgMeta != nil {
			body := msgMeta.Body
			if body != "" && strings.Contains(strings.ToLower(body), queryLower) {
				results = append(results, map[string]any{
					"message_id": msg.MXID.String(),
					"role":       msgMeta.Role,
					"content":    truncateString(body, 200),
					"timestamp":  msg.Timestamp.Unix(),
				})
			}
		}
	}

	// Build JSON response
	resultsJSON, _ := json.Marshal(results)
	return fmt.Sprintf(`{"action":"search","query":%q,"results":%s,"count":%d}`, query, string(resultsJSON), len(results)), nil
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// executeWebFetch fetches a web page and extracts readable content.
func executeWebFetch(ctx context.Context, args map[string]any) (string, error) {
	return executeWebFetchWithProviders(ctx, args)
}

// executeImageGeneration generates image(s) using provider-specific image generation APIs.
func executeImageGeneration(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("image generation requires bridge context")
	}

	req, err := parseImageGenArgs(args)
	if err != nil {
		return "", err
	}

	// Allow explicit async override (JSON tool args might encode booleans as native bools).
	asyncExplicit := false
	asyncValue := false
	if raw, ok := args["async"]; ok {
		asyncExplicit = true
		switch v := raw.(type) {
		case bool:
			asyncValue = v
		case float64:
			asyncValue = v != 0
		case int:
			asyncValue = v != 0
		case string:
			asyncValue = strings.EqualFold(strings.TrimSpace(v), "true") || strings.TrimSpace(v) == "1"
		}
	}

	// Default to async for Magic Proxy since image generation can take long and blocks the stream loop.
	loginMeta := loginMetadata(btc.Client.UserLogin)
	async := asyncValue
	if !asyncExplicit && loginMeta.Provider == ProviderMagicProxy {
		async = true
	}

	if async {
		// Preflight: fail fast on unsupported configs (e.g. advanced controls on OpenRouter).
		if _, err := resolveImageGenProvider(req, btc); err != nil {
			return "", fmt.Errorf("image generation failed: %w", err)
		}

		// Copy minimal data for the background worker.
		reqCopy := req
		client := btc.Client
		portal := btc.Portal
		btcCopy := *btc

		go func() {
			client.Log().Debug().Str("prompt", reqCopy.Prompt).Msg("async image generation started")
			bgctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			images, err := generateImagesForRequest(bgctx, &btcCopy, reqCopy)
			if err != nil {
				client.Log().Warn().Err(err).Msg("async image generation failed")
				client.sendSystemNotice(bgctx, portal, "Image generation failed: "+err.Error())
				return
			}

			sent := 0
			for _, imageB64 := range images {
				imageData, mimeType, err := decodeBase64Image(imageB64)
				if err != nil {
					continue
				}
				if _, _, err := client.sendGeneratedImage(bgctx, portal, imageData, mimeType, ""); err != nil {
					continue
				}
				sent++
			}
			if sent == 0 {
				client.sendSystemNotice(bgctx, portal, "Image generation finished, but sending failed.")
			}
			client.Log().Debug().Int("sent", sent).Int("total", len(images)).Msg("async image generation completed")
		}()

		return "Image generation started (async). I'll send the image(s) here when ready.", nil
	}

	images, err := generateImagesForRequest(ctx, btc, req)
	if err != nil {
		return "", fmt.Errorf("image generation failed: %w", err)
	}

	if len(images) == 1 {
		return ImageResultPrefix + images[0], nil
	}

	payload, err := json.Marshal(images)
	if err != nil {
		return "", fmt.Errorf("couldn't encode image results: %w", err)
	}

	return ImagesResultPrefix + string(payload), nil
}

// callOpenRouterImageGen calls OpenRouter's chat completions endpoint for image generation.
// reqBody must contain: model, messages, modalities=["image","text"] (or include "image").
func callOpenRouterImageGen(ctx context.Context, apiKey, baseURL string, reqBody map[string]any) ([]string, error) {
	// Normalize base URL.
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("missing api key")
	}
	if reqBody == nil {
		return nil, errors.New("missing request body")
	}
	if _, ok := reqBody["model"]; !ok {
		return nil, errors.New("missing model in request body")
	}
	if _, ok := reqBody["messages"]; !ok {
		return nil, errors.New("missing messages in request body")
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal the request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("couldn't create the request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://beeper.com")
	req.Header.Set("X-Title", "Beeper AI")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("couldn't read the response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	// Parse response to extract image URL or data
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Images  []struct {
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
					ImageURLAlt struct {
						URL string `json:"url"`
					} `json:"imageUrl"`
				} `json:"images"`
			} `json:"message"`
		} `json:"choices"`
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("couldn't parse the response: %w", err)
	}

	var images []string

	// Check for direct image data (DALL-E style response)
	if len(result.Data) > 0 {
		for _, item := range result.Data {
			if item.B64JSON != "" {
				images = append(images, item.B64JSON)
				continue
			}
			if item.URL != "" {
				imgB64, err := fetchImageAsBase64(ctx, item.URL)
				if err != nil {
					return nil, err
				}
				images = append(images, imgB64)
			}
		}
	}

	// Check for chat completion response with images array
	if len(images) == 0 && len(result.Choices) > 0 && len(result.Choices[0].Message.Images) > 0 {
		for _, img := range result.Choices[0].Message.Images {
			imageURL := img.ImageURL.URL
			if imageURL == "" {
				imageURL = img.ImageURLAlt.URL
			}
			if imageURL == "" {
				continue
			}
			if strings.HasPrefix(imageURL, "data:") {
				imgB64, err := extractBase64FromDataURL(imageURL)
				if err != nil {
					return nil, err
				}
				images = append(images, imgB64)
				continue
			}
			if strings.HasPrefix(imageURL, "http") {
				imgB64, err := fetchImageAsBase64(ctx, imageURL)
				if err != nil {
					return nil, err
				}
				images = append(images, imgB64)
				continue
			}
			return nil, fmt.Errorf("unexpected image URL format: %s", imageURL)
		}
	}

	// Check for chat completion response with image URL
	if len(images) == 0 && len(result.Choices) > 0 && result.Choices[0].Message.Content != "" {
		content := result.Choices[0].Message.Content
		// If content looks like a URL, fetch it
		if strings.HasPrefix(content, "http") {
			imgB64, err := fetchImageAsBase64(ctx, content)
			if err != nil {
				return nil, err
			}
			images = append(images, imgB64)
		} else if strings.HasPrefix(content, "data:") {
			imgB64, err := extractBase64FromDataURL(content)
			if err != nil {
				return nil, err
			}
			images = append(images, imgB64)
		} else if _, err := base64.StdEncoding.DecodeString(content); err == nil {
			images = append(images, content)
		} else {
			return nil, fmt.Errorf("unexpected response format: %s", content[:min(100, len(content))])
		}
	}

	if len(images) == 0 {
		return nil, errors.New("no image data in response")
	}

	return images, nil
}

// extractBase64FromDataURL parses a data URL and returns raw base64 data.
func extractBase64FromDataURL(dataURL string) (string, error) {
	b64Data, _, err := media.ParseDataURI(dataURL)
	if err == nil {
		return b64Data, nil
	}
	data, _, err := media.DecodeBase64(dataURL)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// fetchImageAsBase64 fetches an image URL and returns it as base64.
func fetchImageAsBase64(ctx context.Context, imageURL string) (string, error) {
	b64Data, _, err := fetchImageAsBase64WithType(ctx, imageURL)
	if err != nil {
		return "", err
	}
	return b64Data, nil
}

func fetchImageAsBase64WithType(ctx context.Context, imageURL string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("couldn't build image request for %s: %w", imageURL, err)
	}

	resp, err := imageFetchHTTPClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("couldn't fetch image %s: %w", imageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("couldn't fetch image %s (status %d)", imageURL, resp.StatusCode)
	}

	mimeType := normalizeMimeString(resp.Header.Get("Content-Type"))

	// Limit to 10MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return "", "", fmt.Errorf("couldn't read image %s: %w", imageURL, err)
	}

	return base64.StdEncoding.EncodeToString(data), mimeType, nil
}

// executeTTS converts text to speech.
// Supports: macOS 'say' command, Beeper provider, OpenAI provider.
func executeTTS(ctx context.Context, args map[string]any) (string, error) {
	text, ok := args["text"].(string)
	if !ok || text == "" {
		return "", errors.New("missing or invalid 'text' argument")
	}

	// Limit text length
	const maxTextLen = 4096
	if len(text) > maxTextLen {
		return "", fmt.Errorf("text too long: %d characters (max %d)", len(text), maxTextLen)
	}

	// Get voice/model (defaults are provider-specific).
	voice := ""
	voiceFromArgs := false
	if v, ok := args["voice"].(string); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			voice = trimmed
			voiceFromArgs = true
		}
	}
	model := ""
	modelFromArgs := false
	if v, ok := args["model"].(string); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			model = trimmed
			modelFromArgs = true
		}
	}

	btc := GetBridgeToolContext(ctx)

	// Allow explicit async override (JSON tool args might encode booleans as native bools).
	asyncExplicit := false
	asyncValue := false
	if raw, ok := args["async"]; ok {
		asyncExplicit = true
		switch v := raw.(type) {
		case bool:
			asyncValue = v
		case float64:
			asyncValue = v != 0
		case int:
			asyncValue = v != 0
		case string:
			asyncValue = strings.EqualFold(strings.TrimSpace(v), "true") || strings.TrimSpace(v) == "1"
		}
	}

	// Default to async for Magic Proxy to avoid blocking the stream loop.
	async := asyncValue
	if btc != nil && !asyncExplicit {
		loginMeta := loginMetadata(btc.Client.UserLogin)
		if loginMeta.Provider == ProviderMagicProxy {
			async = true
		}
	}

	if async {
		if btc == nil || btc.Client == nil || btc.Portal == nil {
			return "", errors.New("TTS async requires bridge context")
		}

		// Preflight: if we're not on macOS and the OpenAI TTS endpoint isn't supported, fail fast.
		supportsOpenAITTS := false
		if provider, ok := btc.Client.provider.(*OpenAIProvider); ok {
			_, supportsOpenAITTS = resolveOpenAITTSBaseURL(btc, provider.baseURL)
		}
		if !supportsOpenAITTS && !isTTSMacOSAvailable() {
			return "", errors.New("TTS not available: requires Beeper/OpenAI provider or macOS")
		}

		// Copy minimal data for the background worker.
		textCopy := text
		voiceCopy := voice
		modelCopy := model
		voiceFromArgsCopy := voiceFromArgs
		modelFromArgsCopy := modelFromArgs
		client := btc.Client
		portal := btc.Portal
		btcCopy := *btc

		go func() {
			client.Log().Debug().Str("voice", voiceCopy).Str("model", modelCopy).Msg("async TTS generation started")
			bgctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			audioB64, err := generateTTSBase64(bgctx, &btcCopy, textCopy, voiceCopy, modelCopy, voiceFromArgsCopy, modelFromArgsCopy)
			if err != nil {
				client.Log().Warn().Err(err).Msg("async TTS generation failed")
				client.sendSystemNotice(bgctx, portal, "TTS failed: "+err.Error())
				return
			}

			audioData, err := base64.StdEncoding.DecodeString(audioB64)
			if err != nil {
				client.Log().Warn().Err(err).Msg("async TTS decode failed")
				client.sendSystemNotice(bgctx, portal, "TTS failed: couldn't decode audio data")
				return
			}

			mimeType := detectAudioMime(audioData, "audio/mpeg")
			if _, _, err := client.sendGeneratedAudio(bgctx, portal, audioData, mimeType, ""); err != nil {
				client.sendSystemNotice(bgctx, portal, "TTS finished, but sending failed: "+err.Error())
				return
			}
			client.Log().Debug().Msg("async TTS generation completed")
		}()

		return "TTS started (async). I'll send the audio here when ready.", nil
	}

	audioB64, err := generateTTSBase64(ctx, btc, text, voice, model, voiceFromArgs, modelFromArgs)
	if err != nil {
		return "", err
	}
	return TTSResultPrefix + audioB64, nil
}

func generateTTSBase64(
	ctx context.Context,
	btc *BridgeToolContext,
	text, voice, model string,
	voiceFromArgs, modelFromArgs bool,
) (string, error) {
	// Try provider-based TTS first (Beeper/OpenAI).
	if btc != nil && btc.Client != nil {
		if provider, ok := btc.Client.provider.(*OpenAIProvider); ok {
			ttsBaseURL, supportsOpenAITTS := resolveOpenAITTSBaseURL(btc, provider.baseURL)
			if supportsOpenAITTS {
				// Pick voice/model for OpenAI TTS.
				openAIVoice := strings.ToLower(strings.TrimSpace(voice))
				if openAIVoice == "" {
					openAIVoice = "nova"
				}
				if !validVoices[openAIVoice] {
					openAIVoice = "nova"
				}

				ttsModel := strings.ToLower(strings.TrimSpace(model))
				if ttsModel == "" {
					ttsModel = "tts-1-hd"
				}
				if !isAllowedValue(ttsModel, validTTSModels) {
					ttsModel = "tts-1-hd"
				}

				// Call OpenAI TTS API (fallback from tts-1-hd -> tts-1 when using defaults).
				audioData, err := callOpenAITTS(ctx, btc.Client.apiKey, ttsBaseURL, text, ttsModel, openAIVoice)
				if err != nil && !modelFromArgs && ttsModel == "tts-1-hd" {
					audioData, err = callOpenAITTS(ctx, btc.Client.apiKey, ttsBaseURL, text, "tts-1", openAIVoice)
				}
				if err == nil {
					return audioData, nil
				}
				// Fall through to macOS say if API fails.
			}
		}
	}

	// Try macOS 'say' command as fallback.
	if isTTSMacOSAvailable() {
		macOSVoice := voice
		if !voiceFromArgs {
			macOSVoice = ""
		} else if validVoices[strings.ToLower(strings.TrimSpace(macOSVoice))] {
			// The user provided an OpenAI voice name; use a macOS voice instead.
			macOSVoice = ""
		}
		if macOSVoice == "" {
			macOSVoice = "Samantha" // Default macOS voice
		}
		audioData, err := callMacOSSay(ctx, text, macOSVoice)
		if err != nil {
			return "", fmt.Errorf("macOS TTS failed: %w", err)
		}
		return audioData, nil
	}

	return "", errors.New("TTS not available: requires Beeper/OpenAI provider or macOS")
}

func resolveOpenAITTSBaseURL(btc *BridgeToolContext, providerBaseURL string) (string, bool) {
	baseURL := strings.TrimRight(strings.TrimSpace(providerBaseURL), "/")
	lowerBaseURL := strings.ToLower(baseURL)

	isOpenAIProvider := lowerBaseURL == "" || strings.Contains(lowerBaseURL, "openai.com")
	isBeeperProvider := strings.Contains(lowerBaseURL, "beeper")

	if btc == nil || btc.Client == nil {
		return baseURL, isOpenAIProvider || isBeeperProvider
	}

	client := btc.Client
	if client.UserLogin == nil || client.UserLogin.Metadata == nil {
		return baseURL, isOpenAIProvider || isBeeperProvider
	}

	meta, ok := client.UserLogin.Metadata.(*UserLoginMetadata)
	if !ok || meta == nil {
		return baseURL, isOpenAIProvider || isBeeperProvider
	}

	switch meta.Provider {
	case ProviderOpenAI:
		if client.connector != nil {
			resolved := strings.TrimRight(strings.TrimSpace(client.connector.resolveOpenAIBaseURL()), "/")
			if resolved != "" {
				return resolved, true
			}
		}
		return baseURL, true
	case ProviderBeeper, ProviderMagicProxy:
		if client.connector != nil {
			services := client.connector.resolveServiceConfig(meta)
			if svc, ok := services[serviceOpenAI]; ok {
				resolved := strings.TrimRight(strings.TrimSpace(svc.BaseURL), "/")
				if resolved != "" {
					return resolved, true
				}
			}
		}

		if meta.Provider == ProviderMagicProxy {
			if root := normalizeMagicProxyBaseURL(meta.BaseURL); root != "" {
				return joinProxyPath(root, "/openai/v1"), true
			}
		}

		if meta.Provider == ProviderBeeper && client.connector != nil {
			base := strings.TrimRight(strings.TrimSpace(client.connector.resolveBeeperBaseURL(meta)), "/")
			if base != "" {
				return base + "/openai/v1", true
			}
		}

		return baseURL, true
	default:
		return baseURL, isOpenAIProvider || isBeeperProvider
	}
}

// isTTSMacOSAvailable checks if macOS 'say' command is available.
func isTTSMacOSAvailable() bool {
	return runtime.GOOS == "darwin"
}

// callMacOSSay uses macOS 'say' command to generate speech.
func callMacOSSay(ctx context.Context, text, voice string) (string, error) {
	audioData, err := runMacOSSay(ctx, text, voice, ".m4a", []string{"--file-format=m4af", "--data-format=aac"})
	if err != nil {
		audioData, err = runMacOSSay(ctx, text, voice, ".aiff", nil)
		if err != nil {
			return "", fmt.Errorf("say command failed: %w", err)
		}
	}

	return base64.StdEncoding.EncodeToString(audioData), nil
}

func runMacOSSay(ctx context.Context, text, voice, suffix string, formatArgs []string) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "tts-*"+suffix)
	if err != nil {
		return nil, fmt.Errorf("couldn't create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	var args []string
	if voice != "" {
		args = append(args, "-v", voice)
	}
	args = append(args, "-o", tmpPath)
	if len(formatArgs) > 0 {
		args = append(args, formatArgs...)
	}
	args = append(args, text)

	cmd := exec.CommandContext(ctx, "say", args...)
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	audioData, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("couldn't read audio file: %w", err)
	}
	return audioData, nil
}

// callOpenAITTS calls OpenAI's /v1/audio/speech endpoint
func callOpenAITTS(ctx context.Context, apiKey, baseURL, text, model, voice string) (string, error) {
	// Determine endpoint URL
	endpoint := "https://api.openai.com/v1/audio/speech"
	if baseURL != "" {
		endpoint = strings.TrimSuffix(baseURL, "/") + "/audio/speech"
	}

	// Build request body
	reqBody := map[string]any{
		"model":           model,
		"input":           text,
		"voice":           voice,
		"response_format": "mp3",
	}
	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("couldn't marshal the request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", fmt.Errorf("couldn't create the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("TTS API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Read audio data
	audioBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("couldn't read audio response: %w", err)
	}

	// Return base64 encoded audio
	return base64.StdEncoding.EncodeToString(audioBytes), nil
}

// executeCalculator evaluates a simple arithmetic expression
func executeCalculator(ctx context.Context, args map[string]any) (string, error) {
	expr, ok := args["expression"].(string)
	if !ok {
		return "", errors.New("missing or invalid 'expression' argument")
	}

	result, err := calc.EvalExpression(expr)
	if err != nil {
		return "", fmt.Errorf("calculation error: %w", err)
	}

	return fmt.Sprintf("%.6g", result), nil
}

// executeWebSearch performs a web search (placeholder implementation)
func executeWebSearch(ctx context.Context, args map[string]any) (string, error) {
	return executeWebSearchWithProviders(ctx, args)
}

// executeSessionStatus returns current session status including time, model, and usage info.
// Similar to OpenClaw's session_status tool.
func executeSessionStatus(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("session_status tool requires bridge context")
	}

	meta := portalMeta(btc.Portal)
	if meta == nil {
		return "", errors.New("failed to get portal metadata")
	}

	// Get current time info
	timezone, loc := btc.Client.resolveUserTimezone()
	now := time.Now().In(loc)
	timeStr := now.Format("2006-01-02 15:04:05")
	dayOfWeek := now.Weekday().String()

	// Get model info
	model := meta.Model
	if model == "" {
		model = btc.Client.effectiveModel(meta)
	}

	// Parse provider from model string (format: "provider/model" or just "model")
	provider := "unknown"
	modelName := model
	if parts := strings.SplitN(model, "/", 2); len(parts) == 2 {
		provider = parts[0]
		modelName = parts[1]
	}

	// Get context/token info from metadata
	maxContext := meta.MaxContextMessages
	if maxContext == 0 {
		maxContext = 12 // default
	}
	maxTokens := meta.MaxCompletionTokens
	if maxTokens == 0 {
		maxTokens = 512 // default
	}

	// Build session info
	sessionID := string(btc.Portal.PortalKey.ID)
	title := meta.Title
	if title == "" {
		title = meta.Slug
	}
	if title == "" {
		title = "Untitled"
	}

	// Handle model change if requested (OpenClaw-style "model" alias supported)
	var modelChanged string
	newModel := ""
	if raw, ok := args["set_model"].(string); ok && strings.TrimSpace(raw) != "" {
		newModel = strings.TrimSpace(raw)
	} else if raw, ok := args["model"].(string); ok && strings.TrimSpace(raw) != "" {
		newModel = strings.TrimSpace(raw)
	}

	if newModel != "" {
		if strings.EqualFold(newModel, "default") || strings.EqualFold(newModel, "reset") {
			// Clear override and recompute capabilities from effective model
			meta.Model = ""
			effective := btc.Client.effectiveModel(meta)
			meta.Capabilities = getModelCapabilities(effective, btc.Client.findModelInfo(effective))
			if err := btc.Portal.Save(ctx); err != nil {
				return "", fmt.Errorf("couldn't save model reset: %w", err)
			}
			btc.Portal.UpdateBridgeInfo(ctx)
			btc.Client.ensureGhostDisplayName(ctx, effective)
			modelChanged = fmt.Sprintf("\n\nModel reset to %s.", effective)
			model = effective
			if parts := strings.SplitN(effective, "/", 2); len(parts) == 2 {
				provider = parts[0]
				modelName = parts[1]
			} else {
				modelName = effective
			}
		} else {
			// Update the model in metadata
			meta.Model = newModel
			meta.Capabilities = getModelCapabilities(newModel, btc.Client.findModelInfo(newModel))
			// Save portal metadata
			if err := btc.Portal.Save(ctx); err != nil {
				return "", fmt.Errorf("couldn't save model change: %w", err)
			}
			btc.Portal.UpdateBridgeInfo(ctx)
			btc.Client.ensureGhostDisplayName(ctx, newModel)
			modelChanged = fmt.Sprintf("\n\nModel set to %s.", newModel)
			model = newModel
			if parts := strings.SplitN(newModel, "/", 2); len(parts) == 2 {
				provider = parts[0]
				modelName = parts[1]
			} else {
				modelName = newModel
			}
		}
	}

	// Get agent info if available
	agentInfo := ""
	if meta.AgentID != "" {
		agentInfo = fmt.Sprintf("\nAgent: %s", meta.AgentID)
	}

	// Build status card similar to OpenClaw
	status := fmt.Sprintf(`Session Status
==============
Time: %s %s (%s)
Day: %s

Model: %s
Provider: %s
Max Context: %d messages
Max Tokens: %d

Session: %s
Chat: %s%s%s`,
		timeStr, timezone, now.Format("MST"),
		dayOfWeek,
		modelName,
		provider,
		maxContext,
		maxTokens,
		sessionID,
		title,
		agentInfo,
		modelChanged,
	)

	return status, nil
}

const textFSMaxBytes = 256 * 1024

const (
	// Protect DB-backed virtual FS tools from hanging indefinitely (e.g. DB locks / slow IO).
	textFSToolTimeout = 10 * time.Second

	// Post-write side effects should never block tool completion.
	textFSPostWriteTimeout = 5 * time.Second
)

func textFSStore(ctx context.Context) (*textfs.Store, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return nil, errors.New("file tool requires bridge context")
	}
	meta := portalMeta(btc.Portal)
	agentID := resolveAgentID(meta)
	if agentID == "" {
		agentID = "default"
	}
	db := btc.Client.UserLogin.Bridge.DB.Database
	bridgeID := string(btc.Client.UserLogin.Bridge.DB.BridgeID)
	loginID := string(btc.Client.UserLogin.ID)
	return textfs.NewStore(db, bridgeID, loginID, agentID), nil
}

func detachedBridgeToolContext(ctx context.Context) context.Context {
	base := context.Background()
	if btc := GetBridgeToolContext(ctx); btc != nil {
		base = WithBridgeToolContext(base, btc)
	}
	return base
}

func readStringArg(args map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if raw, ok := args[key]; ok {
			if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
				return s, true
			}
		}
	}
	return "", false
}

func readIntArg(args map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		if raw, ok := args[key]; ok {
			switch v := raw.(type) {
			case float64:
				return int(v), true
			case int:
				return v, true
			case int64:
				return int(v), true
			case string:
				if strings.TrimSpace(v) == "" {
					continue
				}
				if parsed, err := strconv.Atoi(v); err == nil {
					return parsed, true
				}
			}
		}
	}
	return 0, false
}

// executeReadFile handles the read tool.
func executeReadFile(ctx context.Context, args map[string]any) (string, error) {
	store, err := textFSStore(ctx)
	if err != nil {
		return "", err
	}
	path, ok := readStringArg(args, "path", "file_path")
	if !ok {
		return "", errors.New("missing or invalid 'path' argument")
	}
	offset, _ := readIntArg(args, "offset")
	limit, _ := readIntArg(args, "limit")

	readCtx, cancel := context.WithTimeout(ctx, textFSToolTimeout)
	defer cancel()
	entry, found, err := store.Read(readCtx, path)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("file not found: %s", path)
	}

	content := strings.ReplaceAll(entry.Content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	totalLines := len(lines)
	startLine := 1
	if offset > 0 {
		startLine = offset
	}
	if startLine > totalLines {
		return "", fmt.Errorf("offset %d is beyond end of file (%d lines total)", startLine, totalLines)
	}
	startIdx := startLine - 1
	endIdx := totalLines
	if limit > 0 {
		endIdx = startIdx + limit
		if endIdx > totalLines {
			endIdx = totalLines
		}
	}
	selected := strings.Join(lines[startIdx:endIdx], "\n")
	trunc := textfs.TruncateHead(selected, textfs.DefaultMaxLines, textfs.DefaultMaxBytes)
	if trunc.FirstLineExceedsLimit {
		return fmt.Sprintf("[Line %d exceeds %s limit. Use offset/limit to read smaller sections.]", startLine, textfs.FormatSize(textfs.DefaultMaxBytes)), nil
	}

	output := trunc.Content
	var notices []string
	if endIdx < totalLines {
		notices = append(notices, fmt.Sprintf("Showing lines %d-%d of %d. Use offset=%d to continue", startLine, endIdx, totalLines, endIdx+1))
	}
	if trunc.TruncatedBy == "bytes" {
		notices = append(notices, fmt.Sprintf("%s limit reached", textfs.FormatSize(textfs.DefaultMaxBytes)))
	}
	if len(notices) > 0 {
		output += "\n\n[" + strings.Join(notices, ". ") + "]"
	}
	return output, nil
}

// executeWriteFile handles the write tool.
func executeWriteFile(ctx context.Context, args map[string]any) (string, error) {
	store, err := textFSStore(ctx)
	if err != nil {
		return "", err
	}
	path, ok := readStringArg(args, "path", "file_path")
	if !ok {
		return "", errors.New("missing or invalid 'path' argument")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", errors.New("missing or invalid 'content' argument")
	}
	if len(content) > textFSMaxBytes {
		return "", fmt.Errorf("content exceeds %s limit", textfs.FormatSize(textFSMaxBytes))
	}
	writeCtx, cancel := context.WithTimeout(ctx, textFSToolTimeout)
	defer cancel()
	entry, err := store.Write(writeCtx, path, content)
	if err != nil {
		return "", err
	}
	if entry != nil {
		// Detach post-write work so slow Matrix/DB operations can't stall tool completion.
		go func(path string) {
			bg, cancel := context.WithTimeout(detachedBridgeToolContext(ctx), textFSPostWriteTimeout)
			defer cancel()
			notifyMemoryFileChanged(bg, path)
			maybeRefreshAgentIdentity(bg, path)
		}(entry.Path)
	}
	return fmt.Sprintf("Wrote %d bytes to %s.", len([]byte(content)), path), nil
}

// executeEditFile handles the edit tool.
func executeEditFile(ctx context.Context, args map[string]any) (string, error) {
	store, err := textFSStore(ctx)
	if err != nil {
		return "", err
	}
	path, ok := readStringArg(args, "path", "file_path")
	if !ok {
		return "", errors.New("missing or invalid 'path' argument")
	}
	oldText, ok := readStringArg(args, "oldText", "old_string")
	if !ok {
		return "", errors.New("missing or invalid 'oldText' argument")
	}
	newText, ok := readStringArg(args, "newText", "new_string")
	if !ok {
		return "", errors.New("missing or invalid 'newText' argument")
	}

	readCtx, cancel := context.WithTimeout(ctx, textFSToolTimeout)
	defer cancel()
	entry, found, err := store.Read(readCtx, path)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("file not found: %s", path)
	}

	original := entry.Content
	normalized := strings.ReplaceAll(original, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	oldNormalized := strings.ReplaceAll(oldText, "\r\n", "\n")
	oldNormalized = strings.ReplaceAll(oldNormalized, "\r", "\n")
	newNormalized := strings.ReplaceAll(newText, "\r\n", "\n")
	newNormalized = strings.ReplaceAll(newNormalized, "\r", "\n")

	if oldNormalized == "" {
		return "", errors.New("oldText must not be empty")
	}
	count := strings.Count(normalized, oldNormalized)
	if count == 0 {
		return "", fmt.Errorf("couldn't find the exact text in %s", path)
	}
	if count > 1 {
		return "", fmt.Errorf("found %d occurrences in %s; make the match unique", count, path)
	}

	updated := strings.Replace(normalized, oldNormalized, newNormalized, 1)
	if strings.Contains(original, "\r\n") {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}
	if len(updated) > textFSMaxBytes {
		return "", fmt.Errorf("content exceeds %s limit", textfs.FormatSize(textFSMaxBytes))
	}
	writeCtx, cancel := context.WithTimeout(ctx, textFSToolTimeout)
	defer cancel()
	entry, err = store.Write(writeCtx, path, updated)
	if err != nil {
		return "", err
	}
	if entry != nil {
		go func(path string) {
			bg, cancel := context.WithTimeout(detachedBridgeToolContext(ctx), textFSPostWriteTimeout)
			defer cancel()
			notifyMemoryFileChanged(bg, path)
			maybeRefreshAgentIdentity(bg, path)
		}(entry.Path)
	}
	return fmt.Sprintf("Replaced text in %s.", path), nil
}

// GetBuiltinTool returns a builtin tool by name, or nil if not found
func GetBuiltinTool(name string) *ToolDefinition {
	for _, tool := range BuiltinTools() {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// GetEnabledBuiltinTools returns the list of enabled builtin tools based on config
func GetEnabledBuiltinTools(isToolEnabled func(string) bool) []ToolDefinition {
	var enabled []ToolDefinition
	for _, tool := range BuiltinTools() {
		if isToolEnabled(tool.Name) {
			enabled = append(enabled, tool)
		}
	}
	return enabled
}

// executeMemorySearch handles the memory_search tool
func executeMemorySearch(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("memory_search requires bridge context")
	}

	mode := ""
	if raw, ok := args["mode"].(string); ok {
		mode = strings.ToLower(strings.TrimSpace(raw))
	}
	query := ""
	if raw, ok := args["query"].(string); ok {
		query = strings.TrimSpace(raw)
	}
	if mode != "list" && query == "" {
		return "", errors.New("query required")
	}
	var maxResults *int
	var minScore *float64

	if raw := args["maxResults"]; raw != nil {
		if max, ok := readNumberArg(raw); ok {
			val := int(max)
			maxResults = &val
		}
	}
	if raw := args["minScore"]; raw != nil {
		if score, ok := readNumberArg(raw); ok {
			minScore = &score
		}
	}

	meta := portalMeta(btc.Portal)
	agentID := resolveAgentID(meta)
	manager, errMsg := getMemorySearchManager(btc.Client, agentID)
	if manager == nil {
		payload := memorySearchOutput{
			Results:  []memory.SearchResult{},
			Disabled: true,
			Error:    errMsg,
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}

	opts := memory.SearchOptions{
		SessionKey: btc.Portal.PortalKey.String(),
		MinScore:   math.NaN(),
		Mode:       mode,
	}
	if maxResults != nil {
		opts.MaxResults = *maxResults
	}
	if minScore != nil {
		opts.MinScore = *minScore
	}
	if raw, ok := args["pathPrefix"].(string); ok {
		opts.PathPrefix = strings.TrimSpace(raw)
	}
	if raw := args["sources"]; raw != nil {
		if list, ok := raw.([]any); ok {
			out := make([]string, 0, len(list))
			for _, item := range list {
				if s, ok := item.(string); ok {
					if trimmed := strings.TrimSpace(s); trimmed != "" {
						out = append(out, trimmed)
					}
				}
			}
			if len(out) > 0 {
				opts.Sources = out
			}
		} else if list, ok := raw.([]string); ok {
			out := make([]string, 0, len(list))
			for _, s := range list {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
			if len(out) > 0 {
				opts.Sources = out
			}
		}
	}
	results, err := manager.Search(ctx, query, opts)
	if err != nil {
		payload := memorySearchOutput{
			Results:  []memory.SearchResult{},
			Disabled: true,
			Error:    err.Error(),
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}

	status := manager.Status()
	citationsMode := resolveMemoryCitationsMode(btc.Client)
	includeCitations := shouldIncludeMemoryCitations(ctx, btc.Client, btc.Portal, citationsMode)
	decorated := decorateMemorySearchResults(results, includeCitations)
	payload := memorySearchOutput{
		Results:   decorated,
		Provider:  status.Provider,
		Model:     status.Model,
		Fallback:  status.Fallback,
		Citations: citationsMode,
	}
	output, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("couldn't format results: %w", err)
	}

	return string(output), nil
}

// executeMemoryGet handles the memory_get tool
func executeMemoryGet(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("memory_get requires bridge context")
	}

	pathRaw, ok := args["path"].(string)
	path := strings.TrimSpace(pathRaw)
	if !ok || path == "" {
		return "", errors.New("path required")
	}

	meta := portalMeta(btc.Portal)
	agentID := resolveAgentID(meta)
	manager, errMsg := getMemorySearchManager(btc.Client, agentID)
	if manager == nil {
		payload := memoryGetOutput{
			Path:     path,
			Text:     "",
			Disabled: true,
			Error:    errMsg,
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}

	var from *int
	var lines *int
	if raw := args["from"]; raw != nil {
		if value, ok := readNumberArg(raw); ok {
			val := int(value)
			from = &val
		}
	}
	if raw := args["lines"]; raw != nil {
		if value, ok := readNumberArg(raw); ok {
			val := int(value)
			lines = &val
		}
	}

	result, err := manager.ReadFile(ctx, path, from, lines)
	if err != nil {
		payload := memoryGetOutput{
			Path:     path,
			Text:     "",
			Disabled: true,
			Error:    err.Error(),
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}
	text, _ := result["text"].(string)
	resolvedPath, _ := result["path"].(string)
	if resolvedPath == "" {
		resolvedPath = path
	}
	payload := memoryGetOutput{
		Path: resolvedPath,
		Text: text,
	}
	output, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("couldn't format the result: %w", err)
	}

	return string(output), nil
}

func resolveMemoryCitationsMode(client *AIClient) string {
	if client == nil || client.connector == nil || client.connector.Config.Memory == nil {
		return "auto"
	}
	mode := strings.ToLower(strings.TrimSpace(client.connector.Config.Memory.Citations))
	switch mode {
	case "on", "off", "auto":
		return mode
	default:
		return "auto"
	}
}

func shouldIncludeMemoryCitations(ctx context.Context, client *AIClient, portal *bridgev2.Portal, mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on":
		return true
	case "off":
		return false
	default:
	}
	if client == nil || portal == nil {
		return true
	}
	return !client.isGroupChat(ctx, portal)
}

func decorateMemorySearchResults(results []memory.SearchResult, include bool) []memory.SearchResult {
	if !include || len(results) == 0 {
		return results
	}
	out := make([]memory.SearchResult, 0, len(results))
	for _, entry := range results {
		next := entry
		citation := formatMemoryCitation(entry)
		if citation != "" {
			snippet := strings.TrimSpace(entry.Snippet)
			if snippet != "" {
				next.Snippet = fmt.Sprintf("%s\n\nSource: %s", snippet, citation)
			} else {
				next.Snippet = fmt.Sprintf("Source: %s", citation)
			}
		}
		out = append(out, next)
	}
	return out
}

func formatMemoryCitation(entry memory.SearchResult) string {
	if strings.TrimSpace(entry.Path) == "" {
		return ""
	}
	if entry.StartLine > 0 && entry.EndLine > 0 {
		if entry.StartLine == entry.EndLine {
			return fmt.Sprintf("%s#L%d", entry.Path, entry.StartLine)
		}
		return fmt.Sprintf("%s#L%d-L%d", entry.Path, entry.StartLine, entry.EndLine)
	}
	return entry.Path
}

func readNumberArg(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func executeGravatarFetch(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil || btc.Client == nil || btc.Meta == nil {
		return "", errors.New("bridge context not available")
	}

	email := ""
	if raw, ok := args["email"].(string); ok {
		email = strings.TrimSpace(raw)
	}
	if email == "" {
		loginMeta := loginMetadata(btc.Client.UserLogin)
		if loginMeta != nil && loginMeta.Gravatar != nil && loginMeta.Gravatar.Primary != nil {
			email = loginMeta.Gravatar.Primary.Email
		}
	}
	if email == "" {
		return "", errors.New("email is required")
	}

	profile, err := fetchGravatarProfile(ctx, email)
	if err != nil {
		return "", err
	}
	return formatGravatarMarkdown(profile, "fetched"), nil
}

func executeGravatarSet(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil || btc.Client == nil || btc.Meta == nil {
		return "", errors.New("bridge context not available")
	}

	email, ok := args["email"].(string)
	if !ok || strings.TrimSpace(email) == "" {
		return "", errors.New("email is required")
	}

	profile, err := fetchGravatarProfile(ctx, email)
	if err != nil {
		return "", err
	}

	loginMeta := loginMetadata(btc.Client.UserLogin)
	state := ensureGravatarState(loginMeta)
	state.Primary = profile
	if err := btc.Client.UserLogin.Save(ctx); err != nil {
		return "", fmt.Errorf("couldn't save the Gravatar profile: %w", err)
	}

	return formatGravatarMarkdown(profile, "primary set"), nil
}
