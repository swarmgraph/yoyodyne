package slack

// The log relay: the process between a launched sink and its log.
//
// The repository-confined-writes design forbids handing a descriptor the
// confined writer opened to another process, because the checks it makes
// before and after each batch only run on writes it makes itself. So the sink
// writes to a pipe, and the relay reads the pipe and appends what it reads to
// the log through the confined writer. The process that launches the sink
// exits long before the sink does, so the relay is a process of its own,
// started in the sink's place: this executable, recognised by relayVariable
// before anything else in it runs.
//
// When the checks stop the appends — the log's directory left the root, or the
// log gained a link from outside it — the relay keeps reading and discards what
// it reads, so the sink is not stopped by a pipe nobody empties, and says so
// once, in a note beside the log.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// relayVariable marks a process started as a log relay. The relay removes it
// from the environment it gives the sink.
const relayVariable = "YOYODYNE_SINK_LOG_RELAY"

// relayStoppedSuffix names the note beside a log whose appends were stopped.
const relayStoppedSuffix = ".stopped"

// relayConfig is what the launcher tells the relay, on its standard input.
type relayConfig struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
	Dir     string   `json:"dir"`
	LogRoot string   `json:"log_root"`
	Log     string   `json:"log"`
}

func init() {
	if os.Getenv(relayVariable) == "1" {
		os.Exit(runLogRelay(os.Stdin))
	}
}

func runLogRelay(input io.Reader) int {
	// The launcher reads one line from the relay's fourth descriptor: the
	// sink's process once it is started, or why it was not.
	report := os.NewFile(3, "the started sink")
	defer report.Close()
	fail := func(err error) int {
		fmt.Fprintln(report, strings.ReplaceAll(err.Error(), "\n", " "))
		return 2
	}
	var config relayConfig
	if err := json.NewDecoder(input).Decode(&config); err != nil {
		return fail(fmt.Errorf("read what to start: %w", err))
	}
	root, err := repowrite.NewRoot(config.LogRoot)
	if err != nil {
		return fail(err)
	}
	// The log is open before the sink starts, so the sink is never running
	// with nowhere to write, and the relay creates nothing once the sink is
	// running.
	appender, err := root.OpenAppender(config.Log, logPermissions, logDirectoryMode)
	if err != nil {
		return fail(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		appender.Close()
		return fail(err)
	}
	command := exec.Command(config.Program, config.Args...)
	command.Dir = config.Dir
	command.Env = withoutRelayVariable(os.Environ())
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Start(); err != nil {
		writer.Close()
		reader.Close()
		appender.Close()
		return fail(err)
	}
	writer.Close()
	fmt.Fprintln(report, command.Process.Pid)
	report.Close()
	// A stop signal sent to the relay is passed on to the sink, so stopping
	// either stops both.
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for received := range signals {
			_ = command.Process.Signal(received)
		}
	}()
	relayOutput(reader, appender, root, config.Log)
	reader.Close()
	err = command.Wait()
	signal.Stop(signals)
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return exited.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

// relayOutput appends everything read from output to the log, in the
// confined writer's checked batches, until output ends. Once an append is
// refused it reads on and discards, and records the refusal once.
func relayOutput(output io.Reader, appender *repowrite.Appender, root repowrite.Root, log string) {
	buffer := make([]byte, 64<<10)
	for {
		n, readErr := output.Read(buffer)
		if n > 0 && appender != nil {
			if _, err := appender.Write(buffer[:n]); err != nil {
				noteStopped(root, log, err)
				appender.Close()
				appender = nil
			}
		}
		if readErr != nil {
			break
		}
	}
	if appender != nil {
		appender.Sync()
		appender.Close()
	}
}

// noteStopped records, beside the log, why its appends stopped. It is
// published as a new file through the confined writer like any other record.
func noteStopped(root repowrite.Root, log string, cause error) {
	note := fmt.Sprintf("the sink's output stopped being written to %s, and what it said afterwards was discarded: %v\n", log, cause)
	_ = root.ReplaceFile(log+relayStoppedSuffix, []byte(note), logPermissions, logDirectoryMode)
}

func withoutRelayVariable(environment []string) []string {
	kept := make([]string, 0, len(environment))
	for _, variable := range environment {
		if !strings.HasPrefix(variable, relayVariable+"=") {
			kept = append(kept, variable)
		}
	}
	return kept
}
