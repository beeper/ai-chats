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
	CronDescription = "Manage cron jobs and wake events.\n\nACTIONS:\n- status: Check cron scheduler status\n- list: List jobs (use includeDisabled:true to include disabled)\n- add: Create job (requires job object, see schema below)\n- update: Modify job (requires jobId + patch object)\n- remove: Delete job (requires jobId)\n- run: Trigger job immediately (requires jobId)\n- runs: Get job run history (requires jobId)\n- wake: Send wake event (requires text, optional mode)\n\nJOB SCHEMA (for add action):\n{\n  \"name\": \"string (optional)\",\n  \"schedule\": { ... },      // Required: when to run\n  \"payload\": { ... },       // Required: what to execute\n  \"delivery\": { ... },      // Optional: announce summary (isolated only)\n  \"sessionTarget\": \"main\" | \"isolated\",  // Optional (defaults inferred)\n  \"enabled\": true | false   // Optional, default true\n}\n\nSCHEDULE TYPES (schedule.kind):\n- \"at\": One-shot at absolute time\n  { \"kind\": \"at\", \"at\": \"<ISO-8601 timestamp>\" }\n- \"every\": Recurring interval\n  { \"kind\": \"every\", \"everyMs\": <interval-ms>, \"anchorMs\": <optional-start-ms> }\n- \"cron\": Cron expression\n  { \"kind\": \"cron\", \"expr\": \"<cron-expression>\", \"tz\": \"<optional-timezone>\" }\n\nISO timestamps without an explicit timezone are treated as UTC.\n\nPAYLOAD TYPES (payload.kind):\n- \"systemEvent\": Injects text as system event into session\n  { \"kind\": \"systemEvent\", \"text\": \"<message>\" }\n- \"agentTurn\": Runs agent with message (isolated sessions only)\n  { \"kind\": \"agentTurn\", \"message\": \"<prompt>\", \"model\": \"<optional>\", \"thinking\": \"<optional>\", \"timeoutSeconds\": <optional> }\n\nDELIVERY (isolated-only, top-level):\n  { \"mode\": \"none|announce\", \"to\": \"<!room-id:server>\", \"bestEffort\": <optional-bool> }\n  - delivery.to: Matrix room ID (e.g. !abcdef:server.com). Omit to auto-route to the room where the job was created or last active room.\n  - Default for isolated agentTurn jobs (when delivery omitted): \"announce\"\n  - If the task needs to send to a specific chat/recipient, set delivery.to here; do not call messaging tools inside the run.\n\nCRITICAL CONSTRAINTS:\n- sessionTarget=\"main\" REQUIRES payload.kind=\"systemEvent\"\n- sessionTarget=\"isolated\" REQUIRES payload.kind=\"agentTurn\"\nDefault: prefer isolated agentTurn jobs unless the user explicitly wants a main-session system event.\n\nWAKE MODES (for wake action):\n- \"next-heartbeat\" (default): Wake on next heartbeat\n- \"now\": Wake immediately\n\nUse contextMessages (0-10) to add previous messages as context to the job text."

	SessionStatusName        = "session_status"
	SessionStatusDescription = "Show a /status-equivalent session status card (usage + time + cost when available). Use for model-use questions (📊 session_status). Optional: set per-session model override (model=default resets overrides)."

	// ImageName matches OpenClaw's image analysis tool (vision).
	ImageName                  = "image"
	ImageDescription           = "Analyze an image with the configured image model (agents.defaults.imageModel). Provide a prompt and image path or URL."
	ImageDescriptionVisionHint = "Analyze an image with a vision model. Only use this tool when the image was NOT already provided in the user's message. Images mentioned in the prompt are automatically visible to you."

	// ImageGenerateName is an AI image generation tool (not in OpenClaw).
	ImageGenerateName        = "image_generate"
	ImageGenerateDescription = "Generate one or more images from a text prompt. Optional controls include model selection, aspect ratio, resolution, and input images for editing/composition."

	TTSName        = "tts"
	TTSDescription = "Convert text to speech and return a MEDIA: path. Use when the user requests audio or TTS is enabled. Copy the MEDIA line exactly."

	MemorySearchName        = "memory_search"
	MemorySearchDescription = "Mandatory recall step: semantically search MEMORY.md + memory/*.md (and optional session transcripts) before answering questions about prior work, decisions, dates, people, preferences, or todos; returns top snippets with path + lines."
	MemoryGetName           = "memory_get"
	MemoryGetDescription    = "Safe snippet read from MEMORY.md, memory/*.md, or configured memorySearch.extraPaths with optional from/lines; use after memory_search to pull only the needed lines and keep context small."

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
)

