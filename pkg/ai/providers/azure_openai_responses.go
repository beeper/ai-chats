package providers

import (
	"errors"
	"os"
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
)

const defaultAzureAPIVersion = "v1"

var ErrMissingAzureBaseURL = errors.New("azure openai base url is required")

var azureToolCallProviders = map[string]struct{}{
	"openai":                 {},
	"openai-codex":           {},
	"opencode":               {},
	"azure-openai-responses": {},
}

type AzureOpenAIResponsesOptions struct {
	OpenAIResponsesOptions
	AzureAPIVersion     string
	AzureResourceName   string
	AzureBaseURL        string
	AzureDeploymentName string
}

func ParseDeploymentNameMap(value string) map[string]string {
	out := map[string]string{}
	for _, entry := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		modelID := strings.TrimSpace(parts[0])
		deployment := strings.TrimSpace(parts[1])
		if modelID == "" || deployment == "" {
			continue
		}
		out[modelID] = deployment
	}
	return out
}

func ResolveDeploymentName(model ai.Model, options *AzureOpenAIResponsesOptions) string {
	if options != nil && strings.TrimSpace(options.AzureDeploymentName) != "" {
		return strings.TrimSpace(options.AzureDeploymentName)
	}
	mapped := ParseDeploymentNameMap(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP"))
	if deployment, ok := mapped[model.ID]; ok {
		return deployment
	}
	return model.ID
}

func normalizeAzureBaseURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/")
}

func buildDefaultAzureBaseURL(resourceName string) string {
	return "https://" + resourceName + ".openai.azure.com/openai/v1"
}

func ResolveAzureConfig(model ai.Model, options *AzureOpenAIResponsesOptions) (baseURL string, apiVersion string, err error) {
	apiVersion = defaultAzureAPIVersion
	if envVersion := strings.TrimSpace(os.Getenv("AZURE_OPENAI_API_VERSION")); envVersion != "" {
		apiVersion = envVersion
	}
	if options != nil && strings.TrimSpace(options.AzureAPIVersion) != "" {
		apiVersion = strings.TrimSpace(options.AzureAPIVersion)
	}

	var resolvedBaseURL string
	if options != nil && strings.TrimSpace(options.AzureBaseURL) != "" {
		resolvedBaseURL = strings.TrimSpace(options.AzureBaseURL)
	}
	if resolvedBaseURL == "" {
		resolvedBaseURL = strings.TrimSpace(os.Getenv("AZURE_OPENAI_BASE_URL"))
	}

	resourceName := strings.TrimSpace(os.Getenv("AZURE_OPENAI_RESOURCE_NAME"))
	if options != nil && strings.TrimSpace(options.AzureResourceName) != "" {
		resourceName = strings.TrimSpace(options.AzureResourceName)
	}
	if resolvedBaseURL == "" && resourceName != "" {
		resolvedBaseURL = buildDefaultAzureBaseURL(resourceName)
	}
	if resolvedBaseURL == "" {
		resolvedBaseURL = strings.TrimSpace(model.BaseURL)
	}
	if resolvedBaseURL == "" {
		return "", "", ErrMissingAzureBaseURL
	}
	return normalizeAzureBaseURL(resolvedBaseURL), apiVersion, nil
}

func BuildAzureOpenAIResponsesParams(
	model ai.Model,
	context ai.Context,
	options AzureOpenAIResponsesOptions,
) map[string]any {
	deploymentName := ResolveDeploymentName(model, &options)
	messages := ConvertResponsesMessages(model, context, azureToolCallProviders, nil)

	params := map[string]any{
		"model":            deploymentName,
		"input":            messages,
		"stream":           true,
		"prompt_cache_key": options.StreamOptions.SessionID,
	}
	if options.StreamOptions.MaxTokens > 0 {
		params["max_output_tokens"] = options.StreamOptions.MaxTokens
	}
	if options.StreamOptions.Temperature != nil {
		params["temperature"] = *options.StreamOptions.Temperature
	}
	if len(context.Tools) > 0 {
		params["tools"] = ConvertResponsesTools(context.Tools, false)
	}
	if model.Reasoning {
		if options.ReasoningEffort != "" || strings.TrimSpace(options.ReasoningSummary) != "" {
			effort := options.ReasoningEffort
			if effort == "" {
				effort = ai.ThinkingMedium
			}
			summary := strings.TrimSpace(options.ReasoningSummary)
			if summary == "" {
				summary = "auto"
			}
			params["reasoning"] = map[string]any{
				"effort":  string(effort),
				"summary": summary,
			}
			params["include"] = []string{"reasoning.encrypted_content"}
		} else if strings.HasPrefix(strings.ToLower(model.Name), "gpt-5") {
			params["input"] = append(messages, map[string]any{
				"role": "developer",
				"content": []map[string]any{{
					"type": "input_text",
					"text": "# Juice: 0 !important",
				}},
			})
		}
	}

	return params
}
