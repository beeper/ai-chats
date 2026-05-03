package connector

import (
	"cmp"
	"slices"
	"strings"

	"maunium.net/go/mautrix/event"
)

type mediaAttachment struct {
	Index         int
	URL           string
	MimeType      string
	EncryptedFile *event.EncryptedFileInfo
	FileName      string
}

func selectMediaAttachments(attachments []mediaAttachment, policy *MediaUnderstandingAttachmentsConfig) []mediaAttachment {
	if len(attachments) == 0 {
		return nil
	}

	mode := ""
	prefer := ""
	max := 1
	if policy != nil {
		mode = strings.TrimSpace(strings.ToLower(policy.Mode))
		prefer = strings.TrimSpace(strings.ToLower(policy.Prefer))
		if policy.MaxAttachments > 0 {
			max = policy.MaxAttachments
		}
	}
	if mode == "" {
		mode = "first"
	}

	ordered := make([]mediaAttachment, 0, len(attachments))
	ordered = append(ordered, attachments...)

	switch prefer {
	case "last":
		for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
			ordered[i], ordered[j] = ordered[j], ordered[i]
		}
	case "path":
		slices.SortStableFunc(ordered, func(a, b mediaAttachment) int {
			left := strings.ToLower(strings.TrimSpace(a.FileName))
			right := strings.ToLower(strings.TrimSpace(b.FileName))
			if left == "" && right == "" {
				return cmp.Compare(a.Index, b.Index)
			}
			if left == "" {
				return 1
			}
			if right == "" {
				return -1
			}
			if left == right {
				return cmp.Compare(a.Index, b.Index)
			}
			return cmp.Compare(left, right)
		})
	case "url":
		slices.SortStableFunc(ordered, func(a, b mediaAttachment) int {
			left := strings.ToLower(strings.TrimSpace(a.URL))
			right := strings.ToLower(strings.TrimSpace(b.URL))
			if left == right {
				return cmp.Compare(a.Index, b.Index)
			}
			return cmp.Compare(left, right)
		})
	}

	if mode == "all" {
		if max > 0 && len(ordered) > max {
			return ordered[:max]
		}
		return ordered
	}

	if len(ordered) == 0 {
		return nil
	}
	return ordered[:1]
}
