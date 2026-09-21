package obs

// MultiSink delivers one event to several sinks in order.
//
// Client holds a single sink, and more than one thing needs the stream: the
// MCP notification path, and the Lua bridge waiting for its replies. Nil
// members are skipped so a caller can wire an optional sink without branching.
type MultiSink []EventSink

func (m MultiSink) HandleEvent(e Event) {
	for _, sink := range m {
		if sink != nil {
			sink.HandleEvent(e)
		}
	}
}

// FilterSink forwards only the events Keep accepts.
//
// It exists because the bridge's transport sources produce a settings event on
// every reply. Those are plumbing, not OBS state changes, and letting them
// reach the automation engine would feed it traffic it caused -- the problem
// ADR-010 solved for rule writes, arriving by a different road.
type FilterSink struct {
	Sink EventSink
	Keep func(Event) bool
}

func (f FilterSink) HandleEvent(e Event) {
	if f.Sink == nil || (f.Keep != nil && !f.Keep(e)) {
		return
	}
	f.Sink.HandleEvent(e)
}
