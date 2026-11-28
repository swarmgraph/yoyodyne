package epicjoin

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// The Lead Product Manager answers a question about scope first, and her handling
// decides only whether the operator is asked: it names him, for the reason of
// lifting a hold he placed, or it does not. Where it does, the harness writes his
// one question itself, building each command from the identifiers, with her words
// beside it as her reason; it reads nothing from her words to choose a command.
// docs/designs/epic-mode-second-part.md, "What records the answer on scope",
// points 5 to 7, states the rule.

// askOperator files the one question a piece owes the operator. Its identifier is
// written into the piece's judgment entry first, with where the piece stands, so a
// stop between the two writes files that same question at the next round and no
// second one.
func (j Joiner) askOperator(naming epicprogress.Naming, item string) error {
	view, err := j.read()
	if err != nil {
		return err
	}
	entry, _ := view.Entry(naming)
	piece, asked := entry.Asked(item)
	if !asked || piece.Answered == nil || piece.Answered.Operator == "" {
		return nil
	}
	question := piece.Answered.Asked
	if question == nil {
		id, err := report.NewID()
		if err != nil {
			return err
		}
		kept, err := j.Progress.OperatorAsked(naming, item, epicprogress.OperatorQuestion{Report: id, At: j.Now().UTC(), Joined: entry.Has(item)})
		if err != nil {
			return fmt.Errorf("keep the question to the operator about %s: %w", item, err)
		}
		question = &kept
	} else if filed, err := (&pile{reports: j.Evidence.Reports}).filed(question.Report); err != nil || filed {
		return err
	}
	return j.Evidence.Reports.Append(j.operatorQuestion(naming, piece, *question))
}

// operatorQuestion is the report that asks the operator about one piece. It is
// addressed to the Lead Product Manager, who answered first and may record that
// nothing more is needed of her, and said to the operators directly, once.
func (j Joiner) operatorQuestion(naming epicprogress.Naming, piece epicprogress.Piece, question epicprogress.OperatorQuestion) report.Report {
	epic, item := naming.WorkItemID, piece.WorkItemID
	offers := question.Offers(epic, item)
	var asks string
	if question.Joined {
		asks = fmt.Sprintf("%s joined epic %s, and its edited text was judged not to be asked for by the epic, so it waits. "+
			"Answer with one of two commands: `%s` keeps it as it now reads, because an item you added is never compared again; "+
			"`%s` lets the epic be delivered without it.", item, epic, offers[0], offers[1])
	} else {
		asks = fmt.Sprintf("%s was made inside epic %s and judged not to be asked for by it, so it stays outside and starts nothing. "+
			"`%s` puts it in.", item, epic, offers[0])
	}
	says := strings.Join(strings.Fields(piece.Answered.Says), " ")
	message := fmt.Sprintf("A question for the operator about the scope of epic %s, for the reason %s: %s "+
		"The other members carry on meanwhile. The Lead Product Manager's reason, from her handling of %s: %s",
		epic, piece.Answered.Operator, asks, piece.Question, oneline.Fold(says, report.MaxMessageBytes/2))
	return report.Report{
		SchemaVersion: report.SchemaVersion, ID: question.Report, Role: report.HarnessReporter, RunID: report.ScopeOperatorQuestionRun + epic,
		WorkItemID: item, DuringEpic: epic, Build: j.Evidence.Build, ProductID: j.Product, RepositoryID: j.Evidence.RepositoryID,
		Severity: report.SeverityWarning, For: domain.RoleProductManager,
		Message: oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)), RecordedAt: question.At,
	}
}
