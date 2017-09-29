package monitorInterface

import (
	"log"
	"net"
	"os"
	"syscall"
)

// Creates and binds UDP socket with BROADCAST and REUSEADDR options set
func listenUDP(addr *syscall.SockaddrInet4) (net.PacketConn, error) {
	s, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	ck(err)
	err = syscall.SetsockoptInt(s, syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	ck(err)
	err = syscall.SetsockoptInt(s, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	ck(err)
	err = syscall.Bind(s, addr)
	ck(err)

	f := os.NewFile(uintptr(s), "socket")
	return net.FilePacketConn(f)
}

func ck(err error) {
	if err != nil {
		log.Fatalln(err)
	}
}
