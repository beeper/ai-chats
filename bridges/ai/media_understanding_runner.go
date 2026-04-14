package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

type mediaUnderstandingResult struct {
	Outputs    []MediaUnderstandingOutput
	Decisions  []MediaUnderstandingDecision
	Body       string
	Transcript string
	FileBlocks []string
}

func mediaCapabilityForMessage(msgType event.MessageType) (MediaUnderstandingCapability, bool) {
	switch msgType {
	case event.MsgImage:
		return MediaCapabilityImage, true
	case event.MsgAudio:
		return MediaCapabilityAudio, true
	case event.MsgVideo:
		return MediaCapabilityVideo, true
	default:
		return "", false
	}
}

func (oc *AIClient) applyMediaUnderstandingForAttachments(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	capability MediaUnderstandingCapability,
	attachments []mediaAttachment,
	rawCaption string,
	hasUserCaption bool,
) (*mediaUnderstandingResult, error) {
	result := &mediaUnderstandingResult{}
	toolsCfg := oc.connector.Config.Tools.Media
	capCfg := toolsCfg.ConfigForCapability(capability)

	if capCfg != nil && capCfg.Enabled != nil && !*capCfg.Enabled {
		result.Decisions = []MediaUnderstandingDecision{{
			Capability: capability,
			Outcome:    MediaOutcomeDisabled,
		}}
		return result, nil
	}

	selected := selectMediaAttachments(attachments, capCfg.Attachments)
	if len(selected) == 0 {
		result.Decisions = []MediaUnderstandingDecision{{
			Capability: capability,
			Outcome:    MediaOutcomeNoAttachment,
		}}
		return result, nil
	}

	if capCfg != nil && capCfg.Scope != nil {
		if oc.mediaUnderstandingScopeDecision(ctx, portal, capCfg.Scope) == scopeDeny {
			attachmentDecisions := make([]MediaUnderstandingAttachmentDecision, 0, len(selected))
			for _, attachment := range selected {
				attachmentDecisions = append(attachmentDecisions, MediaUnderstandingAttachmentDecision{
					AttachmentIndex: attachment.Index,
					Attempts:        nil,
				})
			}
			result.Decisions = []MediaUnderstandingDecision{{
				Capability:  capability,
				Outcome:     MediaOutcomeScopeDeny,
				Attachments: attachmentDecisions,
			}}
			return result, nil
		}
	}

	// Skip image understanding when the primary model supports vision.
	if capability == MediaCapabilityImage {
		responder := oc.responderForMeta(ctx, meta)
		if responder != nil && responder.SupportsVision {
			attachmentDecisions := make([]MediaUnderstandingAttachmentDecision, 0, len(selected))
			modelID := responder.ModelID
			provider := normalizeMediaProviderID(oc.responderProvider(responder))
			for _, attachment := range selected {
				attempt := MediaUnderstandingModelDecision{
					Type:     MediaEntryTypeProvider,
					Provider: provider,
					Model:    modelID,
					Outcome:  MediaOutcomeSkipped,
					Reason:   "primary model supports vision",
				}
				attachmentDecisions = append(attachmentDecisions, MediaUnderstandingAttachmentDecision{
					AttachmentIndex: attachment.Index,
					Attempts:        []MediaUnderstandingModelDecision{attempt},
					Chosen:          &attempt,
				})
			}
			result.Decisions = []MediaUnderstandingDecision{{
				Capability:  capability,
				Outcome:     MediaOutcomeSkipped,
				Attachments: attachmentDecisions,
			}}
			return result, nil
		}
	}

	entries := resolveMediaEntries(toolsCfg, capCfg, capability)
	if len(entries) == 0 {
		if auto := oc.resolveAutoMediaEntries(capability, capCfg, meta); len(auto) > 0 {
			entries = append(entries, auto...)
		}
	}
	if len(entries) == 0 {
		attachmentDecisions := make([]MediaUnderstandingAttachmentDecision, 0, len(selected))
		for _, attachment := range selected {
			attachmentDecisions = append(attachmentDecisions, MediaUnderstandingAttachmentDecision{
				AttachmentIndex: attachment.Index,
				Attempts:        nil,
			})
		}
		result.Decisions = []MediaUnderstandingDecision{{
			Capability:  capability,
			Outcome:     MediaOutcomeSkipped,
			Attachments: attachmentDecisions,
		}}
		return result, nil
	}

	var outputs []MediaUnderstandingOutput
	var lastErr error
	attachmentDecisions := make([]MediaUnderstandingAttachmentDecision, 0, len(selected))
	for _, attachment := range selected {
		output, attempts, err := oc.runMediaUnderstandingEntries(ctx, capability, attachment, entries, capCfg)
		if err != nil {
			lastErr = err
		}
		decision := MediaUnderstandingAttachmentDecision{
			AttachmentIndex: attachment.Index,
			Attempts:        attempts,
		}
		for i := range attempts {
			if attempts[i].Outcome == MediaOutcomeSuccess {
				decision.Chosen = &attempts[i]
				break
			}
		}
		if output != nil {
			outputs = append(outputs, *output)
		}
		attachmentDecisions = append(attachmentDecisions, decision)
	}

	result.Outputs = outputs
	decisionOutcome := MediaOutcomeSkipped
	if len(outputs) > 0 {
		decisionOutcome = MediaOutcomeSuccess
	}
	result.Decisions = []MediaUnderstandingDecision{{
		Capability:  capability,
		Outcome:     decisionOutcome,
		Attachments: attachmentDecisions,
	}}
	oc.loggerForContext(ctx).Debug().
		Str("capability", string(capability)).
		Str("outcome", string(decisionOutcome)).
		Int("attachments", len(selected)).
		Int("outputs", len(outputs)).
		Msg("Media understanding decision")

	bodyBase := ""
	if hasUserCaption {
		bodyBase = rawCaption
	}
	combined := formatMediaUnderstandingBody(bodyBase, outputs)
	if len(outputs) > 0 {
		audioOutputs := filterMediaOutputs(outputs, MediaKindAudioTranscription)
		if len(audioOutputs) > 0 {
			result.Transcript = formatAudioTranscripts(audioOutputs)
		}
	}

	fileBlocks := oc.extractMediaFileBlocks(ctx, selected, outputs)
	if len(fileBlocks) > 0 {
		result.FileBlocks = fileBlocks
		if combined == "" {
			combined = strings.Join(fileBlocks, "\n\n")
		} else {
			combined = strings.TrimSpace(combined + "\n\n" + strings.Join(fileBlocks, "\n\n"))
		}
	}
	result.Body = combined
	if len(outputs) == 0 && lastErr != nil {
		return result, lastErr
	}
	return result, nil
}

