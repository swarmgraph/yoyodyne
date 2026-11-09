package mcpcall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
)

// The remote transport is MCP's streamable HTTP: every message is a POST to
// the server's one address, and the answer to a request comes back either as
// one JSON message or as a stream of server-sent events ending with it. The
// client never opens the separate stream a server may offer for messages of
// its own, because it answers none of them; requests a server makes inside an
// answer's stream are refused by posting the refusal back.
//
// The older transport of 2024-11-05, with its separate event address, is not
// spoken: a remote server that offers only that fails at the opening exchange.

const (
	sessionHeader  = "Mcp-Session-Id"
	versionHeader  = "MCP-Protocol-Version"
	eventStream    = "text/event-stream"
	jsonMedia      = "application/json"
	acceptBoth     = jsonMedia + ", " + eventStream
	maxErrorDetail = 512
)

type remoteTransport struct {
	name       string
	address    string
	client     *http.Client
	credential string
	session    string
	version    string
}

// openURL prepares a fresh session with a remote server. Nothing is sent until
// the opening exchange, which is what gives the session its identifier, so
// every batch's session is new.
func openURL(_ context.Context, server Server, options Options, credential string) (transport, error) {
	address, err := url.Parse(strings.TrimSpace(server.Declared.URL))
	if err != nil || address.Scheme != "https" || address.Host == "" {
		return nil, fmt.Errorf("%w: mcp server %q has a url that is not an https address", ErrRefused, server.Name)
	}
	if address.User != nil {
		return nil, fmt.Errorf("%w: mcp server %q has a url carrying a user or password, and a credential is read only from the stores", ErrRefused, server.Name)
	}
	return &remoteTransport{
		name:       server.Name,
		address:    address.String(),
		client:     &http.Client{Transport: options.Transport, CheckRedirect: mcpcredential.CheckRedirect},
		credential: credential,
	}, nil
}

func (t *remoteTransport) settled(version string) { t.version = version }

func (t *remoteTransport) newRequest(ctx context.Context, method string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, t.address, reader)
	if err != nil {
		return nil, fmt.Errorf("%w: mcp server %q: %v", ErrFailed, t.name, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", jsonMedia)
	}
	request.Header.Set("Accept", acceptBoth)
	if t.credential != "" {
		request.Header.Set("Authorization", "Bearer "+t.credential)
	}
	if t.session != "" {
		request.Header.Set(sessionHeader, t.session)
	}
	if t.version != "" {
		request.Header.Set(versionHeader, t.version)
	}
	return request, nil
}

// post sends one message and hands back the response, refused where its
// status says the server did not take it.
func (t *remoteTransport) post(ctx context.Context, value any) (*http.Response, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	request, err := t.newRequest(ctx, http.MethodPost, encoded)
	if err != nil {
		return nil, err
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, t.sendFailure(ctx, err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound && t.session != "" {
			return nil, fmt.Errorf("%w: mcp server %q ended the session before the batch did", ErrFailed, t.name)
		}
		return nil, fmt.Errorf("%w: mcp server %q answered %s", ErrFailed, t.name, response.Status)
	}
	return response, nil
}

// sendFailure says why a request did not reach the server, keeping the
// context's own error where that is why.
func (t *remoteTransport) sendFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("mcp server %q did not answer in time: %w", t.name, ctx.Err())
	}
	if errors.Is(err, mcpcredential.ErrRedirectRefused) {
		return fmt.Errorf("%w: mcp server %q: %v", ErrFailed, t.name, errors.Unwrap(err))
	}
	return fmt.Errorf("%w: mcp server %q could not be reached: %v", ErrFailed, t.name, err)
}

func (t *remoteTransport) call(ctx context.Context, id int64, request map[string]any) (message, error) {
	response, err := t.post(ctx, request)
	if err != nil {
		return message{}, err
	}
	defer response.Body.Close()
	if t.session == "" {
		// The session identifier comes with the answer to the opening exchange,
		// and is held for this batch alone.
		t.session = strings.TrimSpace(response.Header.Get(sessionHeader))
	}
	body := io.LimitReader(response.Body, maxMessageBytes+1)
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	switch media {
	case jsonMedia:
		return t.fromJSON(body, id)
	case eventStream:
		return t.fromEvents(ctx, body, id)
	}
	return message{}, fmt.Errorf("%w: mcp server %q answered with %q, which is neither JSON nor an event stream", ErrFailed, t.name, media)
}

func (t *remoteTransport) fromJSON(body io.Reader, id int64) (message, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return message{}, fmt.Errorf("%w: mcp server %q: %v", ErrFailed, t.name, err)
	}
	if len(data) > maxMessageBytes {
		return message{}, fmt.Errorf("%w: mcp server %q answered with more than %d bytes", ErrFailed, t.name, maxMessageBytes)
	}
	var m message
	if err := json.Unmarshal(data, &m); err != nil || !m.answers(id) {
		return message{}, fmt.Errorf("%w: mcp server %q answered with something that is not the answer it was asked for", ErrFailed, t.name)
	}
	return m, nil
}

// fromEvents reads an answer's event stream until the answer itself arrives,
// refusing every request the server makes on the way.
func (t *remoteTransport) fromEvents(ctx context.Context, body io.Reader, id int64) (message, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxMessageBytes)
	var data []string
	dispatch := func() (message, bool, error) {
		if len(data) == 0 {
			return message{}, false, nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		var m message
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			return message{}, false, nil
		}
		switch {
		case m.answers(id):
			return m, true, nil
		case m.isRequest():
			if err := t.refuse(ctx, m.ID); err != nil {
				return message{}, false, err
			}
		}
		return message{}, false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if m, done, err := dispatch(); err != nil || done {
				return m, err
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if m, done, err := dispatch(); err != nil || done {
		return m, err
	}
	if ctx.Err() != nil {
		return message{}, fmt.Errorf("mcp server %q did not answer in time: %w", t.name, ctx.Err())
	}
	if err := scanner.Err(); err != nil {
		return message{}, fmt.Errorf("%w: mcp server %q's event stream failed: %v", ErrFailed, t.name, err)
	}
	return message{}, fmt.Errorf("%w: mcp server %q ended its event stream without answering", ErrFailed, t.name)
}

// refuse posts the refusal to a request the server made.
func (t *remoteTransport) refuse(ctx context.Context, id json.RawMessage) error {
	response, err := t.post(ctx, refusal(id))
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorDetail))
	return response.Body.Close()
}

func (t *remoteTransport) notify(ctx context.Context, method string, params any) error {
	response, err := t.post(ctx, notification(method, params))
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorDetail))
	return response.Body.Close()
}

// close ends the session, so the server holds nothing of this batch. A server
// that does not let a client end its sessions says so with 405, which is not
// a failure; nothing else it says changes that the batch is over, so close
// reports nothing either way.
func (t *remoteTransport) close() error {
	defer t.client.CloseIdleConnections()
	if t.session == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopGrace)
	defer cancel()
	request, err := t.newRequest(ctx, http.MethodDelete, nil)
	if err != nil {
		return nil
	}
	response, err := t.client.Do(request)
	if err == nil {
		response.Body.Close()
	}
	return nil
}
