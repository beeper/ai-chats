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

type nexusSpec struct {
	name, title, description string
	schema                   func() map[string]any
}

var nexusSpecs = []nexusSpec{
	{toolspec.NexusContactsName, "Contacts", toolspec.NexusContactsDescription, toolspec.NexusContactsSchema},
	{toolspec.NexusSearchContactsName, "Nexus Search Contacts", toolspec.NexusSearchContactsDescription, toolspec.NexusSearchContactsSchema},
	{toolspec.NexusGetContactName, "Nexus Get Contact", toolspec.NexusGetContactDescription, toolspec.NexusGetContactSchema},
	{toolspec.NexusCreateContactName, "Nexus Create Contact", toolspec.NexusCreateContactDescription, toolspec.NexusCreateContactSchema},
	{toolspec.NexusUpdateContactName, "Nexus Update Contact", toolspec.NexusUpdateContactDescription, toolspec.NexusUpdateContactSchema},
	{toolspec.NexusArchiveContactName, "Nexus Archive Contact", toolspec.NexusArchiveContactDescription, toolspec.NexusBulkContactActionSchema},
	{toolspec.NexusRestoreContactName, "Nexus Restore Contact", toolspec.NexusRestoreContactDescription, toolspec.NexusBulkContactActionSchema},
	{toolspec.NexusCreateNoteName, "Nexus Create Note", toolspec.NexusCreateNoteDescription, toolspec.NexusCreateNoteSchema},
	{toolspec.NexusGetGroupsName, "Nexus Get Groups", toolspec.NexusGetGroupsDescription, toolspec.NexusGetGroupsSchema},
	{toolspec.NexusCreateGroupName, "Nexus Create Group", toolspec.NexusCreateGroupDescription, toolspec.NexusCreateGroupSchema},
	{toolspec.NexusUpdateGroupName, "Nexus Update Group", toolspec.NexusUpdateGroupDescription, toolspec.NexusUpdateGroupSchema},
	{toolspec.NexusGetNotesName, "Nexus Get Notes", toolspec.NexusGetNotesDescription, toolspec.NexusGetNotesSchema},
	{toolspec.NexusGetEventsName, "Nexus Get Events", toolspec.NexusGetEventsDescription, toolspec.NexusGetEventsSchema},
	{toolspec.NexusGetUpcomingEventsName, "Nexus Get Upcoming Events", toolspec.NexusGetUpcomingEventsDescription, toolspec.NexusGetUpcomingEventsSchema},
	{toolspec.NexusGetEmailsName, "Nexus Get Emails", toolspec.NexusGetEmailsDescription, toolspec.NexusGetEmailsSchema},
	{toolspec.NexusGetRecentEmailsName, "Nexus Get Recent Emails", toolspec.NexusGetRecentEmailsDescription, toolspec.NexusGetRecentEmailsSchema},
	{toolspec.NexusGetRecentRemindersName, "Nexus Get Recent Reminders", toolspec.NexusGetRecentRemindersDescription, toolspec.NexusGetRecentRemindersSchema},
	{toolspec.NexusGetUpcomingRemindersName, "Nexus Get Upcoming Reminders", toolspec.NexusGetUpcomingRemindersDescription, toolspec.NexusGetUpcomingRemindersSchema},
	{toolspec.NexusFindDuplicatesName, "Nexus Find Duplicates", toolspec.NexusFindDuplicatesDescription, toolspec.NexusFindDuplicatesSchema},
	{toolspec.NexusMergeContactsName, "Nexus Merge Contacts", toolspec.NexusMergeContactsDescription, toolspec.NexusMergeContactsSchema},
}

func NexusTools() []*Tool {
	tools := make([]*Tool, len(nexusSpecs))
	for i, s := range nexusSpecs {
		tools[i] = nexusTool(s.name, s.title, s.description, s.schema())
	}
	return tools
}
