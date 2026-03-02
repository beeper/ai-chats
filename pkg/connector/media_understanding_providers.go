package connector

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

const (
	defaultOpenAITranscriptionBaseURL = "https://api.openai.com/v1"
	defaultGroqTranscriptionBaseURL   = "https://api.groq.com/openai/v1"
	defaultDeepgramBaseURL            = "https://api.deepgram.com/v1"
	defaultGoogleBaseURL              = "https://generativelanguage.googleapis.com/v1beta"
	defaultGoogleAudioModel           = "gemini-3-flash-preview"
	defaultGoogleImageModel           = "gemini-3-flash-preview"
	defaultGoogleVideoModel           = "gemini-3-flash-preview"
)

var mediaProviderCapabilities = map[string][]MediaUnderstandingCapability{
	"openai":     {MediaCapabilityImage, MediaCapabilityAudio},
	"groq":       {MediaCapabilityAudio},
	"deepgram":   {MediaCapabilityAudio},
	"google":     {MediaCapabilityImage, MediaCapabilityAudio, MediaCapabilityVideo},
	"openrouter": {MediaCapabilityImage, MediaCapabilityVideo},
}

func normalizeMediaProviderID(id string) string {
	normalized := strings.ToLower(strings.TrimSpace(id))
	switch normalized {
	case "gemini":
		return "google"
	case "beeper":
		return "openrouter"
	case "magic_proxy":
		return "openrouter"
	default:
		return normalized
	}
}

