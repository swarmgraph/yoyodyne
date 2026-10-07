package runstate

// Where the product manager's evaluations of the operator's ideas are kept.
//
// It is durable for the reason the proposed amendments are: the conversation
// that produced the recommendation ends, and its provider session and its
// process go with it, long before anybody acts on what was recommended. An
// evaluation that lived in the conversation would be gone exactly when somebody
// asked what a decision was decided from.
//
// It is append-only and nothing decides one. Unlike a proposed amendment there
// is no verdict to record here: an evaluation is advice, what becomes of the
// advice is a work item or a document revision with its own record, and a
// "decided" flag on the advice itself would be a second place the same decision
// was written down.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/evaluation"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// maxEncodedEvaluationBytes bounds one encoded record, including the trailing
// newline. The writer and the reader share it, so a record that was written is
// always one that can be read back. It is larger than an amendment's because an
// evaluation carries the research it rests on as well as the argument.
const maxEncodedEvaluationBytes = 128 << 10

// EvaluationStore is the log of recorded evaluations, in the same
// operating-system state root as runs and conversations and beside them rather
// than among them. It is one append-only log per product, like the collected
// reports and the proposed amendments: an evaluation outlives the conversation
// that reached it.
type EvaluationStore struct {
	root      string
	productID domain.ProductID
}

func NewEvaluationStore(root string, productID domain.ProductID) (*EvaluationStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &EvaluationStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *EvaluationStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the evaluations
// actually are.
func (s *EvaluationStore) Path() string { return filepath.Join(s.root, "evaluations.jsonl") }

// Append records one evaluation durably.
func (s *EvaluationStore) Append(recorded evaluation.Evaluation) error {
	if recorded.ProductID != s.productID {
		return fmt.Errorf("evaluation product %q does not match store product %q", recorded.ProductID, s.productID)
	}
	if err := recorded.Validate(); err != nil {
		return err
	}
	encoded, err := encodeEvaluation(recorded)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedEvaluationBytes {
		return fmt.Errorf("encoded evaluation is %d bytes, limit is %d", len(encoded), maxEncodedEvaluationBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create evaluation directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect evaluation log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open evaluation log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append evaluation: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append evaluation: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync evaluation log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close evaluation log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every evaluation in the order it was written. A log that does not
// exist yet is a product nobody has evaluated an idea for, which is not a
// failure to read.
func (s *EvaluationStore) List() ([]evaluation.Evaluation, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open evaluation log: %w", err)
	}
	defer file.Close()

	var recorded []evaluation.Evaluation
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedEvaluationBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		decoded, err := decodeEvaluation([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("decode evaluation log: %w", err)
		}
		if decoded.ProductID != s.productID {
			return nil, fmt.Errorf("decode evaluation log: evaluation product %q does not match store product %q",
				decoded.ProductID, s.productID)
		}
		recorded = append(recorded, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read evaluation log: %w", err)
	}
	return recorded, nil
}

// Find returns one recorded evaluation by identifier.
func (s *EvaluationStore) Find(id string) (evaluation.Evaluation, bool, error) {
	recorded, err := s.List()
	if err != nil {
		return evaluation.Evaluation{}, false, err
	}
	for _, one := range recorded {
		if one.ID == strings.TrimSpace(id) {
			return one, true, nil
		}
	}
	return evaluation.Evaluation{}, false, nil
}

func decodeEvaluation(data []byte) (evaluation.Evaluation, error) {
	var decoded evaluation.Evaluation
	if err := json.Unmarshal(data, &decoded); err != nil {
		return evaluation.Evaluation{}, err
	}
	if err := decoded.Validate(); err != nil {
		return evaluation.Evaluation{}, err
	}
	return decoded, nil
}

func encodeEvaluation(recorded evaluation.Evaluation) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(recorded); err != nil {
		return nil, fmt.Errorf("encode evaluation: %w", err)
	}
	return buffer.Bytes(), nil
}