func (oc *AIClient) resolveAutoAudioEntry(cfg *MediaUnderstandingConfig) *MediaUnderstandingModelConfig {
	var headers map[string]string
	if cfg != nil {
		headers = cfg.Headers
	}

	candidates := []struct {
		provider string
		model    string
	}{
		{"openai", defaultAudioModelsByProvider["openai"]},
		{"groq", defaultAudioModelsByProvider["groq"]},
		{"deepgram", defaultAudioModelsByProvider["deepgram"]},
		{"google", defaultGoogleAudioModel},
	}
	for _, c := range candidates {
		if oc.resolveMediaProviderAPIKey(c.provider, "", "") != "" || hasProviderAuthHeader(c.provider, headers) {
			return &MediaUnderstandingModelConfig{
				Provider: c.provider,
				Model:    c.model,
			}
		}
	}
	return nil
}

func (oc *AIClient) resolveAutoMediaEntries(
	capability MediaUnderstandingCapability,
	cfg *MediaUnderstandingConfig,
	meta *PortalMetadata,
) []MediaUnderstandingModelConfig {
	if active := oc.resolveActiveMediaEntry(capability, cfg, meta); active != nil {
		return []MediaUnderstandingModelConfig{*active}
	}

	if capability == MediaCapabilityAudio {
		if local := resolveLocalAudioEntry(); local != nil {
			return []MediaUnderstandingModelConfig{*local}
		}
	}

	if gemini := resolveGeminiCliEntry(); gemini != nil {
		return []MediaUnderstandingModelConfig{*gemini}
	}

	if keyEntry := oc.resolveKeyMediaEntry(capability, cfg); keyEntry != nil {
		return []MediaUnderstandingModelConfig{*keyEntry}
	}

	return nil
}

