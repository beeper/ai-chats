package toolspec

// Shared tool schema definitions used by both connector and agents.

const (
	CalculatorName        = "calculator"
	CalculatorDescription = "Perform basic arithmetic calculations. Supports addition, subtraction, multiplication, division, and modulo operations."

	WebSearchName        = "web_search"
	WebSearchDescription = "Search the web using the best available provider (OpenRouter web search when configured). Supports region-specific and localized search via country and language parameters. Returns titles, URLs, and snippets for fast research."

	WebFetchName        = "web_fetch"
	WebFetchDescription = "Fetch and extract readable content from a URL (HTML \u2192 markdown/text). Use for lightweight page access without browser automation."

	MessageName        = "message"
	MessageDescription = "Send messages and channel actions. Supports actions: send, delete, react, poll, pin, threads, focus, and more."

	CronName        = "cron"
	CronDescription = "Manage scheduler-backed jobs that run in hidden background rooms.\n\nACTIONS:\n- status: Check scheduler status\n- list: List jobs (use includeDisabled:true to include disabled)\n- add: Create job (requires job object, see schema below)\n- update: Modify job (requires jobId + patch object)\n- remove: Delete job (requires jobId)\n- run: Trigger job immediately (requires jobId)\n\nJOB SCHEMA (for add action):\n{\n  \"name\": \"string (optional)\",\n  \"schedule\": { ... },\n  \"payload\": { ... },\n  \"delivery\": { ... },\n  \"enabled\": true | false\n}\n\nSCHEDULE TYPES (schedule.kind):\n- \"at\": One-shot at absolute time\n  { \"kind\": \"at\", \"at\": \"<ISO-8601 timestamp>\" }\n- \"every\": Recurring interval\n  { \"kind\": \"every\", \"everyMs\": <interval-ms>, \"anchorMs\": <optional-start-ms> }\n- \"cron\": Cron expression\n  { \"kind\": \"cron\", \"expr\": \"<cron-expression>\", \"tz\": \"<optional-timezone>\" }\n\nPAYLOAD:\n- \"agentTurn\": Run the agent inside a hidden background room\n  { \"kind\": \"agentTurn\", \"message\": \"<prompt>\", \"model\": \"<optional>\", \"thinking\": \"<optional>\", \"timeoutSeconds\": <optional> }\n\nDELIVERY:\n  { \"mode\": \"none|announce\", \"to\": \"<!room-id:server>\", \"bestEffort\": <optional-bool> }\n  - delivery.to: Matrix room ID (e.g. !abcdef:server.com). Omit to use the last active room or default chat.\n\nUse contextMessages (0-10) to add recent chat context to the scheduled payload."

	SessionStatusName        = "session_status"
	SessionStatusDescription = "Show a /status-equivalent session status card (usage + time + cost when available). Use for model-use questions (📊 session_status). Optional: set per-session model override (model=default resets overrides)."

	// ImageName matches OpenClaw's image analysis tool (vision).
	ImageName                  = "image"
	ImageDescription           = "Analyze an image with the configured image model (agents.defaults.imageModel). Provide a prompt and image path or URL."
	ImageDescriptionVisionHint = "Analyze an image with a vision model. Only use this tool when the image was NOT already provided in the user's message. Images mentioned in the prompt are automatically visible to you."

	// ImageGenerateName is an AI image generation tool (not in OpenClaw).
	ImageGenerateName        = "image_generate"
	ImageGenerateDescription = "Generate or edit images from a text prompt. To edit an existing image, pass its media URL (from a [media_url: ...] tag or Media URL in a tool result) in input_images."

	TTSName        = "tts"
	TTSDescription = "Convert text to speech and return a MEDIA: path. Use when the user requests audio or TTS is enabled. Copy the MEDIA line exactly."

	MemorySearchName        = "memory_search"
	MemorySearchDescription = "Mandatory recall step: semantically and keyword search MEMORY.md + memory/*.md + workspace/*.md (and optional session transcripts) before answering questions about prior work, decisions, dates, people, preferences, or todos; returns top snippets with path + lines."
	MemoryGetName           = "memory_get"
	MemoryGetDescription    = "Safe snippet read from MEMORY.md, memory/*.md, workspace/*.md, or configured memorySearch.extraPaths with optional from/lines; use after memory_search to pull only the needed lines and keep context small."

	ReadName         = "read"
	ReadDescription  = "Read file contents. Images sent as attachments. Text: first 2000 lines, lines truncated at 2000 chars. Use offset/limit for large files."
	WriteName        = "write"
	WriteDescription = "Write/overwrite file. Creates parent directories."
	EditName         = "edit"
	EditDescription  = "Replace exact text in file. Must match exactly including whitespace. Fails if text appears multiple times or not found."

	GravatarFetchName        = "gravatar_fetch"
	GravatarFetchDescription = "Fetch a Gravatar profile for an email address. You must provide an email address."
	GravatarSetName          = "gravatar_set"
	GravatarSetDescription   = "Set the primary Gravatar profile for this login."

	BeeperDocsName        = "beeper_docs"
	BeeperDocsDescription = "Search Beeper documentation (help.beeper.com, developers.beeper.com). Use when answering questions about Beeper features, setup, troubleshooting, configuration, or developer APIs."

	BeeperSendFeedbackName        = "beeper_send_feedback"
	BeeperSendFeedbackDescription = "Submit feedback or bug reports to Beeper. Use when the user wants to report a problem, request a feature, or send feedback about their Beeper experience."
)

