package execution

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// connections reads Linux's TCP sockets from /proc/net/tcp and tcp6, the peer
// of every connected socket file from the kernel's socket diagnostics, and
// which process holds each socket from every process's fd directory.
func (systemProcessTable) connections() (map[int][]localConnection, error) {
	var tcp []procTCPSocket
	for _, name := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		content, err := os.ReadFile(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s could not be read: %w", name, err)
		}
		tcp = append(tcp, parseProcNetTCP(content)...)
	}
	peers, err := linuxUnixPeers()
	if err != nil {
		return nil, fmt.Errorf("the peers of socket files could not be read: %w", err)
	}
	return procConnections(tcp, peers, linuxSocketOwners()), nil
}

// linuxSocketOwners is which processes hold each socket inode, from every
// readable process's fd directory, whose links name a socket "socket:[N]".
func linuxSocketOwners() map[uint64][]int {
	owners := map[uint64][]int{}
	names, err := os.ReadDir("/proc")
	if err != nil {
		return owners
	}
	for _, name := range names {
		pid, err := strconv.Atoi(name.Name())
		if err != nil {
			continue
		}
		descriptors := filepath.Join("/proc", name.Name(), "fd")
		open, err := os.ReadDir(descriptors)
		if err != nil {
			continue
		}
		for _, descriptor := range open {
			target, err := os.Readlink(filepath.Join(descriptors, descriptor.Name()))
			if err != nil {
				continue
			}
			number, found := strings.CutPrefix(target, "socket:[")
			if !found {
				continue
			}
			if inode, err := strconv.ParseUint(strings.TrimSuffix(number, "]"), 10, 64); err == nil {
				owners[inode] = append(owners[inode], pid)
			}
		}
	}
	return owners
}

// The kernel's socket diagnostics for socket files (linux/unix_diag.h).
const (
	unixDiagShowPeer   = 0x4 // UDIAG_SHOW_PEER
	unixDiagPeer       = 2   // UNIX_DIAG_PEER
	unixEstablished    = 1   // TCP_ESTABLISHED, which socket files share
	unixDiagRequestLen = 24  // struct unix_diag_req
	unixDiagMessageLen = 16  // struct unix_diag_msg
)

// linuxUnixPeers is the inode of the socket at the other end of every
// connected socket file, by the inode of this end, asked of the kernel over a
// netlink socket. /proc/net/unix names no peer.
func linuxUnixPeers() (map[uint64]uint64, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 5}); err != nil {
		return nil, err
	}
	request := make([]byte, unix.SizeofNlMsghdr+unixDiagRequestLen)
	order := binary.NativeEndian
	order.PutUint32(request[0:], uint32(len(request)))
	order.PutUint16(request[4:], unix.SOCK_DIAG_BY_FAMILY)
	order.PutUint16(request[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	order.PutUint32(request[8:], 1)
	body := request[unix.SizeofNlMsghdr:]
	body[0] = unix.AF_UNIX
	order.PutUint32(body[4:], 1<<unixEstablished)
	order.PutUint32(body[12:], unixDiagShowPeer)
	if err := unix.Sendto(fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, err
	}
	peers := map[uint64]uint64{}
	buffer := make([]byte, 1<<16)
	for {
		n, _, err := unix.Recvfrom(fd, buffer, 0)
		if err != nil {
			return nil, err
		}
		messages, err := syscall.ParseNetlinkMessage(buffer[:n])
		if err != nil {
			return nil, err
		}
		for _, message := range messages {
			switch message.Header.Type {
			case unix.NLMSG_DONE:
				return peers, nil
			case unix.NLMSG_ERROR:
				if len(message.Data) >= 4 {
					if code := int32(order.Uint32(message.Data[:4])); code != 0 {
						return nil, syscall.Errno(-code)
					}
				}
				return nil, errors.New("the kernel refused the request")
			}
			if len(message.Data) < unixDiagMessageLen {
				continue
			}
			inode := uint64(order.Uint32(message.Data[4:8]))
			peers[inode] = 0
			for attributes := message.Data[unixDiagMessageLen:]; len(attributes) >= 4; {
				length := int(order.Uint16(attributes[0:2]))
				if length < 4 || length > len(attributes) {
					break
				}
				if order.Uint16(attributes[2:4]) == unixDiagPeer && length >= 8 {
					peers[inode] = uint64(order.Uint32(attributes[4:8]))
				}
				aligned := (length + 3) &^ 3
				if aligned > len(attributes) {
					break
				}
				attributes = attributes[aligned:]
			}
		}
	}
}
