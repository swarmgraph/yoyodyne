package backend

import (
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// UsageWindowReading is what a provider last said about one of an account's
// usage windows: how much of it is used, when it resets, and the invocation
// that heard it. It is a reading rather than a refusal: the provider reports
// its windows on healthy invocations far more often than on refused ones, and
// what a reading is for is letting the operator see how close a window is
// before it refuses anything.
//
// A provider that reports no windows produces none of these, and nothing is
// inferred in their place.
type UsageWindowReading struct {
	// Provider is the backend the invocation was recorded under, and Account the
	// alias it ran under. Together with Window they are what a newer reading
	// replaces an older one by.
	Provider domain.Backend `json:"provider"`
	Account  string         `json:"account"`
	// Window is the provider's own name for the window, carried as it said it:
	// "five_hour", "seven_day", or a window of one model's own such as
	// "seven_day_opus". See SevenDayModel for how the last is read.
	Window string `json:"window"`
	// Utilization is the fraction of the window used, 0 to 1, as the provider
	// reported it. It is a pointer because a window the provider named without
	// a figure is a different fact from one it reported as unused.
	Utilization *float64 `json:"utilization,omitempty"`
	// ResetsAt is when the provider said the window resets, and absent where it
	// named no usable time.
	ResetsAt *time.Time `json:"resets_at,omitempty"`
	// Status is the provider's word for the window the event was about —
	// allowed, allowed_warning, rejected — and empty on the other windows the
	// same event reported, which the provider gave no status for.
	Status string `json:"status,omitempty"`
	// UsingOverage and OverageStatus are what the provider said about paid
	// usage beyond the plan on the same event.
	UsingOverage  bool   `json:"using_overage,omitempty"`
	OverageStatus string `json:"overage_status,omitempty"`
	// Model and Session are the invocation that heard the reading: the model it
	// was served by (the resolved identifier where the provider had said one
	// by then) and its provider session.
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
	// At is when the reading arrived.
	At time.Time `json:"at"`
}

// Key is what identifies the window a reading is about, so a later reading of
// the same window replaces it.
func (r UsageWindowReading) Key() string {
	return strings.Join([]string{string(r.Provider), strings.TrimSpace(r.Account), strings.TrimSpace(r.Window)}, "\x00")
}

// Usage window names the harness reads, in the provider's own spelling.
const (
	UsageWindowFiveHour = "five_hour"
	UsageWindowSevenDay = "seven_day"
	// usageWindowOverageIncluded is the seven-day window with paid overage
	// counted in. It is the account's, not any one model's.
	usageWindowOverageIncluded = "seven_day_overage_included"
)

// SevenDayModel is the model family a seven-day window is kept for, where the
// window is one model's own rather than the account's: "seven_day_opus" is
// opus's. It is empty for the account's own seven-day window and for every
// other window.
func (r UsageWindowReading) SevenDayModel() string {
	window := strings.TrimSpace(r.Window)
	if window == usageWindowOverageIncluded || !strings.HasPrefix(window, UsageWindowSevenDay+"_") {
		return ""
	}
	return strings.TrimPrefix(window, UsageWindowSevenDay+"_")
}

// Percent is the used fraction as a whole percentage, and false where the
// provider gave no figure.
func (r UsageWindowReading) Percent() (int, bool) {
	if r.Utilization == nil {
		return 0, false
	}
	return int(*r.Utilization*100 + 0.5), true
}

// UsageWindowRecorder is where an adapter hands the readings it hears, one
// event's worth at a time, as they arrive. It is optional on an adapter, and an
// adapter given none records nothing beyond its event log.
type UsageWindowRecorder interface {
	RecordUsageWindows(readings []UsageWindowReading) error
}
