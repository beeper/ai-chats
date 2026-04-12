package ai

type ScheduleTickContent struct {
	Kind           string `json:"kind"`
	EntityID       string `json:"entityId"`
	Revision       int    `json:"revision"`
	ScheduledForMs int64  `json:"scheduledForMs"`
	RunKey         string `json:"runKey"`
	Reason         string `json:"reason,omitempty"`
}
