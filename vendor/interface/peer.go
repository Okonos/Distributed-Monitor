package intface

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	zmq "github.com/pebbe/zmq4"
)

const (
	peerExpiry = 5000 * time.Millisecond
)

// Peer : Defines each peer that we discover and track
type Peer struct {
	hello     bool        // HELLO message received from peer
	connected bool        // Determines if the socket is connected
	endpoint  string      // Endpoint of the dealer; used in disconnect
	dealer    *zmq.Socket // Connection to peer using DEALER socket
	expiresAt time.Time
}

// Constructor for the peer class
func newPeer(agent *Agent, peerAddr string) (peer *Peer) {
	dealer, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	// Set high-water mark and make socket nonblocking
	dealer.SetSndhwm(1000 * int(peerExpiry.Seconds()))
	dealer.SetSndtimeo(0)
	if err := dealer.SetIdentity(agent.uuidString); err != nil {
		panic(err)
	}
	fmt.Println("CONNECT:", peerAddr)
	if err := dealer.Connect(peerAddr); err != nil {
		panic(err)
	}
	endpoint, _ := dealer.GetLastEndpoint()

	// Send HELLO message
	go func() {
		time.Sleep(500 * time.Millisecond)
		localIP := endpoint[:strings.LastIndex(endpoint, ":")]
		localEndpoint := localIP + ":" + agent.port
		fmt.Println("Sending HELLO to", peerAddr, "with endpoint", localEndpoint)
		if _, err := dealer.SendMessage("HELLO", localEndpoint); err != nil {
			panic(err)
		}
	}()

	peer = &Peer{
		connected: true,
		endpoint:  endpoint,
		dealer:    dealer,
	}
	return
}

func (peer *Peer) send(args []string) (n int, err error) {
	if peer.connected {
		n, err = peer.dealer.SendMessage(args)
		if err == zmq.AsErrno(syscall.EAGAIN) {
			fmt.Println("Unexpected err: High-water mark reached")
			peer.disconnect()
		}
	}
	return
}

func (peer *Peer) disconnect() {
	if peer.connected {
		peer.dealer.Disconnect(peer.endpoint)
		peer.endpoint = ""
		peer.connected = false
	}
}

// Resets peers expiry time; called whenever we get any activity from a peer
func (peer *Peer) isAlive() {
	peer.expiresAt = time.Now().Add(peerExpiry)
}
