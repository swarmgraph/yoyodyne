// Package teamspends counts each work item's budgets across the factories of a
// team, through spend records in the shared records reference, in team mode only
// (docs/designs/working-alongside-other-contributors.md, section 8).
//
// A spend is one file, spends/<item>/<id>.json, naming the budget, the run, the
// factory and the time. Before a spend, the factory holding the item's claim
// fetches the reference and counts the item's spends of that budget across every
// factory, and is asked whether the count permits another. Only then does it
// write its record and push it with a lease, and the spend is made only once the
// push succeeded. A spend that cannot be pushed is not made and costs nothing:
// the caller waits and asks again. A factory that does not hold the item's claim
// spends nothing.
//
// Because the count is read from the records, a factory that takes over an
// expired claim starts from what the earlier holder recorded, and review rounds
// are counted from these records rather than from any machine's run records.
// Only the harness writes them, through the harness's own Git code in the
// machine's state directory; nothing here is reachable from an agent.
package teamspends

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// SchemaVersion is 1 and has never changed.
const SchemaVersion = 1

// maxAttempts is how many times one spend fetches, counts and pushes before it
// is left unmade for the caller to ask again.
const maxAttempts = 5

// returnedBudget is the budget a record names when it gives back a review round.
const returnedBudget = runstate.TriageReviewRoundBudget + " returned"

// Remote is the records repository (*gitworktree.RecordsRepository).
type Remote interface {
	Fetch(ctx context.Context) (string, error)
	Paths(ctx context.Context, commit string) (map[string]bool, error)
	Read(ctx context.Context, commit string, paths []string) (map[string][]byte, error)
	Build(ctx context.Context, parent string, files map[string][]byte, now time.Time) (string, error)
	Push(ctx context.Context, commit, expected string) error
}

// Claims says whether this factory holds the claim on an item
// (*runstate.TeamClaimStore reads it).
type Claims interface {
	Load(workItemID string) (runstate.TeamClaim, bool, error)
}

// Lock holds the machine's record sharing against every other process on it
// (*runstate.SharedRecordsStore), because the fetched reference is one the
// record sharing writes too.
type Lock interface {
	Lock(ctx context.Context) (func(), error)
}

// Record is the JSON a spend file holds.
type Record struct {
	SchemaVersion int `json:"schema_version"`
	// WorkItem is the item the spend is of, and is what is counted by: the
	// directory is a name for it, not the item itself.
	WorkItem string `json:"work_item"`
	// Budget is what was spent.
	Budget string `json:"budget"`
	// Run is the run the spend was made for.
	Run string `json:"run,omitempty"`
	// Key is the publication a merge re-arm repeated, or the developer attempt a
	// review round judged.
	Key string `json:"key,omitempty"`
	// Factory is the factory that made it.
	Factory string    `json:"factory"`
	At      time.Time `json:"at"`
}

// Spender counts and records one factory's spends. It is a runstate.SharedSpends.
type Spender struct {
	// Open opens the records repository. It is asked once per spend, so a
	// machine that has team mode on and never spends never touches the remote.
	Open   func(ctx context.Context) (Remote, error)
	Claims Claims
	Lock   Lock
	// Factory is this factory's identity, read when a record is written.
	Factory func(ctx context.Context) string
	Now     func() time.Time
}

var _ runstate.SharedSpends = Spender{}

// remote opens the records repository, as a failure to share the spend: the
// repository is in the machine's state directory and a spend that cannot reach
// it is not made.
func (s Spender) remote(ctx context.Context) (Remote, error) {
	if s.Open == nil {
		return nil, errors.New("no records repository is configured")
	}
	return s.Open(ctx)
}

func (s Spender) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// plainName is an item identifier that can be a directory name as it is.
var plainName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// directory is where one item's spends are kept. An identifier that is not a
// safe directory name is named by a digest of it, and every record names the
// item it is of, so two items never share a count.
func directory(workItemID string) string {
	if plainName.MatchString(workItemID) && len(workItemID) <= 100 && !strings.HasSuffix(workItemID, ".json") {
		return "spends/" + workItemID + "/"
	}
	sum := sha256.Sum256([]byte(workItemID))
	return "spends/item-" + hex.EncodeToString(sum[:])[:32] + "/"
}

func slug(budget string) string {
	return strings.NewReplacer(" ", "-").Replace(budget)
}

// path names one spend. A review round is named by the attempt it judged, so
// the same round recorded twice is one file; every other spend is its own.
func (s Spender) path(workItemID string, spend runstate.SharedSpend, at time.Time) (string, error) {
	budget := spend.Budget
	if spend.Return {
		budget = returnedBudget
	}
	if budget == runstate.TriageReviewRoundBudget || budget == returnedBudget {
		sum := sha256.Sum256([]byte(budget + "\x00" + spend.Key))
		return directory(workItemID) + slug(budget) + "-" + hex.EncodeToString(sum[:])[:24] + ".json", nil
	}
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("name the spend: %w", err)
	}
	return fmt.Sprintf("%s%s-%019d-%s.json", directory(workItemID), slug(budget), at.UTC().UnixNano(), hex.EncodeToString(random[:])), nil
}

