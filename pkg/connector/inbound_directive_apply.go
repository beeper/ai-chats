package connector

import "fmt"

func applyThinkingLevel(meta *PortalMetadata, level string) {
	_ = meta
	_ = level
}

func applyReasoningLevel(meta *PortalMetadata, level string) {
	_ = meta
	_ = level
}

func formatThinkingAck(level string) string {
	if level == "off" {
		return "Thinking disabled."
	}
	return fmt.Sprintf("Thinking level set to %s.", level)
}

func formatVerboseAck(level string) string {
	switch level {
	case "off":
		return formatSystemAck("Verbose logging disabled.")
	case "full":
		return formatSystemAck("Verbose logging set to full.")
	default:
		return formatSystemAck("Verbose logging enabled.")
	}
}

func formatReasoningAck(level string) string {
	switch level {
	case "off":
		return formatSystemAck("Reasoning visibility disabled.")
	case "stream":
		return formatSystemAck("Reasoning stream enabled (Telegram only).")
	default:
		return formatSystemAck("Reasoning visibility enabled.")
	}
}

func formatElevatedAck(level string) string {
	switch level {
	case "off":
		return formatSystemAck("Elevated mode disabled.")
	case "full":
		return formatSystemAck("Elevated mode set to full (auto-approve).")
	default:
		return formatSystemAck("Elevated mode set to ask (approvals may still apply).")
	}
}
