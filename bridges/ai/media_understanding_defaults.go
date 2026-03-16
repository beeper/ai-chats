package ai

const (
	mediaMB                        = 1024 * 1024
	defaultMediaMaxChars           = 500
	defaultMediaConcurrency        = 2
	defaultVideoMaxBase64Bytes     = 70 * mediaMB
	defaultImageUnderstandingLimit = 1024
)

var defaultMaxCharsByCapability = map[MediaUnderstandingCapability]int{
	MediaCapabilityImage: defaultMediaMaxChars,
	MediaCapabilityAudio: 0,
	MediaCapabilityVideo: defaultMediaMaxChars,
}

var defaultMaxBytesByCapability = map[MediaUnderstandingCapability]int{
	MediaCapabilityImage: 10 * mediaMB,
	MediaCapabilityAudio: 20 * mediaMB,
	MediaCapabilityVideo: 50 * mediaMB,
}

var defaultTimeoutSecondsByCapability = map[MediaUnderstandingCapability]int{
	MediaCapabilityImage: 60,
	MediaCapabilityAudio: 60,
	MediaCapabilityVideo: 120,
}

var defaultPromptByCapability = map[MediaUnderstandingCapability]string{
	MediaCapabilityImage: "Describe the image.",
	MediaCapabilityAudio: "Transcribe the audio.",
	MediaCapabilityVideo: "Describe the video.",
}

var defaultAudioModelsByProvider = map[string]string{
	"groq":     "whisper-large-v3-turbo",
	"openai":   "gpt-4o-transcribe",
	"deepgram": "nova-3",
}

const defaultOpenRouterGoogleModel = "google/gemini-3-flash-preview"

var defaultImageModelsByProvider = map[string]string{
	"openai":     "gpt-5-mini",
	"openrouter": defaultOpenRouterGoogleModel,
}

func truncateText(s string, maxChars int) string {
	if maxChars > 0 && len(s) > maxChars {
		runes := []rune(s)
		if len(runes) > maxChars {
			return string(runes[:maxChars])
		}
	}
	return s
}

func resolveVideoMaxBase64Bytes(maxBytes int) int {
	if maxBytes <= 0 {
		return defaultVideoMaxBase64Bytes
	}
	expanded := int(float64(maxBytes) * (4.0 / 3.0))
	if expanded > defaultVideoMaxBase64Bytes {
		return defaultVideoMaxBase64Bytes
	}
	return expanded
}
