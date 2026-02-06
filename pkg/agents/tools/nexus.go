package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

func nexusTool(name, title, description string, schema map[string]any) *Tool {
	return &Tool{
		Tool: mcp.Tool{
			Name:        name,
			Description: description,
			Annotations: &mcp.ToolAnnotations{Title: title},
			InputSchema: schema,
		},
		Type:  ToolTypeBuiltin,
		Group: GroupNexus,
	}
}

var NexusSearchContactsTool = nexusTool(
	toolspec.NexusSearchContactsName,
	"Nexus Search Contacts",
	toolspec.NexusSearchContactsDescription,
	toolspec.NexusSearchContactsSchema(),
)

var NexusGetContactTool = nexusTool(
	toolspec.NexusGetContactName,
	"Nexus Get Contact",
	toolspec.NexusGetContactDescription,
	toolspec.NexusGetContactSchema(),
)

var NexusCreateContactTool = nexusTool(
	toolspec.NexusCreateContactName,
	"Nexus Create Contact",
	toolspec.NexusCreateContactDescription,
	toolspec.NexusCreateContactSchema(),
)

var NexusUpdateContactTool = nexusTool(
	toolspec.NexusUpdateContactName,
	"Nexus Update Contact",
	toolspec.NexusUpdateContactDescription,
	toolspec.NexusUpdateContactSchema(),
)

var NexusArchiveContactTool = nexusTool(
	toolspec.NexusArchiveContactName,
	"Nexus Archive Contact",
	toolspec.NexusArchiveContactDescription,
	toolspec.NexusBulkContactActionSchema(),
)

var NexusRestoreContactTool = nexusTool(
	toolspec.NexusRestoreContactName,
	"Nexus Restore Contact",
	toolspec.NexusRestoreContactDescription,
	toolspec.NexusBulkContactActionSchema(),
)

var NexusCreateNoteTool = nexusTool(
	toolspec.NexusCreateNoteName,
	"Nexus Create Note",
	toolspec.NexusCreateNoteDescription,
	toolspec.NexusCreateNoteSchema(),
)

var NexusGetGroupsTool = nexusTool(
	toolspec.NexusGetGroupsName,
	"Nexus Get Groups",
	toolspec.NexusGetGroupsDescription,
	toolspec.NexusGetGroupsSchema(),
)

var NexusCreateGroupTool = nexusTool(
	toolspec.NexusCreateGroupName,
	"Nexus Create Group",
	toolspec.NexusCreateGroupDescription,
	toolspec.NexusCreateGroupSchema(),
)

var NexusUpdateGroupTool = nexusTool(
	toolspec.NexusUpdateGroupName,
	"Nexus Update Group",
	toolspec.NexusUpdateGroupDescription,
	toolspec.NexusUpdateGroupSchema(),
)

var NexusGetNotesTool = nexusTool(
	toolspec.NexusGetNotesName,
	"Nexus Get Notes",
	toolspec.NexusGetNotesDescription,
	toolspec.NexusGetNotesSchema(),
)

var NexusGetEventsTool = nexusTool(
	toolspec.NexusGetEventsName,
	"Nexus Get Events",
	toolspec.NexusGetEventsDescription,
	toolspec.NexusGetEventsSchema(),
)

var NexusGetUpcomingEventsTool = nexusTool(
	toolspec.NexusGetUpcomingEventsName,
	"Nexus Get Upcoming Events",
	toolspec.NexusGetUpcomingEventsDescription,
	toolspec.NexusGetUpcomingEventsSchema(),
)

var NexusGetEmailsTool = nexusTool(
	toolspec.NexusGetEmailsName,
	"Nexus Get Emails",
	toolspec.NexusGetEmailsDescription,
	toolspec.NexusGetEmailsSchema(),
)

var NexusGetRecentEmailsTool = nexusTool(
	toolspec.NexusGetRecentEmailsName,
	"Nexus Get Recent Emails",
	toolspec.NexusGetRecentEmailsDescription,
	toolspec.NexusGetRecentEmailsSchema(),
)

var NexusGetRecentRemindersTool = nexusTool(
	toolspec.NexusGetRecentRemindersName,
	"Nexus Get Recent Reminders",
	toolspec.NexusGetRecentRemindersDescription,
	toolspec.NexusGetRecentRemindersSchema(),
)

var NexusGetUpcomingRemindersTool = nexusTool(
	toolspec.NexusGetUpcomingRemindersName,
	"Nexus Get Upcoming Reminders",
	toolspec.NexusGetUpcomingRemindersDescription,
	toolspec.NexusGetUpcomingRemindersSchema(),
)

var NexusFindDuplicatesTool = nexusTool(
	toolspec.NexusFindDuplicatesName,
	"Nexus Find Duplicates",
	toolspec.NexusFindDuplicatesDescription,
	toolspec.NexusFindDuplicatesSchema(),
)

var NexusMergeContactsTool = nexusTool(
	toolspec.NexusMergeContactsName,
	"Nexus Merge Contacts",
	toolspec.NexusMergeContactsDescription,
	toolspec.NexusMergeContactsSchema(),
)

func NexusTools() []*Tool {
	return []*Tool{
		NexusSearchContactsTool,
		NexusGetContactTool,
		NexusCreateContactTool,
		NexusUpdateContactTool,
		NexusArchiveContactTool,
		NexusRestoreContactTool,
		NexusCreateNoteTool,
		NexusGetGroupsTool,
		NexusCreateGroupTool,
		NexusUpdateGroupTool,
		NexusGetNotesTool,
		NexusGetEventsTool,
		NexusGetUpcomingEventsTool,
		NexusGetEmailsTool,
		NexusGetRecentEmailsTool,
		NexusGetRecentRemindersTool,
		NexusGetUpcomingRemindersTool,
		NexusFindDuplicatesTool,
		NexusMergeContactsTool,
	}
}