func (oc *AIClient) resolveActiveMediaEntry(
	capability MediaUnderstandingCapability,
	cfg *MediaUnderstandingConfig,
	meta *PortalMetadata,
) *MediaUnderstandingModelConfig {
	if oc == nil || meta == nil {
		return nil
	}
	responder := oc.responderForMeta(context.Background(), meta)
	if responder == nil || strings.TrimSpace(responder.ModelID) == "" {
		return nil
	}
	providerID, model := splitModelProvider(responder.ModelID)
	if providerID == "" {
		providerID = normalizeMediaProviderID(oc.responderProvider(responder))
	}
	if providerID == "" {
		return nil
	}
	if !providerSupportsCapability(providerID, capability) {
		return nil
	}
	if !oc.hasMediaProviderAuth(providerID, cfg) {
		return nil
	}
	return &MediaUnderstandingModelConfig{
		Provider: providerID,
		Model:    model,
	}
}

func (oc *AIClient) resolveKeyMediaEntry(
	capability MediaUnderstandingCapability,
	cfg *MediaUnderstandingConfig,
) *MediaUnderstandingModelConfig {
	switch capability {
	case MediaCapabilityImage:
		if oc.hasMediaProviderAuth("openrouter", cfg) {
			return &MediaUnderstandingModelConfig{
				Provider: "openrouter",
				Model:    defaultOpenRouterGoogleModel,
			}
		}
		if oc.hasMediaProviderAuth("openai", cfg) {
			return &MediaUnderstandingModelConfig{
				Provider: "openai",
				Model:    defaultImageModelsByProvider["openai"],
			}
		}
	case MediaCapabilityVideo:
		if oc.hasMediaProviderAuth("openrouter", cfg) {
			return &MediaUnderstandingModelConfig{
				Provider: "openrouter",
				Model:    defaultOpenRouterGoogleModel,
			}
		}
		if oc.hasMediaProviderAuth("google", cfg) {
			return &MediaUnderstandingModelConfig{
				Provider: "google",
				Model:    defaultGoogleVideoModel,
			}
		}
	case MediaCapabilityAudio:
		return oc.resolveAutoAudioEntry(cfg)
	}
	return nil
}

func (oc *AIClient) hasMediaProviderAuth(providerID string, cfg *MediaUnderstandingConfig) bool {
	var headers map[string]string
	if cfg != nil {
		headers = cfg.Headers
	}
	if hasProviderAuthHeader(providerID, headers) {
		return true
	}
	key := oc.resolveMediaProviderAPIKey(providerID, "", "")
	return strings.TrimSpace(key) != ""
}

func providerSupportsCapability(providerID string, capability MediaUnderstandingCapability) bool {
	spec, ok := mediaProviderSpecFor(providerID)
	if !ok {
		return false
	}
	return slices.Contains(spec.capabilities, capability)
}

var hasBinaryCache sync.Map

