package store

type SystemEvent struct {
	Text string
	TS   int64
}

type SystemEventQueue struct {
	SessionKey string
	Events     []SystemEvent
	LastText   string
}