// CalculatorSchema returns the JSON schema for the calculator tool.
func CalculatorSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"expression": StringProperty("A mathematical expression to evaluate, e.g. '2 + 3 * 4' or '100 / 5'"),
	}, "expression")
}

// WebSearchSchema returns the JSON schema for the web search tool.
func WebSearchSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"query":       StringProperty("Search query string."),
		"count":       BoundedNumberProperty("Number of results to return (1-10).", 1, 10),
		"country":     StringProperty("2-letter country code for region-specific results (e.g., 'DE', 'US', 'ALL'). Default: 'US'."),
		"search_lang": StringProperty("ISO language code for search results (e.g., 'de', 'en', 'fr')."),
		"ui_lang":     StringProperty("ISO language code for UI elements."),
		"freshness":   StringProperty("Filter results by discovery time (Brave only). Values: 'pd' (past 24h), 'pw' (past week), 'pm' (past month), 'py' (past year), or date range 'YYYY-MM-DDtoYYYY-MM-DD'."),
	}, "query")
}

// WebFetchSchema returns the JSON schema for the web fetch tool.
func WebFetchSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"url": StringProperty("HTTP or HTTPS URL to fetch."),
		"maxChars": map[string]any{
			"type":        "number",
			"description": "Maximum characters to return (truncates when exceeded).",
			"minimum":     100,
		},
		"extractMode": StringEnumProperty(`Extraction mode ("markdown" or "text").`, []string{"markdown", "text"}),
	}, "url")
}

// ReadSchema returns the JSON schema for the read tool.
func ReadSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"path":   StringProperty("Path to the file to read (relative or absolute)"),
		"offset": NumberProperty("Line number to start reading from (1-indexed)"),
		"limit":  NumberProperty("Maximum number of lines to read"),
	}, "path")
}

// WriteSchema returns the JSON schema for the write tool.
func WriteSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"path":    StringProperty("Path to the file to write (relative or absolute)"),
		"content": StringProperty("Content to write to the file"),
	}, "path", "content")
}

// EditSchema returns the JSON schema for the edit tool.
func EditSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"path":    StringProperty("Path to the file to edit (relative or absolute)"),
		"oldText": StringProperty("Exact text to find and replace (must match exactly)"),
		"newText": StringProperty("New text to replace the old text with"),
	}, "path", "oldText", "newText")
}

// GravatarFetchSchema returns the JSON schema for the Gravatar fetch tool.
func GravatarFetchSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"email": StringProperty("Email address to fetch from Gravatar. If omitted, uses the stored Gravatar email."),
	})
}

// GravatarSetSchema returns the JSON schema for the Gravatar set tool.
func GravatarSetSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"email": StringProperty("Email address to set as the primary Gravatar profile."),
	}, "email")
}

