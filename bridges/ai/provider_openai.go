package ai

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/rs/zerolog"
	"go.mau.fi/util/random"

	"github.com/beeper/agentremote/pkg/shared/httputil"
)

// OpenAIProvider wraps the OpenAI client and provider-specific request helpers.
type OpenAIProvider struct {
	client  openai.Client
	log     zerolog.Logger
	baseURL string
}

// pdfEngineContextKey is the context key for per-request PDF engine override
type pdfEngineContextKey struct{}

// GetPDFEngineFromContext retrieves the PDF engine override from context
func GetPDFEngineFromContext(ctx context.Context) string {
	return contextValue[string](ctx, pdfEngineContextKey{})
}

// WithPDFEngine adds a PDF engine override to the context
func WithPDFEngine(ctx context.Context, engine string) context.Context {
	return context.WithValue(ctx, pdfEngineContextKey{}, engine)
}

// NewOpenAIProviderWithBaseURL creates an OpenAI provider with custom base URL
// Used for OpenRouter, Beeper proxy, or custom endpoints
// NewOpenAIProviderWithUserID creates an OpenAI provider that passes user_id with each request.
// Used for Beeper proxy to ensure correct rate limiting and feature flags per user.
func NewOpenAIProviderWithUserID(apiKey, baseURL, userID string, log zerolog.Logger) (*OpenAIProvider, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}

	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	opts = appendUserIDOption(opts, userID)
	opts = append(opts, option.WithMiddleware(makeRequestTraceMiddleware(log)))

	client := openai.NewClient(opts...)

	return &OpenAIProvider{
		client:  client,
		log:     log.With().Str("provider", "openai").Logger(),
		baseURL: baseURL,
	}, nil
}

func newOutboundRequestID() string {
	return "abr_" + random.String(12)
}

func appendUserIDOption(opts []option.RequestOption, userID string) []option.RequestOption {
	if userID == "" {
		return opts
	}
	return append(opts, option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		q := req.URL.Query()
		q.Set("user_id", userID)
		req.URL.RawQuery = q.Encode()
		return next(req)
	}))
}

func makeRequestTraceMiddleware(log zerolog.Logger) option.Middleware {
	traceLog := log.With().Str("component", "openai_http").Logger()
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		start := time.Now()
		requestID := strings.TrimSpace(req.Header.Get("x-request-id"))
		if requestID == "" {
			requestID = newOutboundRequestID()
			req.Header.Set("x-request-id", requestID)
		}

		reqMethod := req.Method
		reqHost := ""
		reqPath := ""
		if req.URL != nil {
			reqHost = req.URL.Host
			reqPath = req.URL.Path
		}

		traceLog.Debug().
			Str("request_id", requestID).
			Str("request_method", reqMethod).
			Str("request_host", reqHost).
			Str("request_path", reqPath).
			Msg("Dispatching provider HTTP request")

		resp, err := next(req)
		elapsedMs := time.Since(start).Milliseconds()
		if err != nil {
			traceLog.Error().
				Err(err).
				Str("request_id", requestID).
				Str("request_method", reqMethod).
				Str("request_host", reqHost).
				Str("request_path", reqPath).
				Int64("duration_ms", elapsedMs).
				Msg("Provider HTTP request failed")
			return nil, err
		}

		upstreamRequestID := strings.TrimSpace(resp.Header.Get("x-request-id"))
		if upstreamRequestID == "" {
			upstreamRequestID = strings.TrimSpace(resp.Header.Get("x-openai-request-id"))
		}

		event := traceLog.Debug().
			Str("request_id", requestID).
			Str("request_method", reqMethod).
			Str("request_host", reqHost).
			Str("request_path", reqPath).
			Int("status_code", resp.StatusCode).
			Int64("duration_ms", elapsedMs)

		if upstreamRequestID != "" {
			event = event.Str("upstream_request_id", upstreamRequestID)
		}
		if cfRay := strings.TrimSpace(resp.Header.Get("cf-ray")); cfRay != "" {
			event = event.Str("cf_ray", cfRay)
		}
		if server := strings.TrimSpace(resp.Header.Get("server")); server != "" {
			event = event.Str("response_server", server)
		}

		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			event.Msg("Provider HTTP response error")
		} else {
			event.Msg("Provider HTTP response")
		}
		return resp, nil
	}
}

// NewOpenAIProviderWithPDFPlugin creates an OpenAI provider with PDF plugin middleware.
// Used for OpenRouter/Beeper to enable universal PDF support via file-parser plugin.
func NewOpenAIProviderWithPDFPlugin(apiKey, baseURL, userID, pdfEngine string, headers map[string]string, log zerolog.Logger) (*OpenAIProvider, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}

	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	opts = appendUserIDOption(opts, userID)

	opts = httputil.AppendHeaderOptions(opts, headers)

	// Add PDF plugin middleware
	opts = append(opts, option.WithMiddleware(MakePDFPluginMiddleware(pdfEngine)))
	// Deduplicate tools in the final request payload (OpenRouter/Anthropic requires unique names)
	opts = append(opts, option.WithMiddleware(MakeToolDedupMiddleware(log)))
	opts = append(opts, option.WithMiddleware(makeRequestTraceMiddleware(log)))

	client := openai.NewClient(opts...)

	return &OpenAIProvider{
		client:  client,
		log:     log.With().Str("provider", "openai").Str("pdf_engine", pdfEngine).Logger(),
		baseURL: baseURL,
	}, nil
}

// Client returns the underlying OpenAI client for direct access
// Used by the bridge for advanced features like Responses API
func (o *OpenAIProvider) Client() openai.Client {
	return o.client
}

// ListModels returns available OpenAI models
func (o *OpenAIProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	// Try to list models from API
	page, err := o.client.Models.List(ctx)
	if err != nil {
		// Fallback to known models
		return defaultOpenAIModels(), nil
	}

	var models []ModelInfo
	for page != nil {
		for _, model := range page.Data {
			// Filter to only relevant models
			if !strings.HasPrefix(model.ID, "gpt-") &&
				!strings.HasPrefix(model.ID, "o1") &&
				!strings.HasPrefix(model.ID, "o3") &&
				!strings.HasPrefix(model.ID, "chatgpt") {
				continue
			}

			fullModelID := AddModelPrefix(BackendOpenAI, model.ID)
			models = append(models, ModelInfo{
				ID:                  fullModelID,
				Name:                strings.TrimSpace(fullModelID),
				Provider:            "openai",
				SupportsVision:      strings.Contains(model.ID, "vision") || strings.Contains(model.ID, "4o") || strings.Contains(model.ID, "4-turbo"),
				SupportsToolCalling: true,
				SupportsReasoning:   strings.HasPrefix(model.ID, "o1") || strings.HasPrefix(model.ID, "o3"),
			})
		}

		// Get next page
		page, err = page.GetNextPage()
		if err != nil {
			break
		}
	}

	if len(models) == 0 {
		return defaultOpenAIModels(), nil
	}

	return models, nil
}

// defaultOpenAIModels returns an empty list; model metadata comes from the provider or manifest.
func defaultOpenAIModels() []ModelInfo {
	return nil
}
