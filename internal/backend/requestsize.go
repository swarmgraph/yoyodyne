package backend

import "fmt"

// RequestSizer reports an endpoint's input bound and measures the text its
// adapter actually sends, including adapter-added instructions and framing.
// Bytes may conservatively overestimate a provider's character count. A zero
// limit means the adapter knows no bound; it is never a promise of capacity.
// This is separate from a native session's context window, which the harness
// cannot measure from the new prompt alone.
type RequestSizer interface {
	RequestSize(RunRequest) (bytes, limitBytes int)
}

// RequestTooLarge is a request refused locally before starting the provider.
type RequestTooLarge struct {
	Bytes      int
	LimitBytes int
}

func (e *RequestTooLarge) Error() string {
	return fmt.Sprintf("provider request is %d bytes, limit is %d", e.Bytes, e.LimitBytes)
}

// CheckRequestSize is the adapter's last check before launching a provider.
// Conversation callers leave a margin and shorten their reconstruction first;
// other callers still cannot send an input past the endpoint's known bound.
func CheckRequestSize(sizer RequestSizer, request RunRequest) error {
	size, limit := sizer.RequestSize(request)
	if limit > 0 && size > limit {
		return &RequestTooLarge{Bytes: size, LimitBytes: limit}
	}
	return nil
}

// InstructionsKeeper is implemented by an adapter whose provider session keeps
// the instructions a request carried, because the adapter sends them inside the
// message rather than apart from it. A turn that resumes such a session already
// holds what its first turn was told, so text the harness adds to the
// instructions for a whole run of turns need not be sent again. An adapter that
// does not implement it is taken to send instructions apart, afresh on every
// call, so nothing is left out of a turn it serves.
type InstructionsKeeper interface {
	SessionKeepsInstructions() bool
}

// SessionKeepsInstructions reports whether adapter's sessions keep the
// instructions a request carried; see InstructionsKeeper.
func SessionKeepsInstructions(adapter any) bool {
	keeper, ok := adapter.(InstructionsKeeper)
	return ok && keeper.SessionKeepsInstructions()
}