// CalculatorSchema returns the JSON schema for the calculator tool.
func CalculatorSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expression": map[string]any{
				"type":        "string",
				"description": "A mathematical expression to evaluate, e.g. '2 + 3 * 4' or '100 / 5'",
			},
		},
		"required": []string{"expression"},
	}
}

// WebSearchSchema returns the JSON schema for the web search tool.
func WebSearchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query string.",
			},
			"count": map[string]any{
				"type":        "number",
				"description": "Number of results to return (1-10).",
				"minimum":     1,
				"maximum":     10,
			},
			"country": map[string]any{
				"type":        "string",
				"description": "2-letter country code for region-specific results (e.g., 'DE', 'US', 'ALL'). Default: 'US'.",
			},
			"search_lang": map[string]any{
				"type":        "string",
				"description": "ISO language code for search results (e.g., 'de', 'en', 'fr').",
			},
			"ui_lang": map[string]any{
				"type":        "string",
				"description": "ISO language code for UI elements.",
			},
			"freshness": map[string]any{
				"type":        "string",
				"description": "Filter results by discovery time (Brave only). Values: 'pd' (past 24h), 'pw' (past week), 'pm' (past month), 'py' (past year), or date range 'YYYY-MM-DDtoYYYY-MM-DD'.",
			},
		},
		"required": []string{"query"},
	}
}

// WebFetchSchema returns the JSON schema for the web fetch tool.
func WebFetchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "HTTP or HTTPS URL to fetch.",
			},
			"maxChars": map[string]any{
				"type":        "number",
				"description": "Maximum characters to return (truncates when exceeded).",
				"minimum":     100,
			},
			"extractMode": map[string]any{
				"type":        "string",
				"enum":        []string{"markdown", "text"},
				"description": "Extraction mode (\"markdown\" or \"text\").",
			},
		},
		"required": []string{"url"},
	}
}

// ReadSchema returns the JSON schema for the read tool.
func ReadSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read (relative or absolute)",
			},
			"offset": map[string]any{
				"type":        "number",
				"description": "Line number to start reading from (1-indexed)",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "Maximum number of lines to read",
			},
		},
		"required": []string{"path"},
	}
}

// WriteSchema returns the JSON schema for the write tool.
func WriteSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write (relative or absolute)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}

// EditSchema returns the JSON schema for the edit tool.
func EditSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to edit (relative or absolute)",
			},
			"oldText": map[string]any{
				"type":        "string",
				"description": "Exact text to find and replace (must match exactly)",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "New text to replace the old text with",
			},
		},
		"required": []string{"path", "oldText", "newText"},
	}
}

// GravatarFetchSchema returns the JSON schema for the Gravatar fetch tool.
func GravatarFetchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"email": map[string]any{
				"type":        "string",
				"description": "Email address to fetch from Gravatar. If omitted, uses the stored Gravatar email.",
			},
		},
	}
}

// GravatarSetSchema returns the JSON schema for the Gravatar set tool.
func GravatarSetSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"email": map[string]any{
				"type":        "string",
				"description": "Email address to set as the primary Gravatar profile.",
			},
		},
		"required": []string{"email"},
	}
}

