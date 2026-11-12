// Package mcpcall is the only code in the harness that speaks the Model Context
// Protocol (docs/designs/mcp-tools-through-the-harness.md, section 4).
//
// This file and the two transports beside it are the harness's own small
// client. It speaks only what a client needing a tool call speaks: the opening
// exchange, listing tools, and calling a tool, over standard input and output
// for a server the harness starts and over https for a remote one. It declares
// no client abilities, and every request a server makes back — asking a model,
// asking a person, listing roots, even a ping — is answered with a refusal, so
// nothing a server asks reaches an agent or the operator.
//
// A server lives for one batch: batch starts it, or opens a fresh session with
// it, runs the calls it is given, and ends it before returning, so nothing is
// left running to supervise or move onto a new build. A command server runs in
// a process group of its own, with an environment built from a list rather than
// inherited, and with its own folder under the state directory as both its
// working directory and its home — mcpcredential.ServerDirectoryWithin, the
// folder its login was kept in — and the group is ended afterwards however
// the batch ended. A remote server's redirects are decided by
// mcpcredential.CheckRedirect, so a credential never leaves its host.
//
// A credential is read by internal/mcpcredential's Stores.Read, from the store
// the server's declaration names, and travels only where the server needs it: in
// the environment variable a command server names, or in the Authorization
// header of a request to a remote one. It is in no argument the client passes,
// and it is removed from every error the client returns, including errors whose
// text a server wrote.
//
// The client is reached only through the one call path in path.go, Path.Do,
// which decides who may call what, records each call, and scrubs and bounds
// what comes back; batch is unexported so nothing outside this package can
// reach a server any other way. A result the client returns is still the
// server's untrusted data until that path has handled it.
package mcpcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// ProtocolVersions are the MCP protocol versions this client accepts, newest
// first. The opening exchange asks for the first, and a server answering with
// any version not on this list is refused and ended. This is the one list: a
// version is supported when it is added here and tested, and nowhere else.
var ProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// ClientName is the name the client gives itself in the opening exchange.
const ClientName = "yoyo"

// maxMessageBytes bounds one message read from a server. A larger message
// ends the batch rather than being read whole, so a server cannot make the
// harness hold more than this in memory for one answer. It is far above what
// reaches an agent or a record, which the call path bounds separately.
const maxMessageBytes = 4 << 20

// maxToolPages bounds how many pages a tool listing may run to, so a server
// that hands back a cursor forever cannot hold a batch open.
const maxToolPages = 64

var (
	// ErrRefused is a server the client will not start or speak to as declared:
	// neither or both of a command and a url, a url that is not https, a
	// credential with nowhere to go, or a protocol version not on the list. A
	// configuration that loaded refuses the first three already; the client
	// refuses them again because it is the last thing between a declaration and
	// a started program.
	ErrRefused = errors.New("the MCP server is refused")
	// ErrCredential is a credential the stores would not produce.
	ErrCredential = errors.New("the MCP server's credential could not be read")
	// ErrFailed is a server that could not be started, stopped answering,
	// answered with something that is not MCP, or answered a request with an
	// error.
	ErrFailed = errors.New("the MCP server's answer failed")
	// ErrClosed is a call made on a session whose batch has ended.
	ErrClosed = errors.New("the MCP batch has ended")
	// ErrUnreachable is a server the message never reached or whose answer
	// never came back because the connection or the program went away: a
	// remote server that could not be reached or answered that it is
	// unavailable, or a command server that stopped before answering. It is
	// always also ErrFailed. The call path tries a read once more on it.
	ErrUnreachable = errors.New("the MCP server could not be reached")
	// errAnswered is a request the server answered with an error, so it is
	// known to have been heard and refused. It is always also ErrFailed.
	errAnswered = errors.New("the MCP server answered with an error")
)

// Server is one declared server: its name and its declaration in the
// project's mcp section, which a configuration that loaded has already checked.
type Server struct {
	Name     string
	Declared config.MCPServer
}

// Options are what the caller supplies beside the server.
type Options struct {
	// Stores is where the server's credential is read from, under the name its
	// own name derives, and whose state root holds a command server's own
	// folder. A server declaring no credential store reads nothing.
	Stores mcpcredential.Stores
	// Transport carries a remote server's requests. Nil is Go's default; a test
	// supplies the one that trusts its own server's certificate. Redirects are
	// always decided by mcpcredential.CheckRedirect, whatever carries them.
	Transport http.RoundTripper
	// ClientVersion is the harness's version, given in the opening exchange.
	ClientVersion string
}

