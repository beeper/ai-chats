package oauth

type Credentials struct {
	Refresh string         `json:"refresh"`
	Access  string         `json:"access"`
	Expires int64          `json:"expires"`
	Extra   map[string]any `json:"extra,omitempty"`
}

type ProviderID string

type Prompt struct {
	Message     string `json:"message"`
	Placeholder string `json:"placeholder,omitempty"`
	AllowEmpty  bool   `json:"allowEmpty,omitempty"`
}

type AuthInfo struct {
	URL          string `json:"url"`
	Instructions string `json:"instructions,omitempty"`
}

type LoginCallbacks struct {
	OnAuth            func(info AuthInfo)
	OnPrompt          func(prompt Prompt) (string, error)
	OnProgress        func(message string)
	OnManualCodeInput func() (string, error)
}

type Provider interface {
	ID() ProviderID
	Name() string

	Login(callbacks LoginCallbacks) (Credentials, error)
	UsesCallbackServer() bool
	RefreshToken(credentials Credentials) (Credentials, error)
	GetAPIKey(credentials Credentials) string
}