// MessageSchema returns the JSON schema for the message tool.
func MessageSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"send", "react", "reactions", "edit", "delete", "reply", "pin", "unpin", "list-pins", "thread-reply", "search", "read", "member-info", "channel-info", "channel-edit", "focus", "desktop-list-chats", "desktop-search-chats", "desktop-search-messages", "desktop-create-chat", "desktop-archive-chat", "desktop-set-reminder", "desktop-clear-reminder", "desktop-upload-asset", "desktop-download-asset"},
				"description": "The action to perform",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "For send/edit/reply/thread-reply: the message text",
			},
			"media": map[string]any{
				"type":        "string",
				"description": "Optional: media URL/path/data URL to send (image/audio/video/file).",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "Optional: filename for media uploads.",
			},
			"buffer": map[string]any{
				"type":        "string",
				"description": "Optional: base64 payload for attachments (optionally a data: URL).",
			},
			"mimeType": map[string]any{
				"type":        "string",
				"description": "Optional: content type override for attachments.",
			},
			"caption": map[string]any{
				"type":        "string",
				"description": "Optional: caption for media uploads.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional: file path to upload (alias for media).",
			},
			"message_id": map[string]any{
				"type":        "string",
				"description": "Target message ID for react/reactions/edit/delete/reply/pin/unpin/thread-reply/read",
			},
			"emoji": map[string]any{
				"type":        "string",
				"description": "For action=react: the emoji to react with (empty to remove all reactions)",
			},
			"remove": map[string]any{
				"type":        "boolean",
				"description": "For action=react: set true to remove the reaction instead of adding",
			},
			"user_id": map[string]any{
				"type":        "string",
				"description": "For action=member-info: the Matrix user ID to look up (e.g., @user:server.com)",
			},
			"thread_id": map[string]any{
				"type":        "string",
				"description": "For action=thread-reply: the thread root message ID",
			},
			"asVoice": map[string]any{
				"type":        "boolean",
				"description": "Optional: send audio as a voice message (when media is audio).",
			},
			"silent": map[string]any{
				"type":        "boolean",
				"description": "Optional: send silently (ignored by bridge).",
			},
			"quoteText": map[string]any{
				"type":        "string",
				"description": "Optional: quote text for replies (ignored by bridge).",
			},
			"bestEffort": map[string]any{
				"type":        "boolean",
				"description": "Optional: best-effort delivery flag (ignored by bridge).",
			},
			"gifPlayback": map[string]any{
				"type":        "boolean",
				"description": "Optional: treat video media as GIF playback (sets MauGIF flag).",
			},
			"buttons": map[string]any{
				"type":        "array",
				"description": "Optional: inline keyboard buttons (ignored by bridge).",
				"items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"text": map[string]any{
								"type":        "string",
								"description": "Button label text.",
							},
							"callback_data": map[string]any{
								"type":        "string",
								"description": "Callback payload for button clicks.",
							},
							"url": map[string]any{
								"type":        "string",
								"description": "Optional URL to open when clicked.",
							},
						},
					},
				},
			},
			"card": map[string]any{
				"type":        "object",
				"description": "Optional: adaptive card payload (ignored by bridge).",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "For action=search: search query to find messages",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "For action=search: max results to return (default: 20)",
			},
			"sessionKey": map[string]any{
				"type":        "string",
				"description": "Preferred canonical target (field 'sessionKey' from sessions_list). For desktop: desktop-api:<instance>:<chatId> or desktop-api:<chatId>.",
			},
			"instance": map[string]any{
				"type":        "string",
				"description": "For desktop actions: desktop API instance name (default if omitted)",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Fallback desktop target by label/title (can be ambiguous; sessionKey is preferred)",
			},
			"chatId": map[string]any{
				"type":        "string",
				"description": "For action=focus: desktop chat ID",
			},
			"accountId": map[string]any{
				"type":        "string",
				"description": "For desktop targeting: account ID filter; for desktop-create-chat: source account ID",
			},
			"network": map[string]any{
				"type":        "string",
				"description": "For desktop targeting: network filter (e.g. whatsapp, instagram, imessage)",
			},
			"participantIds": map[string]any{
				"type":        "array",
				"description": "For desktop-create-chat: participant user IDs",
				"items": map[string]any{
					"type": "string",
				},
			},
			"type": map[string]any{
				"type":        "string",
				"description": "For desktop-create-chat: chat type (single|group)",
			},
			"archived": map[string]any{
				"type":        "boolean",
				"description": "For desktop-archive-chat: true to archive, false to unarchive",
			},
			"remindAtMs": map[string]any{
				"type":        "number",
				"description": "For desktop-set-reminder: unix timestamp in milliseconds",
			},
			"dismissOnIncomingMessage": map[string]any{
				"type":        "boolean",
				"description": "For desktop-set-reminder: dismiss reminder if new message arrives",
			},
			"uploadID": map[string]any{
				"type":        "string",
				"description": "For desktop send action: upload ID from desktop-upload-asset",
			},
			"attachmentType": map[string]any{
				"type":        "string",
				"description": "For desktop send action: attachment type override (gif|voiceNote|sticker)",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "For desktop-download-asset: mxc:// or localmxc:// URL",
			},
			"draftText": map[string]any{
				"type":        "string",
				"description": "For action=focus: draft text to prefill",
			},
			"draftAttachmentPath": map[string]any{
				"type":        "string",
				"description": "For action=focus: attachment file path to prefill",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "For action=channel-edit: new channel/room name; for action=desktop-create-chat: optional chat title",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "For action=channel-edit: new channel/room topic",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Optional: channel override (ignored by bridge; current room only).",
			},
			"target": map[string]any{
				"type":        "string",
				"description": "Optional: target override (ignored by bridge; current room only).",
			},
			"targets": map[string]any{
				"type":        "array",
				"description": "Optional: multi-target override (ignored by bridge; current room only).",
				"items": map[string]any{
					"type": "string",
				},
			},
			"dryRun": map[string]any{
				"type":        "boolean",
				"description": "Optional: dry run (ignored by bridge).",
			},
		},
		"required": []string{"action"},
	}
}