// Tool is one tool as a server lists it. Everything in it is the server's own
// statement and untrusted: the class of a tool comes from the project's
// declaration, never from here.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Content is one item of a tool's result.
type Content struct {
	// Type is the server's word for the item: text, image, audio, resource,
	// resource_link, or anything else it sends.
	Type string
	// Text is the item's text, for a text item.
	Text string
	// Raw is the whole item as the server sent it.
	Raw json.RawMessage
}

// Result is what a tool call returned.
type Result struct {
	Content []Content
	// Structured is the result's structured content, where the server sent one.
	Structured json.RawMessage
	// IsError is the server saying the tool itself failed. The call was made
	// and answered; what went wrong is in the content.
	IsError bool
	// Raw is the whole result as the server sent it.
	Raw json.RawMessage
}

// Session is one batch's conversation with a server. It exists only inside
// the function batch is given, and is ended when that function returns.
type Session struct {
	server     Server
	transport  transport
	credential string
	version    string
	nextID     int64
	closed     bool
}

// Version is the protocol version the opening exchange settled on.
func (s *Session) Version() string { return s.version }

// batch starts the server, or opens a fresh session with a remote one, does
// the opening exchange, runs calls, and ends the server before it returns,
// whichever way calls ended. The context bounds the whole batch: when it ends,
// a command server's group is killed and a remote request is abandoned.
func batch(ctx context.Context, server Server, options Options, calls func(*Session) error) error {
	session, err := open(ctx, server, options)
	if err != nil {
		return err
	}
	callErr := calls(session)
	closeErr := session.close()
	if callErr != nil {
		return callErr
	}
	return closeErr
}

func open(ctx context.Context, server Server, options Options) (*Session, error) {
	declared := server.Declared
	hasCommand, hasURL := len(declared.Command) > 0, strings.TrimSpace(declared.URL) != ""
	if hasCommand == hasURL {
		return nil, fmt.Errorf("%w: mcp server %q must have exactly one of a command and a url", ErrRefused, server.Name)
	}
	if hasCommand && declared.Credential != config.MCPCredentialNone && strings.TrimSpace(declared.CredentialEnv) == "" {
		return nil, fmt.Errorf("%w: mcp server %q has a credential and no variable to hand it to the program in", ErrRefused, server.Name)
	}
	credential := ""
	if declared.Credential != config.MCPCredentialNone {
		value, err := options.Stores.Read(ctx, server.Name, declared)
		if err != nil {
			// The stores' errors name the server and the store and never the
			// value, and a failed read has no value to leak.
			return nil, fmt.Errorf("%w: %v", ErrCredential, err)
		}
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%w: the credential for mcp server %q is empty", ErrCredential, server.Name)
		}
		credential = value
	}
	session := &Session{server: server, credential: credential}
	var err error
	if hasCommand {
		session.transport, err = startCommand(ctx, server, options.Stores, credential)
	} else {
		session.transport, err = openURL(ctx, server, options, credential)
	}
	if err != nil {
		return nil, session.scrub(err)
	}
	if err := session.initialize(ctx, options.ClientVersion); err != nil {
		_ = session.close()
		return nil, err
	}
	return session, nil
}

// initialize is the opening exchange: ask for the newest version on the list,
// declare no client abilities, refuse a version off the list, and tell the
// server the exchange is done.
func (s *Session) initialize(ctx context.Context, clientVersion string) error {
	if clientVersion == "" {
		clientVersion = "development"
	}
	params := map[string]any{
		"protocolVersion": ProtocolVersions[0],
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": ClientName, "version": clientVersion},
	}
	raw, err := s.request(ctx, "initialize", params)
	if err != nil {
		return err
	}
	var answer struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return s.scrub(fmt.Errorf("%w: mcp server %q answered the opening exchange with something that is not MCP: %v", ErrFailed, s.server.Name, err))
	}
	if !accepted(answer.ProtocolVersion) {
		return s.scrub(fmt.Errorf("%w: mcp server %q speaks protocol version %q, and this client accepts only %s", ErrRefused, s.server.Name, answer.ProtocolVersion, strings.Join(ProtocolVersions, ", ")))
	}
	s.version = answer.ProtocolVersion
	s.transport.settled(answer.ProtocolVersion)
	if err := s.transport.notify(ctx, "notifications/initialized", nil); err != nil {
		return s.scrub(err)
	}
	return nil
}

func accepted(version string) bool {
	for _, known := range ProtocolVersions {
		if version == known {
			return true
		}
	}
	return false
}

// ListTools lists every tool the server offers, following its pages.
func (s *Session) ListTools(ctx context.Context) ([]Tool, error) {
	var tools []Tool
	cursor := ""
	for page := 0; ; page++ {
		if page == maxToolPages {
			return nil, fmt.Errorf("%w: mcp server %q listed more than %d pages of tools", ErrFailed, s.server.Name, maxToolPages)
		}
		var params map[string]any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		raw, err := s.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var answer struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &answer); err != nil {
			return nil, s.scrub(fmt.Errorf("%w: mcp server %q listed its tools in something that is not MCP: %v", ErrFailed, s.server.Name, err))
		}
		tools = append(tools, answer.Tools...)
		if answer.NextCursor == "" {
			return tools, nil
		}
		cursor = answer.NextCursor
	}
}