func hasBinary(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if v, ok := hasBinaryCache.Load(name); ok {
		return v.(bool)
	}
	_, err := exec.LookPath(name)
	found := err == nil
	hasBinaryCache.Store(name, found)
	return found
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func resolveLocalWhisperCPPEntry() *MediaUnderstandingModelConfig {
	if !hasBinary("whisper-cli") {
		return nil
	}
	envModel := strings.TrimSpace(os.Getenv("WHISPER_CPP_MODEL"))
	defaultModel := "/opt/homebrew/share/whisper-cpp/for-tests-ggml-tiny.bin"
	modelPath := defaultModel
	if envModel != "" && fileExists(envModel) {
		modelPath = envModel
	}
	if !fileExists(modelPath) {
		return nil
	}
	return &MediaUnderstandingModelConfig{
		Type:    "cli",
		Command: "whisper-cli",
		Args:    []string{"-m", modelPath, "-otxt", "-of", "{{OutputBase}}", "-np", "-nt", "{{MediaPath}}"},
	}
}

func resolveLocalWhisperEntry() *MediaUnderstandingModelConfig {
	if !hasBinary("whisper") {
		return nil
	}
	return &MediaUnderstandingModelConfig{
		Type:    "cli",
		Command: "whisper",
		Args: []string{
			"--model",
			"turbo",
			"--output_format",
			"txt",
			"--output_dir",
			"{{OutputDir}}",
			"--verbose",
			"False",
			"{{MediaPath}}",
		},
	}
}

func resolveSherpaOnnxEntry() *MediaUnderstandingModelConfig {
	if !hasBinary("sherpa-onnx-offline") {
		return nil
	}
	modelDir := strings.TrimSpace(os.Getenv("SHERPA_ONNX_MODEL_DIR"))
	if modelDir == "" {
		return nil
	}
	tokens := filepath.Join(modelDir, "tokens.txt")
	encoder := filepath.Join(modelDir, "encoder.onnx")
	decoder := filepath.Join(modelDir, "decoder.onnx")
	joiner := filepath.Join(modelDir, "joiner.onnx")
	if !fileExists(tokens) || !fileExists(encoder) || !fileExists(decoder) || !fileExists(joiner) {
		return nil
	}
	return &MediaUnderstandingModelConfig{
		Type:    "cli",
		Command: "sherpa-onnx-offline",
		Args: []string{
			"--tokens=" + tokens,
			"--encoder=" + encoder,
			"--decoder=" + decoder,
			"--joiner=" + joiner,
			"{{MediaPath}}",
		},
	}
}

func resolveLocalAudioEntry() *MediaUnderstandingModelConfig {
	if entry := resolveSherpaOnnxEntry(); entry != nil {
		return entry
	}
	if entry := resolveLocalWhisperCPPEntry(); entry != nil {
		return entry
	}
	return resolveLocalWhisperEntry()
}

func resolveGeminiCliEntry() *MediaUnderstandingModelConfig {
	if !hasBinary("gemini") {
		return nil
	}
	return &MediaUnderstandingModelConfig{
		Type:    "cli",
		Command: "gemini",
		Args: []string{
			"--output-format",
			"json",
			"--allowed-tools",
			"read_many_files",
			"--include-directories",
			"{{MediaDir}}",
			"{{Prompt}}",
			"Use read_many_files to read {{MediaPath}} and respond with only the text output.",
		},
	}
}

func (oc *AIClient) runMediaUnderstandingEntries(
	ctx context.Context,
	capability MediaUnderstandingCapability,
	attachment mediaAttachment,
	entries []MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
) (*MediaUnderstandingOutput, []MediaUnderstandingModelDecision, error) {
	attempts := make([]MediaUnderstandingModelDecision, 0, len(entries))
	var lastErr error
	for _, entry := range entries {
		entryType := entry.ResolvedType()
		provider := strings.TrimSpace(entry.Provider)
		model := strings.TrimSpace(entry.Model)
		if entryType == MediaEntryTypeCLI {
			provider = strings.TrimSpace(entry.Command)
			if provider == "" {
				provider = string(MediaEntryTypeCLI)
			}
			if model == "" {
				model = provider
			}
		} else {
			provider = normalizeMediaProviderID(provider)
		}
		output, err := oc.runMediaUnderstandingEntry(ctx, capability, attachment, entry, capCfg)
		if err != nil {
			lastErr = err
			attempts = append(attempts, MediaUnderstandingModelDecision{
				Type:     entryType,
				Provider: provider,
				Model:    model,
				Outcome:  MediaOutcomeFailed,
				Reason:   err.Error(),
			})
			continue
		}
		if output == nil || strings.TrimSpace(output.Text) == "" {
			attempts = append(attempts, MediaUnderstandingModelDecision{
				Type:     entryType,
				Provider: provider,
				Model:    model,
				Outcome:  MediaOutcomeSkipped,
				Reason:   "empty output",
			})
			continue
		}
		attempts = append(attempts, MediaUnderstandingModelDecision{
			Type:     entryType,
			Provider: provider,
			Model:    model,
			Outcome:  MediaOutcomeSuccess,
		})
		return output, attempts, nil
	}
	return nil, attempts, lastErr
}

func filterMediaOutputs(outputs []MediaUnderstandingOutput, kind MediaUnderstandingKind) []MediaUnderstandingOutput {
	filtered := make([]MediaUnderstandingOutput, 0, len(outputs))
	for _, output := range outputs {
		if output.Kind == kind {
			filtered = append(filtered, output)
		}
	}
	return filtered
}

func (oc *AIClient) extractMediaFileBlocks(
	ctx context.Context,
	attachments []mediaAttachment,
	outputs []MediaUnderstandingOutput,
) []string {
	if len(attachments) == 0 {
		return nil
	}
	skip := map[int]bool{}
	for _, output := range outputs {
		if output.Kind == MediaKindAudioTranscription {
			skip[output.AttachmentIndex] = true
		}
	}
	var blocks []string
	for _, attachment := range attachments {
		if skip[attachment.Index] {
			continue
		}
		mimeType := stringutil.NormalizeMimeType(attachment.MimeType)
		if mimeType == "" || !isTextFileMime(mimeType) {
			continue
		}
		content, truncated, err := oc.downloadTextFile(ctx, attachment.URL, attachment.EncryptedFile, mimeType)
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).
				Int("attachment_index", attachment.Index).
				Msg("Failed to extract text file block for media understanding")
			continue
		}
		block := buildTextFileMessage("", false, attachment.FileName, mimeType, content, truncated)
		if strings.TrimSpace(block) == "" {
			continue
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func (oc *AIClient) runMediaUnderstandingEntry(
	ctx context.Context,
	capability MediaUnderstandingCapability,
	attachment mediaAttachment,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
) (*MediaUnderstandingOutput, error) {
	entryType := entry.ResolvedType()

	maxChars := resolveMediaMaxChars(capability, entry, capCfg)
	maxBytes := resolveMediaMaxBytes(capability, entry, capCfg)
	prompt := resolveMediaPrompt(capability, entry.Prompt, capCfg, maxChars)
	timeout := resolveMediaTimeoutSeconds(entry.TimeoutSeconds, capCfg, defaultTimeoutSecondsByCapability[capability])

	switch entryType {
	case MediaEntryTypeCLI:
		data, actualMime, err := oc.downloadMediaBytes(ctx, attachment.URL, attachment.EncryptedFile, maxBytes, attachment.MimeType)
		if err != nil {
			return nil, err
		}
		fileName := resolveMediaFileName(attachment.FileName, string(capability), attachment.URL)
		tempDir, err := os.MkdirTemp("", "aichats-media-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tempDir)
		mediaPath := filepath.Join(tempDir, fileName)
		if err := os.WriteFile(mediaPath, data, 0600); err != nil {
			return nil, err
		}
		if actualMime != "" {
			attachment.MimeType = actualMime
		}
		output, err := runMediaCLI(ctx, entry.Command, entry.Args, prompt, maxChars, mediaPath)
		if err != nil {
			return nil, err
		}
		return buildMediaOutput(capability, output, string(MediaEntryTypeCLI), entry.Model, attachment.Index), nil

	default:
		providerID := normalizeMediaProviderID(entry.Provider)
		if providerID == "" && capability != MediaCapabilityImage {
			return nil, fmt.Errorf("missing provider for %s understanding", capability)
		}

		switch capability {
		case MediaCapabilityImage:
			return oc.describeImageWithEntry(ctx, entry, capCfg, attachment.URL, attachment.MimeType, attachment.EncryptedFile, maxBytes, maxChars, prompt, attachment.Index)
		case MediaCapabilityAudio:
			return oc.transcribeAudioWithEntry(ctx, entry, capCfg, attachment.URL, attachment.MimeType, attachment.EncryptedFile, attachment.FileName, maxBytes, maxChars, prompt, timeout, attachment.Index)
		case MediaCapabilityVideo:
			return oc.describeVideoWithEntry(ctx, entry, capCfg, attachment.URL, attachment.MimeType, attachment.EncryptedFile, maxBytes, maxChars, prompt, timeout, attachment.Index)
		}
	}
	return nil, fmt.Errorf("unsupported media capability %s", capability)
}

func (oc *AIClient) describeImageWithEntry(
	ctx context.Context,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
	mediaURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	maxBytes int,
	maxChars int,
	prompt string,
	attachmentIndex int,
) (*MediaUnderstandingOutput, error) {
	modelID := strings.TrimSpace(entry.Model)
	if modelID == "" {
		return nil, errors.New("image understanding requires model id")
	}
	entryProvider := normalizeMediaProviderID(entry.Provider)
	if entryProvider != "" {
		currentProvider := normalizeMediaProviderID(loginMetadata(oc.UserLogin).Provider)
		if entryProvider != "" && currentProvider != "" && entryProvider != currentProvider && entryProvider != "openrouter" && entryProvider != "google" {
			return nil, fmt.Errorf("image provider %s not available for current login provider", entryProvider)
		}
	}

	if entryProvider == "google" {
		data, actualMime, err := oc.downloadMediaBytes(ctx, mediaURL, encryptedFile, maxBytes, mimeType)
		if err != nil {
			return nil, err
		}
		if actualMime == "" {
			actualMime = mimeType
		}
		timeout := resolveMediaTimeoutSeconds(entry.TimeoutSeconds, capCfg, defaultTimeoutSecondsByCapability[MediaCapabilityImage])
		return oc.callGeminiMediaCapability(ctx, MediaCapabilityImage, entry, capCfg, data, actualMime, prompt, timeout, maxChars, attachmentIndex)
	}

	rawData, actualMime, err := oc.downloadMediaBytes(ctx, mediaURL, encryptedFile, maxBytes, mimeType)
	if err != nil {
		return nil, err
	}
	if actualMime == "" {
		actualMime = mimeType
	}
	if actualMime == "" {
		actualMime = "image/jpeg"
	}
	b64Data := base64.StdEncoding.EncodeToString(rawData)
	dataURL := BuildDataURL(actualMime, b64Data)

	ctxPrompt := UserPromptContext(
		PromptBlock{
			Type: PromptBlockText,
			Text: prompt,
		},
		PromptBlock{
			Type:     PromptBlockImage,
			ImageURL: dataURL,
			MimeType: actualMime,
		},
	)
	modelIDForAPI := oc.modelIDForAPI(ResolveAlias(modelID))
	var resp *GenerateResponse
	if entryProvider == "openrouter" {
		resp, err = oc.generateWithOpenRouter(ctx, modelIDForAPI, ctxPrompt, capCfg, entry)
	} else {
		resp, err = oc.provider.Generate(ctx, GenerateParams{
			Model:               modelIDForAPI,
			Context:             ctxPrompt,
			MaxCompletionTokens: defaultImageUnderstandingLimit,
		})
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(resp.Content)
	text = truncateText(text, maxChars)
	return buildMediaOutput(MediaCapabilityImage, text, entry.Provider, modelID, attachmentIndex), nil
}

func (oc *AIClient) transcribeAudioWithEntry(
	ctx context.Context,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
	mediaURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	fileName string,
	maxBytes int,
	maxChars int,
	prompt string,
	timeout time.Duration,
	attachmentIndex int,
) (*MediaUnderstandingOutput, error) {
	providerID := normalizeMediaProviderID(entry.Provider)
	if providerID == "" {
		return nil, errors.New("missing audio provider")
	}
	data, actualMime, err := oc.downloadMediaBytes(ctx, mediaURL, encryptedFile, maxBytes, mimeType)
	if err != nil {
		return nil, err
	}
	if actualMime == "" {
		actualMime = mimeType
	}
	fileName = resolveMediaFileName(fileName, string(MediaCapabilityAudio), mediaURL)

	headers := mergeMediaHeaders(capCfg, entry)
	apiKey := oc.resolveMediaProviderAPIKey(providerID, entry.Profile, entry.PreferredProfile)
	if apiKey == "" && !hasProviderAuthHeader(providerID, headers) {
		return nil, fmt.Errorf("missing API key for %s audio transcription", providerID)
	}

	request := mediaAudioRequest{
		mediaRequestBase: mediaRequestBase{
			APIKey:   apiKey,
			BaseURL:  resolveMediaBaseURL(capCfg, entry),
			Headers:  headers,
			Model:    strings.TrimSpace(entry.Model),
			Prompt:   prompt,
			MimeType: actualMime,
			Data:     data,
			Timeout:  timeout,
		},
		Provider: providerID,
		Language: resolveMediaLanguage(entry, capCfg),
		FileName: fileName,
	}
	if providerID == "openai" && strings.TrimSpace(request.BaseURL) == "" {
		request.BaseURL = resolveOpenAIMediaBaseURL(oc)
	}

	var text string
	switch providerID {
	case "openai", "groq":
		text, err = transcribeOpenAICompatibleAudio(ctx, request)
	case "deepgram":
		query := resolveProviderQuery("deepgram", capCfg, entry)
		text, err = transcribeDeepgramAudio(ctx, request, query)
	case "google":
		text, err = callGeminiForCapability(ctx, request.mediaRequestBase, MediaCapabilityAudio)
	default:
		err = fmt.Errorf("unsupported audio provider: %s", providerID)
	}
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	text = truncateText(text, maxChars)
	return buildMediaOutput(MediaCapabilityAudio, text, providerID, entry.Model, attachmentIndex), nil
}

func (oc *AIClient) describeVideoWithEntry(
	ctx context.Context,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
	mediaURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	maxBytes int,
	maxChars int,
	prompt string,
	timeout time.Duration,
	attachmentIndex int,
) (*MediaUnderstandingOutput, error) {
	providerID := normalizeMediaProviderID(entry.Provider)
	if providerID == "" {
		return nil, errors.New("missing video provider")
	}

	// Download and check base64 size limit (shared by all video providers).
	data, actualMime, err := oc.downloadMediaBytes(ctx, mediaURL, encryptedFile, maxBytes, mimeType)
	if err != nil {
		return nil, err
	}
	if actualMime == "" {
		actualMime = mimeType
	}
	if actualMime == "" {
		actualMime = "video/mp4"
	}
	base64Size := estimateBase64Size(len(data))
	maxBase64 := resolveVideoMaxBase64Bytes(maxBytes)
	if base64Size > maxBase64 {
		oc.loggerForContext(ctx).Warn().
			Int("base64_bytes", base64Size).
			Int("limit_bytes", maxBase64).
			Str("provider", providerID).
			Msg("Video payload exceeds base64 limit")
		return nil, errors.New("video payload exceeds base64 limit")
	}

	if providerID != "google" {
		return nil, fmt.Errorf("unsupported video provider: %s", providerID)
	}

	return oc.callGeminiMediaCapability(ctx, MediaCapabilityVideo, entry, capCfg, data, actualMime, prompt, timeout, maxChars, attachmentIndex)
}

func (oc *AIClient) callGeminiMediaCapability(
	ctx context.Context,
	capability MediaUnderstandingCapability,
	entry MediaUnderstandingModelConfig,
	capCfg *MediaUnderstandingConfig,
	data []byte,
	actualMime string,
	prompt string,
	timeout time.Duration,
	maxChars int,
	attachmentIndex int,
) (*MediaUnderstandingOutput, error) {
	headers := mergeMediaHeaders(capCfg, entry)
	apiKey := oc.resolveMediaProviderAPIKey("google", entry.Profile, entry.PreferredProfile)
	if apiKey == "" && !hasProviderAuthHeader("google", headers) {
		return nil, fmt.Errorf("missing API key for google %s", capability)
	}
	request := mediaRequestBase{
		APIKey:   apiKey,
		BaseURL:  resolveMediaBaseURL(capCfg, entry),
		Headers:  headers,
		Model:    strings.TrimSpace(entry.Model),
		Prompt:   prompt,
		MimeType: actualMime,
		Data:     data,
		Timeout:  timeout,
	}
	text, err := callGeminiForCapability(ctx, request, capability)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	text = truncateText(text, maxChars)
	return buildMediaOutput(capability, text, "google", entry.Model, attachmentIndex), nil
}

func (oc *AIClient) generateWithOpenRouter(
	ctx context.Context,
	modelID string,
	promptContext PromptContext,
	capCfg *MediaUnderstandingConfig,
	entry MediaUnderstandingModelConfig,
) (*GenerateResponse, error) {
	apiKey, baseURL, headers, pdfEngine, userID, err := oc.resolveOpenRouterMediaConfig(capCfg, entry)
	if err != nil {
		return nil, err
	}
	provider, err := NewOpenAIProviderWithPDFPlugin(apiKey, baseURL, userID, pdfEngine, headers, oc.log)
	if err != nil {
		return nil, err
	}
	params := GenerateParams{
		Model:               modelID,
		Context:             promptContext,
		MaxCompletionTokens: defaultImageUnderstandingLimit,
	}
	return provider.Generate(ctx, params)
}

func (oc *AIClient) resolveOpenRouterMediaConfig(
	capCfg *MediaUnderstandingConfig,
	entry MediaUnderstandingModelConfig,
) (apiKey string, baseURL string, headers map[string]string, pdfEngine string, userID string, err error) {
	if oc == nil || oc.connector == nil {
		err = errors.New("missing connector")
		return
	}
	headers = openRouterHeaders()
	for key, value := range mergeMediaHeaders(capCfg, entry) {
		headers[key] = value
	}
	apiKey = strings.TrimSpace(oc.resolveMediaProviderAPIKey("openrouter", entry.Profile, entry.PreferredProfile))
	if apiKey == "" && !hasProviderAuthHeader("openrouter", headers) {
		err = errors.New("missing API key for openrouter")
		return
	}
	baseURL = strings.TrimSpace(resolveMediaBaseURL(capCfg, entry))
	if baseURL == "" {
		baseURL = resolveOpenRouterMediaBaseURL(oc)
	}
	pdfEngine = oc.defaultPDFEngine()
	if oc.UserLogin != nil && oc.UserLogin.User != nil && oc.UserLogin.User.MXID != "" {
		userID = oc.UserLogin.User.MXID.String()
	}
	return
}

func resolveOpenRouterMediaBaseURL(oc *AIClient) string {
	if oc == nil || oc.connector == nil {
		return defaultOpenRouterBaseURL
	}
	loginCfg := oc.loginConfigSnapshot(context.Background())
	if svc := oc.connector.resolveServiceConfig(loginMetadata(oc.UserLogin).Provider, loginCfg)[serviceOpenRouter]; strings.TrimSpace(svc.BaseURL) != "" {
		return strings.TrimRight(svc.BaseURL, "/")
	}
	base := strings.TrimSpace(oc.connector.resolveOpenRouterBaseURL())
	if base != "" {
		return base
	}
	return defaultOpenRouterBaseURL
}

func resolveOpenAIMediaBaseURL(oc *AIClient) string {
	if oc == nil || oc.connector == nil {
		return defaultOpenAITranscriptionBaseURL
	}
	loginCfg := oc.loginConfigSnapshot(context.Background())
	if svc := oc.connector.resolveServiceConfig(loginMetadata(oc.UserLogin).Provider, loginCfg)[serviceOpenAI]; strings.TrimSpace(svc.BaseURL) != "" {
		return stringutil.NormalizeBaseURL(svc.BaseURL)
	}
	if base := stringutil.NormalizeBaseURL(oc.connector.resolveOpenAIBaseURL()); base != "" {
		return base
	}
	return defaultOpenAITranscriptionBaseURL
}

func resolveMediaBaseURL(cfg *MediaUnderstandingConfig, entry MediaUnderstandingModelConfig) string {
	if strings.TrimSpace(entry.BaseURL) != "" {
		return entry.BaseURL
	}
	if cfg != nil && strings.TrimSpace(cfg.BaseURL) != "" {
		return cfg.BaseURL
	}
	return ""
}

func mergeMediaHeaders(cfg *MediaUnderstandingConfig, entry MediaUnderstandingModelConfig) map[string]string {
	merged := map[string]string{}
	if cfg != nil {
		for key, value := range cfg.Headers {
			merged[key] = value
		}
	}
	for key, value := range entry.Headers {
		merged[key] = value
	}
	return merged
}

func hasProviderAuthHeader(providerID string, headers map[string]string) bool {
	spec, ok := mediaProviderSpecFor(providerID)
	if !ok || spec.authHeader == "" {
		return false
	}
	for key := range headers {
		if strings.EqualFold(key, spec.authHeader) {
			return true
		}
	}
	return false
}

func resolveProfiledEnvKey(base string, profile string) string {
	if base == "" || strings.TrimSpace(profile) == "" {
		return ""
	}
	normalized := strings.TrimSpace(profile)
	normalized = strings.ToUpper(normalized)
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, ".", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	env := base + "_" + normalized
	return strings.TrimSpace(os.Getenv(env))
}

// resolveProfiledKeys tries resolveProfiledEnvKey for each envBase with the given
// profile and preferredProfile, then falls back to the plain env var.
// Returns the first non-empty result.
func resolveProfiledKeys(envBases []string, profile, preferredProfile string) string {
	for _, base := range envBases {
		if key := resolveProfiledEnvKey(base, profile); key != "" {
			return key
		}
		if key := resolveProfiledEnvKey(base, preferredProfile); key != "" {
			return key
		}
	}
	for _, base := range envBases {
		if key := strings.TrimSpace(os.Getenv(base)); key != "" {
			return key
		}
	}
	return ""
}

func (oc *AIClient) resolveMediaProviderAPIKey(providerID string, profile string, preferredProfile string) string {
	spec, ok := mediaProviderSpecFor(providerID)
	if !ok {
		return ""
	}
	if key := resolveProfiledKeys(spec.envKeys, profile, preferredProfile); key != "" {
		return key
	}
	if spec.service != "" {
		if oc == nil || oc.connector == nil || oc.UserLogin == nil || oc.UserLogin.Metadata == nil {
			return ""
		}
		loginCfg := oc.loginConfigSnapshot(context.Background())
		return strings.TrimSpace(oc.connector.resolveServiceConfig(loginMetadata(oc.UserLogin).Provider, loginCfg)[spec.service].APIKey)
	}
	return ""
}

func buildMediaOutput(capability MediaUnderstandingCapability, text string, provider string, model string, attachmentIndex int) *MediaUnderstandingOutput {
	kind := MediaKindImageDescription
	switch capability {
	case MediaCapabilityAudio:
		kind = MediaKindAudioTranscription
	case MediaCapabilityVideo:
		kind = MediaKindVideoDescription
	}
	return &MediaUnderstandingOutput{
		Kind:            kind,
		AttachmentIndex: attachmentIndex,
		Text:            strings.TrimSpace(text),
		Provider:        strings.TrimSpace(provider),
		Model:           strings.TrimSpace(model),
	}
}

func estimateBase64Size(size int) int {
	if size <= 0 {
		return 0
	}
	return ((size + 2) / 3) * 4
}
