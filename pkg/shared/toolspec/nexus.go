package toolspec

// Nexus tool names and schemas mirrored from clay-nexus (toolsV2).
const (
	NexusGetUserInformationName          = "get_user_information"
	NexusGetUserInformationDescription   = "Get information about the current user."
	NexusSearchContactsName              = "searchContacts"
	NexusSearchContactsDescription       = "Search for contacts and return matching people. Use for contact lookup, filtering, and who-type questions."
	NexusGetContactName                  = "getContact"
	NexusGetContactDescription           = "Get full details for a contact by id, including emails, social links, phone numbers, and notes."
	NexusCreateContactName               = "createContact"
	NexusCreateContactDescription        = "Create a new contact record in Clay."
	NexusUpdateContactName               = "updateContact"
	NexusUpdateContactDescription        = "Update an existing contact record in Clay."
	NexusArchiveContactName              = "archive_contact"
	NexusArchiveContactDescription       = "Archive one or more contacts."
	NexusRestoreContactName              = "restore_contact"
	NexusRestoreContactDescription       = "Restore one or more archived contacts."
	NexusCreateNoteName                  = "createNote"
	NexusCreateNoteDescription           = "Create a note for a contact."
	NexusGetGroupsName                   = "getGroups"
	NexusGetGroupsDescription            = "Get all groups or lists for the user."
	NexusCreateGroupName                 = "createGroup"
	NexusCreateGroupDescription          = "Create a group or list for the user."
	NexusUpdateGroupName                 = "updateGroup"
	NexusUpdateGroupDescription          = "Update a group title and/or membership in a single call."
	NexusGetNotesName                    = "getNotes"
	NexusGetNotesDescription             = "Fetch notes in a date range."
	NexusGetEventsName                   = "getEvents"
	NexusGetEventsDescription            = "Fetch meetings/events in a date range."
	NexusGetUpcomingEventsName           = "getUpcomingEvents"
	NexusGetUpcomingEventsDescription    = "Fetch upcoming meetings/events."
	NexusGetEmailsName                   = "getEmails"
	NexusGetEmailsDescription            = "Fetch emails in a date range."
	NexusGetRecentEmailsName             = "getRecentEmails"
	NexusGetRecentEmailsDescription      = "Fetch recent emails when no explicit date range is given."
	NexusGetRecentRemindersName          = "getRecentReminders"
	NexusGetRecentRemindersDescription   = "Fetch recent reminders."
	NexusGetUpcomingRemindersName        = "getUpcomingReminders"
	NexusGetUpcomingRemindersDescription = "Fetch upcoming reminders."
	NexusFindDuplicatesName              = "find_duplicates"
	NexusFindDuplicatesDescription       = "Find potential duplicate contacts."
	NexusMergeContactsName               = "merge_contacts"
	NexusMergeContactsDescription        = "Merge contact ids into one contact. Destructive and irreversible."
)

func nexusDateRangeProperties() map[string]any {
	return map[string]any{
		"start": map[string]any{
			"type":        "string",
			"description": "Start date (YYYY-MM-DD).",
		},
		"end": map[string]any{
			"type":        "string",
			"description": "End date (YYYY-MM-DD).",
		},
		"contact_ids": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "number",
			},
			"description": "Optional contact ids to filter by.",
		},
	}
}

func nexusPaginationProperties() map[string]any {
	return map[string]any{
		"limit": map[string]any{
			"type":        "number",
			"description": "Optional page size.",
			"minimum":     1,
		},
		"page": map[string]any{
			"type":        "number",
			"description": "Optional page number (1-based).",
			"minimum":     1,
		},
		"contact_ids": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "number",
			},
			"description": "Optional contact ids to filter by.",
		},
	}
}

func NexusSearchContactsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural language query for contact search.",
			},
			"keywords": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
			"name": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
			"group_ids": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "number",
				},
			},
			"limit": map[string]any{
				"type":    "number",
				"minimum": 0,
				"maximum": 1000,
			},
			"exclude_contact_ids": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "number",
				},
			},
			"sort": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{
						"type": "string",
					},
					"direction": map[string]any{
						"type": "string",
						"enum": []string{"asc", "desc"},
					},
				},
			},
		},
	}
}

func NexusGetContactSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"contact_id": map[string]any{
				"type": "number",
			},
		},
		"required": []string{"contact_id"},
	}
}

func NexusCreateContactSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"properties": map[string]any{
			"first_name": map[string]any{"type": "string"},
			"last_name":  map[string]any{"type": "string"},
			"phone": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"email": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"linkedin":     map[string]any{"type": "string"},
			"bio":          map[string]any{"type": "string"},
			"title":        map[string]any{"type": "string"},
			"organization": map[string]any{"type": "string"},
			"birthday":     map[string]any{"type": "string"},
			"website": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}
}

func NexusUpdateContactSchema() map[string]any {
	schema := NexusCreateContactSchema()
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		schema["properties"] = props
	}
	props["contact_id"] = map[string]any{"type": "number"}
	schema["required"] = []string{"contact_id"}
	return schema
}

func NexusBulkContactActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"contact_ids": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "number"},
			},
		},
		"required": []string{"contact_ids"},
	}
}

func NexusCreateNoteSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"contact_id": map[string]any{"type": "number"},
			"content":    map[string]any{"type": "string"},
			"reminder_date": map[string]any{
				"type":        "string",
				"description": "Optional ISO date-time reminder.",
			},
		},
		"required": []string{"contact_id", "content"},
	}
}

func NexusGetGroupsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "number"},
		},
	}
}

func NexusCreateGroupSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string"},
		},
		"required": []string{"title"},
	}
}

func NexusUpdateGroupSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"group_id": map[string]any{"type": "number"},
			"title":    map[string]any{"type": "string"},
			"add_contact_ids": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "number"},
			},
			"remove_contact_ids": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "number"},
			},
		},
		"required": []string{"group_id"},
	}
}

func NexusGetNotesSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": nexusDateRangeProperties(),
		"required":   []string{"start", "end"},
	}
}

func NexusGetEventsSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": nexusDateRangeProperties(),
		"required":   []string{"start", "end"},
	}
}

func NexusGetUpcomingEventsSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": nexusPaginationProperties(),
	}
}

func NexusGetEmailsSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": nexusDateRangeProperties(),
		"required":   []string{"start", "end"},
	}
}

func NexusGetRecentEmailsSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": nexusPaginationProperties(),
	}
}

func NexusGetRecentRemindersSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": nexusPaginationProperties(),
	}
}

func NexusGetUpcomingRemindersSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": nexusPaginationProperties(),
	}
}

func NexusFindDuplicatesSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "number", "minimum": 1, "maximum": 100},
			"page":  map[string]any{"type": "number", "minimum": 1},
		},
	}
}

func NexusMergeContactsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"contact_ids": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "number"},
			},
		},
		"required": []string{"contact_ids"},
	}
}