// CallTool calls one tool with one JSON object of arguments. A nil arguments
// is an empty object.
func (s *Session) CallTool(ctx context.Context, name string, arguments json.RawMessage) (Result, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &object); err != nil || object == nil {
		return Result{}, fmt.Errorf("%w: the arguments for tool %q of mcp server %q are not one JSON object", ErrRefused, name, s.server.Name)
	}
	raw, err := s.request(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return Result{}, err
	}
	var answer struct {
		Content           []json.RawMessage `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
		IsError           bool              `json:"isError"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Result{}, s.scrub(fmt.Errorf("%w: mcp server %q answered tool %q with something that is not MCP: %v", ErrFailed, s.server.Name, name, err))
	}
	result := Result{Structured: answer.StructuredContent, IsError: answer.IsError, Raw: raw}
	for _, item := range answer.Content {
		var head struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(item, &head)
		result.Content = append(result.Content, Content{Type: head.Type, Text: head.Text, Raw: item})
	}
	return result, nil
}

func (s *Session) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s.closed {
		return nil, fmt.Errorf("%w: mcp server %q", ErrClosed, s.server.Name)
	}
	s.nextID++
	message := map[string]any{"jsonrpc": "2.0", "id": s.nextID, "method": method}
	if params != nil {
		message["params"] = params
	}
	answer, err := s.transport.call(ctx, s.nextID, message)
	if err != nil {
		return nil, s.scrub(err)
	}
	if answer.Error != nil {
		return nil, s.scrub(fmt.Errorf("%w: %w: mcp server %q answered %s with error %d: %s", ErrFailed, errAnswered, s.server.Name, method, answer.Error.Code, oneLine(answer.Error.Message)))
	}
	return answer.Result, nil
}

func (s *Session) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.scrub(s.transport.close())
}

// scrub is an error with the credential taken out of its text. The error it
// returns wraps only the sentinel the original carried, so nothing that
// unwraps it can reach the original text either.
func (s *Session) scrub(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if s.credential != "" {
		text = strings.ReplaceAll(text, s.credential, "[credential removed]")
	}
	var kinds []error
	for _, sentinel := range []error{ErrRefused, ErrCredential, ErrClosed, ErrUnreachable, errAnswered, ErrFailed, context.DeadlineExceeded, context.Canceled} {
		if errors.Is(err, sentinel) {
			kinds = append(kinds, sentinel)
		}
	}
	if len(kinds) == 0 {
		kinds = []error{ErrFailed}
	}
	return &scrubbed{text: text, kinds: kinds}
}

type scrubbed struct {
	text  string
	kinds []error
}

func (e *scrubbed) Error() string { return e.text }
func (e *scrubbed) Is(target error) bool {
	for _, kind := range e.kinds {
		if target == kind {
			return true
		}
	}
	return false
}

// message is one JSON-RPC message from a server: an answer to a request of
// ours, a request of its own, or a notification.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// isRequest is a server asking something of the client.
func (m message) isRequest() bool { return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null" }

// answers is a message answering the request numbered id.
func (m message) answers(id int64) bool {
	return m.Method == "" && len(m.ID) > 0 && strings.TrimSpace(string(m.ID)) == strconv.FormatInt(id, 10)
}

// refusalCode is JSON-RPC's "method not found": the client offers no method a
// server could call.
const refusalCode = -32601

// refusal is the answer to every request a server makes.
func refusal(id json.RawMessage) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    refusalCode,
			"message": "this client declares no abilities and answers no request a server makes",
		},
	}
}

// transport is how messages reach one server and come back.
type transport interface {
	// call sends a request and returns the answer to it, refusing every request
	// the server makes meanwhile and passing over its notifications.
	call(ctx context.Context, id int64, request map[string]any) (message, error)
	// notify sends a notification, which has no answer.
	notify(ctx context.Context, method string, params any) error
	// settled is told the protocol version the opening exchange settled on.
	settled(version string)
	// close ends the server or the session.
	close() error
}

func notification(method string, params any) map[string]any {
	note := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		note["params"] = params
	}
	return note
}

// oneLine is text a server wrote, folded onto one line and cut on a rune
// boundary, for an error. Its words reach a conversation's prompt as the
// reason a call failed, so it is cut as every line a conversation reads is.
func oneLine(text string) string {
	return oneline.Fold(text, 512)
}
