package providers

import (
	"errors"
	"os"
	"strings"

	"github.com/beeper/ai-bridge/pkg/ai"
)

const GoogleVertexAPIVersion = "v1"

var (
	ErrMissingVertexProject  = errors.New("vertex ai project is required")
	ErrMissingVertexLocation = errors.New("vertex ai location is required")
)

type GoogleVertexOptions struct {
	GoogleOptions
	Project  string
	Location string
}

func ResolveGoogleVertexProject(options *GoogleVertexOptions) (string, error) {
	if options != nil && strings.TrimSpace(options.Project) != "" {
		return strings.TrimSpace(options.Project), nil
	}
	if env := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT")); env != "" {
		return env, nil
	}
	if env := strings.TrimSpace(os.Getenv("GCLOUD_PROJECT")); env != "" {
		return env, nil
	}
	return "", ErrMissingVertexProject
}

func ResolveGoogleVertexLocation(options *GoogleVertexOptions) (string, error) {
	if options != nil && strings.TrimSpace(options.Location) != "" {
		return strings.TrimSpace(options.Location), nil
	}
	if env := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_LOCATION")); env != "" {
		return env, nil
	}
	return "", ErrMissingVertexLocation
}

func BuildGoogleVertexGenerateContentParams(model ai.Model, context ai.Context, options GoogleVertexOptions) map[string]any {
	return BuildGoogleGenerateContentParams(model, context, options.GoogleOptions)
}
