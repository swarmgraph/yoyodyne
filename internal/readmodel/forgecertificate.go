package readmodel

// The supervisor's forge checks failing on the system certificate store, said
// once with the cause and the fix.
//
// A supervisor started from a shell that cannot read the system certificate
// store cannot verify GitHub's certificate, so no forge check it runs can
// succeed, and pull requests merged there are neither noticed nor closed out.
// The supervisor's forge check writes how its checks stand to one record, and
// this is that record on the attention line and among the factory's problems.
// It is the harness's move: the supervisor has to be started again, and the
// harness's own commands do that.

import (
	"fmt"
	"strconv"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ForgeCertificateRecord is how the supervisor's forge checks stand. It is
// satisfied by *runstate.ForgeCertificateStore.
type ForgeCertificateRecord interface {
	Read() (runstate.ForgeCertificateFailure, error)
}

// ReadForgeCertificate reads whether the supervisor's forge checks are failing
// on the certificate store now. A reading with no record wired says nothing; a
// record that cannot be read says so rather than reporting the checks well.
//
// The record stands only while the supervisor that wrote it is the one running:
// a record left by a supervisor that has since stopped or been replaced says
// nothing about the one running now, whose own first check starts the account
// again, so it is not reported.
func ReadForgeCertificate(sources Sources, services *Services) (*runstate.ForgeCertificateFailure, string) {
	if sources.ForgeCertificates == nil {
		return nil, ""
	}
	record, err := sources.ForgeCertificates.Read()
	if err != nil {
		return nil, fmt.Sprintf("whether the supervisor's forge checks are failing on the system certificate store could not be read: %v", err)
	}
	if !record.Failing() {
		return nil, ""
	}
	if services == nil || !services.SupervisorRunning || !services.Recorded || services.Record.PID != record.SupervisorPID {
		return nil, ""
	}
	return &record, ""
}

// forgeCertificateAttention is the failure as the attention line carries it.
// The supervisor and the moment the failing began are the ID, since a later
// failure by another supervisor, or after a clean check, is a different one.
func forgeCertificateAttention(record runstate.ForgeCertificateFailure) Attention {
	return resolved(Attention{
		Kind:             AttentionForgeCertificate,
		ID:               strconv.Itoa(record.SupervisorPID) + ":" + record.Since.UTC().Format(time.RFC3339),
		ForgeCertificate: &record,
	})
}
