package toolspec

// ApplyPatchName is the name of the apply_patch tool.
const ApplyPatchName = "apply_patch"

// ApplyPatchDescription matches AgentRemote's apply_patch description.
const ApplyPatchDescription = "Apply a patch to one or more files using the apply_patch format. The input should include *** Begin Patch and *** End Patch markers."

// ApplyPatchSchema returns the JSON schema for the apply_patch tool.
func ApplyPatchSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"input": StringProperty("Patch content using the *** Begin Patch/End Patch format."),
	}, "input")
}
