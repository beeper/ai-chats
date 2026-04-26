package tools

import "github.com/beeper/agentremote/pkg/shared/toolspec"

var (
	MessageTool       = newBuiltinTool(toolspec.MessageName, toolspec.MessageDescription, "Message", toolspec.MessageSchema(), GroupMessaging, nil)
	WebFetchTool      = newBuiltinTool(toolspec.WebFetchName, toolspec.WebFetchDescription, "Web Fetch", toolspec.WebFetchSchema(), GroupWeb, nil)
	SessionStatusTool = newBuiltinTool(toolspec.SessionStatusName, toolspec.SessionStatusDescription, "Session Status", toolspec.SessionStatusSchema(), GroupStatus, nil)
	MemorySearchTool  = newBuiltinTool(toolspec.MemorySearchName, toolspec.MemorySearchDescription, "Memory Search", toolspec.MemorySearchSchema(), GroupMemory, nil)
	MemoryGetTool     = newBuiltinTool(toolspec.MemoryGetName, toolspec.MemoryGetDescription, "Memory Get", toolspec.MemoryGetSchema(), GroupMemory, nil)
	ImageTool         = newBuiltinTool(toolspec.ImageName, toolspec.ImageDescription, "Image", toolspec.ImageSchema(), GroupMedia, nil)
	ImageGenerateTool = newBuiltinTool(toolspec.ImageGenerateName, toolspec.ImageGenerateDescription, "Image Generate", toolspec.ImageGenerateSchema(), GroupMedia, nil)
	TTSTool           = newBuiltinTool(toolspec.TTSName, toolspec.TTSDescription, "TTS", toolspec.TTSSchema(), GroupMedia, nil)
	GravatarFetchTool = newBuiltinTool(toolspec.GravatarFetchName, toolspec.GravatarFetchDescription, "Gravatar Fetch", toolspec.GravatarFetchSchema(), GroupAgentRemote, nil)
	GravatarSetTool   = newBuiltinTool(toolspec.GravatarSetName, toolspec.GravatarSetDescription, "Gravatar Set", toolspec.GravatarSetSchema(), GroupAgentRemote, nil)
	CronTool          = newBuiltinTool(toolspec.CronName, toolspec.CronDescription, "Scheduler", toolspec.CronSchema(), GroupAgentRemote, nil)
	AgentsListTool    = newBuiltinTool("agents_list", "List agent ids you can target with sessions_spawn (based on allowlists).", "Agents List", toolspec.EmptyObjectSchema(), GroupSessions, nil)
)
