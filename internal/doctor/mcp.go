package doctor

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
)

// checkMCPServers reports each declared MCP server whose credential or login is
// not on this machine, naming the server and the one command that fixes it.
// A missing credential is a warning rather than a problem: calls to that server
// are refused until it is there, and nothing else stops. A project that
// declares no server is reported nothing, so its diagnosis reads exactly as it
// did before servers could be declared.
//
// The keychain is asked whether the item is there and never for its value, and
// a file credential is checked by the reader that will read it, so what this
// says is missing and what a call is refused for are one answer.
func (d *diagnosis) checkMCPServers(ctx context.Context, resolved config.Resolved) []Finding {
	servers := resolved.Config.MCP.Servers
	if len(servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	stateRoot, rootErr := d.stateRootPath()
	findings := make([]Finding, 0, len(names))
	for _, name := range names {
		findings = append(findings, d.checkMCPServer(ctx, resolved, name, servers[name], stateRoot, rootErr))
	}
	return findings
}

func (d *diagnosis) checkMCPServer(ctx context.Context, resolved config.Resolved, name string, declared config.MCPServer, stateRoot string, rootErr error) Finding {
	check := "mcp:" + name
	productID := resolved.Config.Product.ID
	if rootErr != nil && (declared.Credential == config.MCPCredentialFile || len(declared.Login) > 0) {
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: fmt.Sprintf("the state directory MCP server %s keeps its credential or login under could not be found, so calls to it are refused", name),
			Detail:  rootErr.Error(),
			Remedy:  "export YOYODYNE_STATE_HOME=$HOME/.local/state/yoyodyne",
		}
	}
	remedy := mcpcredential.Remedy(stateRoot, productID, name, declared)
	switch declared.Credential {
	case config.MCPCredentialKeychain:
		item := mcpcredential.KeychainItem(productID, name)
		if d.env.GOOS != "darwin" {
			return Finding{
				Check:   check,
				Status:  StatusWarning,
				Summary: fmt.Sprintf("MCP server %s keeps its credential in the keychain, which this platform does not have, so calls to it are refused", name),
				Detail:  fmt.Sprintf("mcp.servers.%s.credential is %q; %q, a file under the state directory, is the store this platform has", name, config.MCPCredentialKeychain, config.MCPCredentialFile),
				Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(resolved.Path)),
			}
		}
		if missing := d.missingKeychainSecrets(ctx, item); len(missing) > 0 {
			return Finding{
				Check:   check,
				Status:  StatusWarning,
				Summary: fmt.Sprintf("MCP server %s has no credential in the keychain as %s, so calls to it are refused until a person stores it", name, item),
				Detail:  fmt.Sprintf("the item is read under the account %s, and carries the product so a sibling project's cannot stand in for it", mcpcredential.KeychainAccount),
				Remedy:  remedy,
			}
		}
		return Finding{Check: check, Status: StatusOK, Summary: fmt.Sprintf("MCP server %s has its credential in the keychain as %s", name, item)}
	case config.MCPCredentialFile:
		stores := mcpcredential.Stores{StateRoot: stateRoot, Product: productID}
		if _, err := stores.Read(ctx, name, declared); err != nil {
			return Finding{
				Check:   check,
				Status:  StatusWarning,
				Summary: fmt.Sprintf("MCP server %s has no usable credential file, so calls to it are refused until a person stores it", name),
				Detail:  err.Error(),
				Remedy:  remedy,
			}
		}
		return Finding{
			Check:   check,
			Status:  StatusOK,
			Summary: fmt.Sprintf("MCP server %s has its credential stored", name),
			Detail:  mcpcredential.File(stateRoot, productID, name),
		}
	}
	if len(declared.Login) > 0 {
		directory := mcpcredential.ServerDirectory(stateRoot, productID, name)
		if info, err := os.Lstat(directory); err != nil || !info.IsDir() {
			return Finding{
				Check:   check,
				Status:  StatusWarning,
				Summary: fmt.Sprintf("MCP server %s has never been logged in on this machine, so its calls may be refused", name),
				Detail:  fmt.Sprintf("its login keeps its files in %s, which is not there", directory),
				Remedy:  remedy,
			}
		}
		return Finding{Check: check, Status: StatusOK, Summary: fmt.Sprintf("MCP server %s has been logged in on this machine", name), Detail: directory}
	}
	return Finding{Check: check, Status: StatusOK, Summary: fmt.Sprintf("MCP server %s needs no credential", name)}
}