func (s Spender) factory(ctx context.Context) string {
	if s.Factory != nil {
		if name := strings.TrimSpace(s.Factory(ctx)); name != "" && !strings.ContainsAny(name, "\r\n") {
			return name
		}
	}
	return "an unnamed factory"
}

func encode(record Record) ([]byte, error) {
	record.SchemaVersion = SchemaVersion
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, fmt.Errorf("encode the spend record: %w", err)
	}
	return encoded.Bytes(), nil
}

// Count is what an item has spent across every factory, read from the commit.
func (s Spender) count(ctx context.Context, remote Remote, commit string, paths map[string]bool, workItemID string) (runstate.SharedSpendCounts, error) {
	prefix := directory(workItemID)
	var wanted []string
	for path := range paths {
		if strings.HasPrefix(path, prefix) && !strings.Contains(strings.TrimPrefix(path, prefix), "/") {
			wanted = append(wanted, path)
		}
	}
	sort.Strings(wanted)
	counts := runstate.SharedSpendCounts{}
	if len(wanted) == 0 {
		return counts, nil
	}
	contents, err := remote.Read(ctx, commit, wanted)
	if err != nil {
		return runstate.SharedSpendCounts{}, err
	}
	returned := 0
	for _, path := range wanted {
		var record Record
		if err := json.Unmarshal(contents[path], &record); err != nil || record.SchemaVersion != SchemaVersion || record.WorkItem != workItemID {
			// A file that is not a spend of this item is no spend of it. It is left
			// out rather than refusing every spend of the item for good, because
			// anybody who can push to the remote can write one.
			continue
		}
		switch record.Budget {
		case runstate.TriageRepairGrantBudget:
			counts.RepairGrants++
		case runstate.TriageRerunBudget:
			counts.Reruns++
		case runstate.TriageReviewRoundBudget:
			counts.ReviewRounds++
		case returnedBudget:
			returned++
		case runstate.TriageMergeRearmBudget:
			if strings.TrimSpace(record.Key) == "" {
				continue
			}
			if counts.Rearms == nil {
				counts.Rearms = map[string]int{}
			}
			counts.Rearms[record.Key]++
		}
	}
	counts.ReviewRounds = max(counts.ReviewRounds-returned, 0)
	return counts, nil
}

func notShared(workItemID, budget, problem string) error {
	return &runstate.SpendNotSharedError{WorkItemID: workItemID, Budget: budget, Problem: problem}
}

// Spend is runstate.SharedSpends.Spend.
func (s Spender) Spend(ctx context.Context, workItemID string, spend runstate.SharedSpend, within func(runstate.SharedSpendCounts) error) error {
	workItemID = strings.TrimSpace(workItemID)
	if workItemID == "" || strings.TrimSpace(spend.Budget) == "" {
		return errors.New("a shared spend names the work item and the budget it spends")
	}
	if !spend.Return {
		claim, held, err := s.Claims.Load(workItemID)
		if err != nil {
			return notShared(workItemID, spend.Budget, "this machine's record of its claim on the item could not be read: "+err.Error())
		}
		if !held || !claim.Held() || claim.Lost != "" {
			return &runstate.NotClaimHolderError{WorkItemID: workItemID, Budget: spend.Budget}
		}
	}
	release, err := s.Lock.Lock(ctx)
	if err != nil {
		return err
	}
	defer release()

	remote, err := s.remote(ctx)
	if err != nil {
		return notShared(workItemID, spend.Budget, "the records repository could not be opened: "+err.Error())
	}
	problem := ""
	for attempt := 0; attempt < maxAttempts; attempt++ {
		fetched, err := remote.Fetch(ctx)
		if err != nil {
			return notShared(workItemID, spend.Budget, "the shared remote could not be reached: "+err.Error())
		}
		paths, err := remote.Paths(ctx, fetched)
		if err != nil {
			return notShared(workItemID, spend.Budget, "the shared records could not be read: "+err.Error())
		}
		counts, err := s.count(ctx, remote, fetched, paths, workItemID)
		if err != nil {
			return notShared(workItemID, spend.Budget, "the shared records could not be read: "+err.Error())
		}
		if within != nil {
			if err := within(counts); err != nil {
				return err
			}
		}
		at := s.now()
		path, err := s.path(workItemID, spend, at)
		if err != nil {
			return err
		}
		if paths[path] {
			// A round recorded already is on the remote, which is all a spend waits for.
			return nil
		}
		budget := spend.Budget
		if spend.Return {
			budget = returnedBudget
		}
		encoded, err := encode(Record{WorkItem: workItemID, Budget: budget, Run: spend.Run, Key: spend.Key, Factory: s.factory(ctx), At: at.UTC()})
		if err != nil {
			return err
		}
		commit, err := remote.Build(ctx, fetched, map[string][]byte{path: encoded}, at)
		if err != nil {
			return notShared(workItemID, spend.Budget, "the spend record could not be prepared: "+err.Error())
		}
		err = remote.Push(ctx, commit, fetched)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, gitworktree.ErrRecordsLeaseRefused):
			problem = "another factory kept pushing first"
		default:
			return notShared(workItemID, spend.Budget, "the shared remote did not take it: "+err.Error())
		}
	}
	return notShared(workItemID, spend.Budget, problem)
}

