package exa

func NewProvider[P any](enabled *bool, apiKey string, build func() P) P {
	var zero P
	if !Enabled(enabled, apiKey) {
		return zero
	}
	return build()
}

func ApplyConfigDefaults(baseURL *string, textMaxChars *int, defaultTextMaxChars int) {
	if baseURL != nil && *baseURL == "" {
		*baseURL = DefaultBaseURL
	}
	if textMaxChars != nil && *textMaxChars <= 0 {
		*textMaxChars = defaultTextMaxChars
	}
}