// CronSchema returns the JSON schema for the cron tool.
func CronSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "list", "add", "update", "remove", "run", "runs", "wake"},
				"description": "Action to perform: status, list, add, update, remove, run, runs, wake.",
			},
			"gatewayUrl": map[string]any{
				"type":        "string",
				"description": "Optional gateway URL override.",
			},
			"gatewayToken": map[string]any{
				"type":        "string",
				"description": "Optional gateway auth token.",
			},
			"timeoutMs": map[string]any{
				"type":        "number",
				"description": "Optional timeout for gateway call.",
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
			"text": map[string]any{
				"type":        "string",
				"description": "Text for wake action (system event).",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"now", "next-heartbeat"},
				"description": "Wake mode (now|next-heartbeat).",
			},
			"contextMessages": map[string]any{
				"type":        "number",
				"minimum":     0,
				"maximum":     10,
				"description": "For add: include recent context lines appended to systemEvent text.",
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
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"async": map[string]any{
				"type":        "boolean",
				"description": "Optional: if true, start image generation in the background and send results to the chat when ready (tool returns immediately).",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "The text prompt describing the image to generate",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional: image model to use.",
			},
			"count": map[string]any{
				"type":        "number",
				"description": "Optional: number of images to generate (default: 1).",
				"minimum":     1,
				"maximum":     10,
			},
			"aspect_ratio": map[string]any{
				"type":        "string",
				"description": "Optional: desired aspect ratio (e.g., 1:1, 16:9, 4:3).",
			},
			"aspectRatio": map[string]any{
				"type":        "string",
				"description": "Optional: alias for aspect_ratio.",
			},
			"resolution": map[string]any{
				"type":        "string",
				"description": "Optional: output resolution. Examples: 1K, 2K, 4K.",
			},
			"input_images": map[string]any{
				"type":        "array",
				"description": "Optional: input images for editing/composition (paths/URLs/data URIs).",
				"items": map[string]any{
					"type": "string",
				},
			},
			"inputImages": map[string]any{
				"type":        "array",
				"description": "Optional: alias for input_images.",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
		"required": []string{"prompt"},
	}
}

// TTSSchema returns the JSON schema for the tts tool.
func TTSSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"async": map[string]any{
				"type":        "boolean",
				"description": "Optional: if true, start TTS in the background and send the audio to the chat when ready (tool returns immediately).",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Text to convert to speech.",
			},
			"voice": map[string]any{
				"type":        "string",
				"description": "Optional: preferred voice (OpenAI voices: alloy, ash, coral, echo, fable, onyx, nova, sage, shimmer).",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional: TTS model (e.g. tts-1-hd, tts-1).",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Optional channel id to pick output format (e.g. telegram).",
			},
		},
		"required": []string{"text"},
	}
}

// MemorySearchSchema returns the JSON schema for the memory_search tool.
func MemorySearchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query to find relevant memories",
			},
			"maxResults": map[string]any{
				"type":        "number",
				"description": "Maximum number of results to return (default: 6)",
			},
			"minScore": map[string]any{
				"type":        "number",
				"description": "Minimum relevance score threshold (0-1, default: 0.35)",
			},
		},
		"required": []string{"query"},
	}
}

// MemoryGetSchema returns the JSON schema for the memory_get tool.
func MemoryGetSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to a memory file (e.g., 'MEMORY.md' or 'memory/2026-02-03.md')",
			},
			"from": map[string]any{
				"type":        "number",
				"description": "Optional: starting line (ignored for Matrix)",
			},
			"lines": map[string]any{
				"type":        "number",
				"description": "Optional: number of lines (ignored for Matrix)",
			},
		},
		"required": []string{"path"},
	}
}