// MessageSchema returns the JSON schema for the message tool.
func MessageSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"action":     StringEnumProperty("The action to perform", []string{"send", "react", "reactions", "edit", "delete", "reply", "pin", "unpin", "list-pins", "thread-reply", "search", "read", "member-info", "channel-info", "channel-edit", "focus", "desktop-list-chats", "desktop-search-chats", "desktop-search-messages", "desktop-create-chat", "desktop-archive-chat", "desktop-set-reminder", "desktop-clear-reminder", "desktop-upload-asset", "desktop-download-asset"}),
		"message":    StringProperty("For send/edit/reply/thread-reply: the message text"),
		"media":      StringProperty("Optional: media URL/path/data URL to send (image/audio/video/file)."),
		"filename":   StringProperty("Optional: filename for media uploads."),
		"buffer":     StringProperty("Optional: base64 payload for attachments (optionally a data: URL)."),
		"mimeType":   StringProperty("Optional: content type override for attachments."),
		"caption":    StringProperty("Optional: caption for media uploads."),
		"path":       StringProperty("Optional: file path to upload (alias for media)."),
		"message_id": StringProperty("Target message ID for react/reactions/edit/delete/reply/pin/unpin/thread-reply/read"),
		"emoji":      StringProperty("For action=react: the emoji to react with (empty to remove all reactions)"),
		"remove":     BooleanProperty("For action=react: set true to remove the reaction instead of adding"),
		"user_id":    StringProperty("For action=member-info: the Matrix user ID to look up (e.g., @user:server.com)"),
		"thread_id":  StringProperty("For action=thread-reply: the thread root message ID"),
		"asVoice":    BooleanProperty("Optional: send audio as a voice message (when media is audio)."),
		"silent":     BooleanProperty("Optional: send silently (ignored by bridge)."),
		"quoteText":  StringProperty("Optional: quote text for replies (ignored by bridge)."),
		"bestEffort": BooleanProperty("Optional: best-effort delivery flag (ignored by bridge)."),
		"gifPlayback": BooleanProperty("Optional: treat video media as GIF playback (sets MauGIF flag)."),
		"buttons": map[string]any{
			"type":        "array",
			"description": "Optional: inline keyboard buttons (ignored by bridge).",
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text":          StringProperty("Button label text."),
						"callback_data": StringProperty("Callback payload for button clicks."),
						"url":           StringProperty("Optional URL to open when clicked."),
					},
				},
			},
		},
		"card": map[string]any{
			"type":        "object",
			"description": "Optional: adaptive card payload (ignored by bridge).",
		},
		"query":                   StringProperty("For action=search: search query to find messages"),
		"limit":                   NumberProperty("For action=search: max results to return (default: 20)"),
		"sessionKey":              StringProperty("Preferred canonical target (field 'sessionKey' from sessions_list). For desktop: desktop-api:<instance>:<chatId> or desktop-api:<chatId>."),
		"instance":                StringProperty("For desktop actions: named instance (leave empty for default; only needed with multiple instances)"),
		"label":                   StringProperty("Fallback desktop target by label/title (can be ambiguous; sessionKey is preferred)"),
		"chatId":                  StringProperty("For action=focus: desktop chat ID"),
		"accountId":               StringProperty("For desktop targeting: account ID filter; for desktop-create-chat: source account ID"),
		"network":                 StringProperty("For desktop targeting: network filter (e.g. whatsapp, instagram, imessage)"),
		"participantIds":          StringArrayProperty("For desktop-create-chat: participant user IDs"),
		"type":                    StringProperty("For desktop-create-chat: chat type (single|group)"),
		"archived":                BooleanProperty("For desktop-archive-chat: true to archive, false to unarchive"),
		"remindAtMs":              NumberProperty("For desktop-set-reminder: unix timestamp in milliseconds"),
		"dismissOnIncomingMessage": BooleanProperty("For desktop-set-reminder: dismiss reminder if new message arrives"),
		"uploadID":                StringProperty("For desktop send action: upload ID from desktop-upload-asset"),
		"attachmentType":          StringProperty("For desktop send action: attachment type override (gif|voiceNote|sticker)"),
		"url":                     StringProperty("For desktop-download-asset: mxc:// or localmxc:// URL"),
		"draftText":               StringProperty("For action=focus: draft text to prefill"),
		"draftAttachmentPath":     StringProperty("For action=focus: attachment file path to prefill"),
		"name":                    StringProperty("For action=channel-edit: new channel/room name; for action=desktop-create-chat: optional chat title"),
		"topic":                   StringProperty("For action=channel-edit: new channel/room topic"),
		"channel":                 StringProperty("Optional: channel override (ignored by bridge; current room only)."),
		"target":                  StringProperty("Optional: target override (ignored by bridge; current room only)."),
		"targets":                 StringArrayProperty("Optional: multi-target override (ignored by bridge; current room only)."),
		"dryRun":                  BooleanProperty("Optional: dry run (ignored by bridge)."),
	}, "action")
}

// CronSchema returns the JSON schema for the cron tool.
func CronSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "list", "add", "update", "remove", "run"},
				"description": "Action to perform: status, list, add, update, remove, run.",
			},
			"includeDisabled": map[string]any{
				"type":        "boolean",
				"description": "Include disabled jobs in list.",
			},
			"job": map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"description":          "Cron job payload for add.",
			},
			"jobId": map[string]any{
				"type":        "string",
				"description": "Cron job ID.",
			},
			"patch": map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"description":          "Patch object for update.",
			},
			"contextMessages": map[string]any{
				"type":        "number",
				"minimum":     0,
				"maximum":     10,
				"description": "For add: include recent context lines appended to payload.message.",
			},
		},
		"required": []string{"action"},
	}
}

// SessionStatusSchema returns the JSON schema for the session_status tool.
func SessionStatusSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"model": map[string]any{
				"type": "string",
			},
			"sessionKey": map[string]any{
				"type": "string",
			},
		},
	}
}