// Joined is what pushing an earlier count came to.
type Joined struct {
	// Pushed is how many spend records were written.
	Pushed int
}

// Join pushes the spends this machine had counted on its own items before it
// joined team mode, as records. Each is named by this factory, the budget and
// its place in the count, so pushing the same count again finds the records
// already there and writes none: the spends are shared once, however many times
// this is asked. Nothing is counted against any cap here, because they were
// spent already.
func (s Spender) Join(ctx context.Context, items []runstate.TriageCounters) (Joined, error) {
	factory := s.factory(ctx)
	owner := sha256.Sum256([]byte(factory))
	type pending struct {
		path    string
		content []byte
	}
	var want []pending
	at := s.now()
	add := func(item, budget, key string, n int) error {
		name := fmt.Sprintf("%sjoined-%s-%s-%s-%d.json", directory(item), slug(budget), hex.EncodeToString(owner[:])[:8], keyDigest(key), n)
		encoded, err := encode(Record{WorkItem: item, Budget: budget, Run: "before joining", Key: key, Factory: factory, At: at.UTC()})
		if err != nil {
			return err
		}
		want = append(want, pending{name, encoded})
		return nil
	}
	for _, counters := range items {
		item := counters.WorkItemID
		for n := 1; n <= counters.RepairGrants; n++ {
			if err := add(item, runstate.TriageRepairGrantBudget, "", n); err != nil {
				return Joined{}, err
			}
		}
		for n := 1; n <= counters.Reruns; n++ {
			if err := add(item, runstate.TriageRerunBudget, "", n); err != nil {
				return Joined{}, err
			}
		}
		for n := 1; n <= counters.ReviewRounds; n++ {
			if err := add(item, runstate.TriageReviewRoundBudget, "", n); err != nil {
				return Joined{}, err
			}
		}
		for publication, spent := range counters.RearmedPublications {
			for n := 1; n <= spent; n++ {
				if err := add(item, runstate.TriageMergeRearmBudget, publication, n); err != nil {
					return Joined{}, err
				}
			}
		}
	}
	if len(want) == 0 {
		return Joined{}, nil
	}
	release, err := s.Lock.Lock(ctx)
	if err != nil {
		return Joined{}, err
	}
	defer release()
	remote, err := s.remote(ctx)
	if err != nil {
		return Joined{}, err
	}
	problem := ""
	for attempt := 0; attempt < maxAttempts; attempt++ {
		fetched, err := remote.Fetch(ctx)
		if err != nil {
			return Joined{}, fmt.Errorf("the shared remote could not be reached: %w", err)
		}
		paths, err := remote.Paths(ctx, fetched)
		if err != nil {
			return Joined{}, err
		}
		files := map[string][]byte{}
		for _, record := range want {
			if !paths[record.path] {
				files[record.path] = record.content
			}
		}
		if len(files) == 0 {
			return Joined{}, nil
		}
		commit, err := remote.Build(ctx, fetched, files, at)
		if err != nil {
			return Joined{}, err
		}
		err = remote.Push(ctx, commit, fetched)
		switch {
		case err == nil:
			return Joined{Pushed: len(files)}, nil
		case errors.Is(err, gitworktree.ErrRecordsLeaseRefused):
			problem = "another factory kept pushing first"
		default:
			return Joined{}, fmt.Errorf("the shared remote did not take the earlier spends: %w", err)
		}
	}
	return Joined{}, errors.New("the earlier spends were not pushed: " + problem)
}

func keyDigest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}

// JoinKept pushes the spends this machine counted before it began counting
// through the shared records (runstate.SharedRecordsStore.KeepSpendsBefore), once.
// It pushes what was kept and never the live counters, which by now hold spends
// that are already records and other factories' spends copied in. A machine that
// already joined does nothing, and one that could not reach the remote tries
// again at the next check.
func (s Spender) JoinKept(ctx context.Context, state *runstate.SharedRecordsStore) (Joined, error) {
	if _, done, err := state.SpendsJoined(); err != nil || done {
		return Joined{}, err
	}
	kept, found, err := state.SpendsBefore()
	if err != nil || !found {
		return Joined{}, err
	}
	items := make([]runstate.TriageCounters, 0, len(kept))
	for _, k := range kept {
		items = append(items, runstate.TriageCounters{WorkItemID: k.WorkItemID, RepairGrants: k.RepairGrants, Reruns: k.Reruns, ReviewRounds: k.ReviewRounds, RearmedPublications: k.RearmedPublications})
	}
	joined, err := s.Join(ctx, items)
	if err != nil {
		return Joined{}, err
	}
	return joined, state.MarkSpendsJoined(s.now())
}
