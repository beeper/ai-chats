package connector

import "strings"

var desktopCanonicalNetworkMap = map[string]string{
	"whatsapp":          "whatsapp",
	"whatsapp_business": "whatsapp",
	"whatsappbusiness":  "whatsapp",
	"wa":                "whatsapp",

	"telegram":      "telegram",
	"telegram_bot":  "telegram",
	"telegrambot":   "telegram",
	"tg":            "telegram",
	"discord":       "discord",
	"slack":         "slack",
	"signal":        "signal",
	"instagram":     "instagram",
	"instagram_dm":  "instagram",
	"instagram_dms": "instagram",
	"ig":            "instagram",

	"imessage":       "imessage",
	"apple_messages": "imessage",
	"bluebubbles":    "imessage",

	"messenger":          "messenger",
	"facebook_messenger": "messenger",
	"fb_messenger":       "messenger",

	"sms":             "sms",
	"mms":             "sms",
	"google_messages": "sms",
	"android_sms":     "sms",

	"line":           "line",
	"google_chat":    "googlechat",
	"googlechat":     "googlechat",
	"nextcloud_talk": "nextcloudtalk",
	"nextcloudtalk":  "nextcloudtalk",
	"mattermost":     "mattermost",
}

func normalizeDesktopNetworkToken(network string) string {
	trimmed := strings.TrimSpace(strings.ToLower(network))
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	wasUnderscore := false
	for _, r := range trimmed {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			wasUnderscore = false
			continue
		}
		if !wasUnderscore {
			b.WriteByte('_')
			wasUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func canonicalDesktopNetwork(network string) string {
	token := normalizeDesktopNetworkToken(network)
	if token == "" {
		return ""
	}
	if canonical, ok := desktopCanonicalNetworkMap[token]; ok {
		return canonical
	}

	switch {
	case strings.HasPrefix(token, "whatsapp"):
		return "whatsapp"
	case strings.HasPrefix(token, "telegram"):
		return "telegram"
	case strings.HasPrefix(token, "discord"):
		return "discord"
	case strings.HasPrefix(token, "signal"):
		return "signal"
	case strings.HasPrefix(token, "imessage"), strings.HasPrefix(token, "apple_messages"), strings.HasPrefix(token, "bluebubbles"):
		return "imessage"
	case strings.HasPrefix(token, "instagram"):
		return "instagram"
	case strings.Contains(token, "messenger"):
		return "messenger"
	case strings.HasPrefix(token, "google_chat"), token == "googlechat":
		return "googlechat"
	case strings.HasPrefix(token, "nextcloud_talk"), token == "nextcloudtalk":
		return "nextcloudtalk"
	case strings.HasPrefix(token, "mattermost"):
		return "mattermost"
	case strings.HasPrefix(token, "line"):
		return "line"
	case strings.HasPrefix(token, "sms"), strings.HasPrefix(token, "mms"), strings.HasPrefix(token, "google_messages"), strings.HasPrefix(token, "android_sms"):
		return "sms"
	default:
		return token
	}
}

func desktopSessionChannelForNetwork(network string) string {
	canonical := canonicalDesktopNetwork(network)
	if canonical == "" {
		return channelDesktopAPI
	}
	return canonical
}

func desktopNetworkFilterMatches(filters map[string]struct{}, network string) bool {
	if len(filters) == 0 {
		return true
	}
	canonicalNetwork := canonicalDesktopNetwork(network)
	rawNetwork := normalizeDesktopNetworkToken(network)

	for filter := range filters {
		canonicalFilter := canonicalDesktopNetwork(filter)
		rawFilter := normalizeDesktopNetworkToken(filter)
		if canonicalFilter != "" && canonicalFilter == canonicalNetwork {
			return true
		}
		if rawFilter != "" && rawFilter == rawNetwork {
			return true
		}
	}
	return false
}
