package cli

import (
	"runtime"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// runMCPCalls is the one MCP call path a run's developer asks through and the
// record that path writes, which the review reads back; both are nil for a
// project that declares no MCP server: such a project's runs tell no developer
// of the block, read every reply as they always have, and show the reviewer
// nothing about MCP. The record is built once and is the same one the path
// writes to. The path's record lives under the state root, its credentials come
// from this machine's stores (the keychain on macOS, the file under the state
// root everywhere), and the operator's pause is the same one every run reads.
//
// The two are returned as interfaces, each nil as a value, so that no caller
// holds a typed nil pointer that compares as set.
func runMCPCalls(parts components) (orchestrator.MCPCalls, orchestrator.MCPCallRecords) {
	cfg := parts.config
	if len(cfg.MCP.Servers) == 0 || parts.holds == nil {
		return nil, nil
	}
	records, err := mcpcall.NewRecords(parts.stateRoot, cfg.Product.ID)
	if err != nil {
		// A record that cannot be kept is a call that cannot be made, so no
		// developer is offered the block rather than offered one every call of
		// which would be refused.
		return nil, nil
	}
	stores := mcpcredential.Stores{StateRoot: parts.stateRoot, Product: cfg.Product.ID}
	if runtime.GOOS == "darwin" {
		stores.Keychain = slack.Keychain{Runner: execution.OSProcessRunner{}}
	}
	return &mcpcall.Path{
		Config:        cfg,
		Stores:        stores,
		Hold:          parts.holds,
		Records:       records,
		RedactValues:  parts.redactValues,
		ClientVersion: buildinfo.Commit(),
	}, records
}