func normalizeMediaBaseURL(value string, fallback string) string {
	if trimmed := stringutil.NormalizeBaseURL(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func readErrorResponse(res *http.Response) string {
	if res == nil || res.Body == nil {
		return ""
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

func headerExists(headers http.Header, name string) bool {
	_, ok := headers[http.CanonicalHeaderKey(name)]
	return ok
}

func applyHeaderMap(headers http.Header, values map[string]string) {
	for key, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		headers.Set(key, trimmed)
	}
}

func resolveProviderQuery(providerID string, cfg *MediaUnderstandingConfig, entry MediaUnderstandingModelConfig) map[string]any {
	merged := map[string]any{}
	var cfgOptions map[string]map[string]any
	if cfg != nil {
		cfgOptions = cfg.ProviderOptions
	}
	for _, src := range []map[string]map[string]any{cfgOptions, entry.ProviderOptions} {
		if src == nil {
			continue
		}
		options, ok := src[providerID]
		if !ok {
			continue
		}
		for key, value := range options {
			if value == nil {
				continue
			}
			merged[key] = value
		}
	}
	if providerID != "deepgram" {
		if len(merged) == 0 {
			return nil
		}
		return merged
	}
	normalized := map[string]any{}
	for key, value := range merged {
		switch key {
		case "detectLanguage":
			normalized["detect_language"] = value
		case "smartFormat":
			normalized["smart_format"] = value
		default:
			normalized[key] = value
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func transcribeOpenAICompatibleAudio(ctx context.Context, params mediaAudioRequest) (string, error) {
	baseURL := normalizeMediaBaseURL(params.BaseURL, defaultOpenAITranscriptionBaseURL)
	if params.Provider == "groq" {
		baseURL = normalizeMediaBaseURL(params.BaseURL, defaultGroqTranscriptionBaseURL)
	}
	model := strings.TrimSpace(params.Model)
	if model == "" {
		model = defaultAudioModelsByProvider[params.Provider]
	}
	if model == "" {
		return "", errors.New("missing transcription model")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", params.FileName)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(params.Data); err != nil {
		return "", err
	}
	_ = writer.WriteField("model", model)
	if params.Language != "" {
		_ = writer.WriteField("language", params.Language)
	}
	if params.Prompt != "" {
		_ = writer.WriteField("prompt", params.Prompt)
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/audio/transcriptions", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	applyHeaderMap(req.Header, params.Headers)
	if !headerExists(req.Header, "Authorization") && params.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+params.APIKey)
	}

	client := &http.Client{Timeout: params.Timeout}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		detail := readErrorResponse(res)
		if detail != "" {
			return "", fmt.Errorf("audio transcription failed (HTTP %d): %s", res.StatusCode, detail)
		}
		return "", fmt.Errorf("audio transcription failed (HTTP %d)", res.StatusCode)
	}
	defer res.Body.Close()
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", err
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		return "", errors.New("audio transcription response missing text")
	}
	return text, nil
}

func transcribeDeepgramAudio(ctx context.Context, params mediaAudioRequest, query map[string]any) (string, error) {
	baseURL := normalizeMediaBaseURL(params.BaseURL, defaultDeepgramBaseURL)
	model := strings.TrimSpace(params.Model)
	if model == "" {
		model = defaultAudioModelsByProvider["deepgram"]
	}
	if model == "" {
		return "", errors.New("missing transcription model")
	}

	endpoint, err := url.Parse(baseURL + "/listen")
	if err != nil {
		return "", err
	}
	q := endpoint.Query()
	q.Set("model", model)
	if params.Language != "" {
		q.Set("language", params.Language)
	}
	for key, value := range query {
		if value == nil {
			continue
		}
		q.Set(key, fmt.Sprint(value))
	}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(params.Data))
	if err != nil {
		return "", err
	}
	applyHeaderMap(req.Header, params.Headers)
	if !headerExists(req.Header, "Authorization") && params.APIKey != "" {
		req.Header.Set("Authorization", "Token "+params.APIKey)
	}
	if !headerExists(req.Header, "Content-Type") {
		mimeType := params.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		req.Header.Set("Content-Type", mimeType)
	}

	client := &http.Client{Timeout: params.Timeout}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		detail := readErrorResponse(res)
		if detail != "" {
			return "", fmt.Errorf("audio transcription failed (HTTP %d): %s", res.StatusCode, detail)
		}
		return "", fmt.Errorf("audio transcription failed (HTTP %d)", res.StatusCode)
	}
	defer res.Body.Close()
	var payload struct {
		Results struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string `json:"transcript"`
				} `json:"alternatives"`
			} `json:"channels"`
		} `json:"results"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", err
	}
	if len(payload.Results.Channels) == 0 || len(payload.Results.Channels[0].Alternatives) == 0 {
		return "", errors.New("audio transcription response missing transcript")
	}
	text := strings.TrimSpace(payload.Results.Channels[0].Alternatives[0].Transcript)
	if text == "" {
		return "", errors.New("audio transcription response missing transcript")
	}
	return text, nil
}

func callGeminiGenerateContent(ctx context.Context, baseURL, model, apiKey string, headers map[string]string, prompt, mimeType string, data []byte, timeout time.Duration, errorLabel string) (string, error) {
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", baseURL, model)
	body := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": prompt},
					{
						"inline_data": map[string]any{
							"mime_type": mimeType,
							"data":      base64.StdEncoding.EncodeToString(data),
						},
					},
				},
			},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	applyHeaderMap(req.Header, headers)
	if !headerExists(req.Header, "Content-Type") {
		req.Header.Set("Content-Type", "application/json")
	}
	if !headerExists(req.Header, "X-Goog-Api-Key") && apiKey != "" {
		req.Header.Set("X-Goog-Api-Key", apiKey)
	}

	client := &http.Client{Timeout: timeout}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		detail := readErrorResponse(res)
		if detail != "" {
			return "", fmt.Errorf("%s failed (HTTP %d): %s", errorLabel, res.StatusCode, detail)
		}
		return "", fmt.Errorf("%s failed (HTTP %d)", errorLabel, res.StatusCode)
	}
	defer res.Body.Close()
	var payloadResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payloadResp); err != nil {
		return "", err
	}
	if len(payloadResp.Candidates) == 0 {
		return "", fmt.Errorf("%s response missing text", errorLabel)
	}
	var parts []string
	for _, part := range payloadResp.Candidates[0].Content.Parts {
		if trimmed := strings.TrimSpace(part.Text); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("%s response missing text", errorLabel)
	}
	return strings.Join(parts, "\n"), nil
}

// geminiCapabilityConfig holds per-capability defaults for Gemini calls.
type geminiCapabilityConfig struct {
	defaultModel string
	capability   MediaUnderstandingCapability
	mimeDefault  string
	errorLabel   string
}

var geminiCapabilityConfigs = map[MediaUnderstandingCapability]geminiCapabilityConfig{
	MediaCapabilityAudio: {defaultModel: defaultGoogleAudioModel, capability: MediaCapabilityAudio, mimeDefault: "audio/wav", errorLabel: "audio transcription"},
	MediaCapabilityVideo: {defaultModel: defaultGoogleVideoModel, capability: MediaCapabilityVideo, mimeDefault: "video/mp4", errorLabel: "video description"},
	MediaCapabilityImage: {defaultModel: defaultGoogleImageModel, capability: MediaCapabilityImage, mimeDefault: "image/jpeg", errorLabel: "image description"},
}

func callGeminiForCapability(ctx context.Context, params mediaRequestBase, cap MediaUnderstandingCapability) (string, error) {
	cfg := geminiCapabilityConfigs[cap]
	baseURL := normalizeMediaBaseURL(params.BaseURL, defaultGoogleBaseURL)
	model := strings.TrimSpace(params.Model)
	if model == "" {
		model = cfg.defaultModel
	}
	prompt := strings.TrimSpace(params.Prompt)
	if prompt == "" {
		prompt = defaultPromptByCapability[cap]
	}
	return callGeminiGenerateContent(ctx, baseURL, model, params.APIKey, params.Headers, prompt, params.MimeTypeOrDefault(cfg.mimeDefault), params.Data, params.Timeout, cfg.errorLabel)
}

type mediaRequestBase struct {
	APIKey   string
	BaseURL  string
	Headers  map[string]string
	Model    string
	Prompt   string
	MimeType string
	Data     []byte
	Timeout  time.Duration
}

func (r mediaRequestBase) Base64Data() string {
	return base64.StdEncoding.EncodeToString(r.Data)
}

func (r mediaRequestBase) MimeTypeOrDefault(fallback string) string {
	if trimmed := strings.TrimSpace(r.MimeType); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(fallback)
}

type mediaAudioRequest struct {
	mediaRequestBase
	Provider string
	Language string
	FileName string
}

type mediaVideoRequest = mediaRequestBase
type mediaImageRequest = mediaRequestBase

func resolveMediaFileName(fallback string, msgType string, mediaURL string) string {
	base := strings.TrimSpace(fallback)
	if base != "" {
		return base
	}
	if mediaURL != "" {
		if parsed, err := url.Parse(mediaURL); err == nil {
			if parsed.Path != "" {
				if name := filepath.Base(parsed.Path); name != "." && name != "/" {
					return name
				}
			}
		}
		if strings.HasPrefix(mediaURL, "file://") {
			path := strings.TrimPrefix(mediaURL, "file://")
			if name := filepath.Base(path); name != "." && name != "/" {
				return name
			}
		}
	}
	if msgType != "" {
		return msgType
	}
	return "media"
}
