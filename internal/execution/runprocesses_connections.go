package execution

import (
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

// Reading each process's live local connections and the process at each other
// end (runprocesses_inuse.go). The readers are split by operating system; what
// they read is parsed here, so both are tested on either.

// tcpOver is what a TCP connection is said to be made over.
func tcpOver(local, remote string) string {
	return "TCP between " + local + " and " + remote
}

// loopbackPair reports a connection both of whose ends are loopback addresses:
// one that never leaves the machine.
func loopbackPair(local, remote string) bool {
	return loopbackAddress(local) && loopbackAddress(remote)
}

func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// lsofSocket is one open socket as lsof's field output names it: the process,
// the kernel address of the socket (d), and its name (n).
type lsofSocket struct {
	PID    int
	Device string
	Name   string
}

// parseLsofSockets reads lsof's field output (-F pdn): a p line starts a
// process, an f line a file, and its d and n lines belong to that file.
func parseLsofSockets(output []byte) []lsofSocket {
	var sockets []lsofSocket
	pid := 0
	current := -1
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) < 1 {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(value)
			current = -1
		case 'f':
			sockets = append(sockets, lsofSocket{PID: pid})
			current = len(sockets) - 1
		case 'd':
			if current >= 0 {
				sockets[current].Device = value
			}
		case 'n':
			if current >= 0 {
				sockets[current].Name = value
			}
		}
	}
	return sockets
}

// lsofConnections is every live local connection in lsof's listing of
// established TCP sockets and of socket files, by process.
//
// A TCP socket is named "local->remote"; its other end is the socket named
// "remote->local". A socket file's socket is named "->0x..." after the kernel
// address of the socket at its other end, or "->(none)" once that end has
// closed, and a path alone on one with no peer the listing can name. The other
// end is found both ways: the socket whose address this one names, and any
// socket that names this one's address, because macOS names the peer from one
// side of an accepted connection only.
func lsofConnections(tcp, unixSockets []byte) map[int][]localConnection {
	connections := map[int][]localConnection{}

	tcpSockets := parseLsofSockets(tcp)
	byTuple := map[string][]int{}
	for _, socket := range tcpSockets {
		byTuple[socket.Name] = append(byTuple[socket.Name], socket.PID)
	}
	for _, socket := range tcpSockets {
		local, remote, found := strings.Cut(socket.Name, "->")
		if !found || !loopbackPair(local, remote) {
			continue
		}
		connections[socket.PID] = append(connections[socket.PID], localConnection{
			Over:  tcpOver(local, remote),
			Peers: byTuple[remote+"->"+local],
		})
	}

	sockets := parseLsofSockets(unixSockets)
	byAddress := map[string][]int{}
	pointingAt := map[string][]int{}
	for _, socket := range sockets {
		if socket.Device != "" {
			byAddress[socket.Device] = append(byAddress[socket.Device], socket.PID)
		}
		if peer := lsofPeerAddress(socket.Name); peer != "" {
			pointingAt[peer] = append(pointingAt[peer], socket.PID)
		}
	}
	for _, socket := range sockets {
		peer := lsofPeerAddress(socket.Name)
		var peers []int
		if peer != "" {
			peers = append(peers, byAddress[peer]...)
		}
		if socket.Device != "" {
			peers = append(peers, pointingAt[socket.Device]...)
		}
		if peer == "" && len(peers) == 0 {
			// Listening, or never connected: nothing is at the other end.
			continue
		}
		connections[socket.PID] = append(connections[socket.PID], localConnection{
			Over:  socketFileOver(strings.TrimSpace(strings.Split(socket.Name, "->")[0])),
			Peers: uniquePIDs(peers),
		})
	}
	return connections
}

// lsofPeerAddress is the kernel address a socket file's name says its other
// end is at, and nothing where it names none or that end has closed.
func lsofPeerAddress(name string) string {
	_, peer, found := strings.Cut(name, "->")
	peer = strings.TrimSpace(peer)
	if !found || peer == "" || peer == "(none)" {
		return ""
	}
	if field, _, cut := strings.Cut(peer, " "); cut {
		peer = field
	}
	return peer
}

// socketFileOver is what a connection over a socket file is said to be made
// over.
func socketFileOver(path string) string {
	if path == "" {
		return "a local socket"
	}
	return "the socket file " + path
}

func uniquePIDs(pids []int) []int {
	seen := map[int]bool{}
	kept := pids[:0:0]
	for _, pid := range pids {
		if !seen[pid] {
			seen[pid] = true
			kept = append(kept, pid)
		}
	}
	return kept
}

// procTCPSocket is one line of Linux's /proc/net/tcp or tcp6: its two ends,
// whether it is established, and the inode of the socket that holds it.
type procTCPSocket struct {
	Local, Remote string
	Established   bool
	Inode         uint64
}

// parseProcNetTCP reads /proc/net/tcp or tcp6. Each address is hexadecimal:
// the address as the kernel stores it, in 32-bit words of the machine's byte
// order (little-endian on every platform this runs on), then the port.
func parseProcNetTCP(content []byte) []procTCPSocket {
	var sockets []procTCPSocket
	for _, line := range strings.Split(string(content), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		local, okLocal := procAddress(fields[1])
		remote, okRemote := procAddress(fields[2])
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if !okLocal || !okRemote || err != nil {
			continue
		}
		sockets = append(sockets, procTCPSocket{Local: local, Remote: remote, Established: fields[3] == "01", Inode: inode})
	}
	return sockets
}

func procAddress(field string) (string, bool) {
	hexAddress, hexPort, found := strings.Cut(field, ":")
	if !found {
		return "", false
	}
	raw, err := hex.DecodeString(hexAddress)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", false
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return "", false
	}
	ip := make(net.IP, len(raw))
	for word := 0; word < len(raw); word += 4 {
		for index := 0; index < 4; index++ {
			ip[word+index] = raw[word+3-index]
		}
	}
	return net.JoinHostPort(ip.String(), strconv.FormatUint(port, 10)), true
}

// procConnections is every live local connection on Linux, by process, from the
// TCP sockets, the peer of each connected socket file (by inode), and which
// processes hold each socket inode.
func procConnections(tcp []procTCPSocket, unixPeers map[uint64]uint64, owners map[uint64][]int) map[int][]localConnection {
	connections := map[int][]localConnection{}
	byTuple := map[string]uint64{}
	for _, socket := range tcp {
		if socket.Established {
			byTuple[socket.Local+"->"+socket.Remote] = socket.Inode
		}
	}
	for _, socket := range tcp {
		if !socket.Established || socket.Inode == 0 || !loopbackPair(socket.Local, socket.Remote) {
			continue
		}
		var peers []int
		if peer := byTuple[socket.Remote+"->"+socket.Local]; peer != 0 {
			peers = owners[peer]
		}
		for _, pid := range uniquePIDs(owners[socket.Inode]) {
			connections[pid] = append(connections[pid], localConnection{Over: tcpOver(socket.Local, socket.Remote), Peers: uniquePIDs(peers)})
		}
	}
	for inode, peer := range unixPeers {
		var peers []int
		if peer != 0 {
			peers = owners[peer]
		}
		for _, pid := range uniquePIDs(owners[inode]) {
			connections[pid] = append(connections[pid], localConnection{Over: socketFileOver(""), Peers: uniquePIDs(peers)})
		}
	}
	return connections
}