// ImageSchema returns the JSON schema for the OpenClaw image (vision) tool.
func ImageSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type": "string",
			},
			"image": map[string]any{
				"type": "string",
			},
			"model": map[string]any{
				"type": "string",
			},
			"maxBytesMb": map[string]any{
				"type": "number",
			},
		},
		"required": []string{"image"},
	}
}

// ImageGenerateSchema returns the JSON schema for the image generation tool.
func ImageGenerateSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"async":        BooleanProperty("Optional: if true, start image generation in the background and send results to the chat when ready (tool returns immediately)."),
		"prompt":       StringProperty("The text prompt describing the image to generate"),
		"model":        StringProperty("Optional: image model to use."),
		"count":        BoundedNumberProperty("Optional: number of images to generate (default: 1).", 1, 10),
		"aspect_ratio": StringProperty("Optional: desired aspect ratio (e.g., 1:1, 16:9, 4:3)."),
		"aspectRatio":  StringProperty("Optional: alias for aspect_ratio."),
		"resolution":   StringProperty("Optional: output resolution. Examples: 1K, 2K, 4K."),
		"input_images": StringArrayProperty("Optional: input images for editing/composition. Use mxc:// media URLs from the conversation (shown in [media_url: ...] tags or tool results). Also accepts file paths, web URLs, or data URIs."),
		"inputImages":  StringArrayProperty("Optional: alias for input_images."),
	}, "prompt")
}

// TTSSchema returns the JSON schema for the tts tool.
func TTSSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"async":   BooleanProperty("Optional: if true, start TTS in the background and send the audio to the chat when ready (tool returns immediately)."),
		"text":    StringProperty("Text to convert to speech."),
		"voice":   StringProperty("Optional: preferred voice (OpenAI voices: alloy, ash, coral, echo, fable, onyx, nova, sage, shimmer)."),
		"model":   StringProperty("Optional: TTS model (e.g. tts-1-hd, tts-1)."),
		"channel": StringProperty("Optional channel id to pick output format (e.g. telegram)."),
	}, "text")
}

func ObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func EmptyObjectSchema() map[string]any {
	return ObjectSchema(map[string]any{})
}

func StringProperty(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func NumberProperty(description string) map[string]any {
	return map[string]any{
		"type":        "number",
		"description": description,
	}
}

func BooleanProperty(description string) map[string]any {
	return map[string]any{
		"type":        "boolean",
		"description": description,
	}
}

func StringEnumProperty(description string, enumValues []string) map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        enumValues,
		"description": description,
	}
}

func BoundedNumberProperty(description string, minimum, maximum float64) map[string]any {
	return map[string]any{
		"type":        "number",
		"description": description,
		"minimum":     minimum,
		"maximum":     maximum,
	}
}

func StringArrayProperty(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

// MemorySearchSchema returns the JSON schema for the memory_search tool.
func MemorySearchSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"mode":       StringEnumProperty("Optional: search mode. auto (default), semantic, keyword, hybrid, or list (lists recent files; query optional).", []string{"auto", "semantic", "keyword", "hybrid", "list"}),
		"query":      StringProperty("Search query to find relevant memories. Required unless mode=list."),
		"sources":    StringArrayProperty("Optional: restrict to sources (e.g. ['memory','workspace'])."),
		"pathPrefix": StringProperty("Optional: restrict results to paths under this prefix (virtual paths). Examples: 'memory/', 'workspace/', 'SOUL.md'."),
		"maxResults": NumberProperty("Maximum number of results to return (default: 6)"),
		"minScore":   NumberProperty("Minimum relevance score threshold (0-1, default: 0.35)"),
	})
}

// MemoryGetSchema returns the JSON schema for the memory_get tool.
func MemoryGetSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"path":  StringProperty("Path to a memory file (e.g., 'MEMORY.md' or 'memory/2026-02-03.md')"),
		"from":  NumberProperty("Optional: starting line (ignored for Matrix)"),
		"lines": NumberProperty("Optional: number of lines (ignored for Matrix)"),
	}, "path")
}

// BeeperDocsSchema returns the JSON schema for the beeper_docs tool.
func BeeperDocsSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"query": StringProperty("Search query for Beeper help documentation."),
		"count": BoundedNumberProperty("Number of results to return (1-10).", 1, 10),
	}, "query")
}

// BeeperSendFeedbackSchema returns the JSON schema for the beeper_send_feedback tool.
func BeeperSendFeedbackSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"text": StringProperty("The feedback or bug report text to submit."),
		"type": StringProperty("Feedback type: 'problem' (default), 'suggestion', or 'question'."),
	}, "text")
}
