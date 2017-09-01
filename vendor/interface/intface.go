package intface

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/pborman/uuid"
	zmq "github.com/pebbe/zmq4"
)

// =====================================================================
// Synchronous part, works in application thread

// Intface : an excuse to start the background thread, and a wrapper around recv
type Intface struct {
	pipe *zmq.Socket // Pipe through to agent
}

// New : Constructor for the interface class
func New() (iface *Intface) {
	iface = &Intface{}
	var err error
	iface.pipe, err = zmq.NewSocket(zmq.PAIR)
	if err != nil {
		panic(err)
	}
	err = iface.pipe.Bind("inproc://iface")
	if err != nil {
		panic(err)
	}
	go iface.agent()
	time.Sleep(100 * time.Millisecond)
	return
}

// Recv : wait for a message from the interface
func (iface *Intface) Recv() (msg []string, err error) {
	msg, err = iface.pipe.RecvMessage(0)
	return
}

// =====================================================================
// Asynchronous part, works in the background

const (
	pingPortNumber = 9999
	pingInterval   = 1000 * time.Millisecond // Once per second
)

// beacon : broadcasted by agent
type beacon struct {
	UUID uuid.UUID // Node's uuid
	Port string    // Router's port
}

// Agent : Background's context
type Agent struct {
	pipe       *zmq.Socket      // Pipe back to application
	udp        *zmq.Socket      // Pipe to goroutine listening for beacons
	conn       net.PacketConn   // UDP socket for discovery
	router     *zmq.Socket      // Router socket for receiving messages
	port       string           // Port the router is bound to
	peers      map[string]*Peer // Hash of known peers, fast lookup
	uuidBytes  []byte           // This node UUID
	uuidString string
}

// Each interface has one agent object, which implements its background thread
func newAgent() *Agent {
	rand.Seed(time.Now().UnixNano())

	bcast := &syscall.SockaddrInet4{
		Port: pingPortNumber,
		Addr: [4]byte{255, 255, 255, 255},
	}
	conn, e := listenUDP(bcast)
	if e != nil {
		panic(e)
	}
	go func() {
		buffer := make([]byte, 1024)
		udp, _ := zmq.NewSocket(zmq.PAIR)
		udp.Bind("inproc://udp")
		for {
			if n, udpAddr, err := conn.ReadFrom(buffer); err == nil {
				var b beacon
				json.Unmarshal(buffer[:n], &b)
				ipAddr := strings.Split(udpAddr.String(), ":")[0]
				addr := "tcp://" + ipAddr + ":" + b.Port
				udp.SendMessage(b.UUID, addr)
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)

	pipe, _ := zmq.NewSocket(zmq.PAIR)
	pipe.Connect("inproc://iface")
	udp, _ := zmq.NewSocket(zmq.PAIR)
	udp.Connect("inproc://udp")

	router, err := zmq.NewSocket(zmq.ROUTER)
	if err != nil {
		panic(err)
	}
	if err := router.Bind("tcp://*:*"); err != nil {
		panic(err)
	}
	endpoint, _ := router.GetLastEndpoint()
	port := strings.Split(endpoint, ":")[2]
	fmt.Println("PORT:", port)

	uuid := uuid.NewRandom()
	fmt.Println("UUID:", uuid)
	agent := &Agent{
		pipe:       pipe,
		udp:        udp,
		conn:       conn,
		router:     router,
		port:       port,
		peers:      make(map[string]*Peer),
		uuidBytes:  []byte(uuid),
		uuidString: uuid.String(),
	}

	go agent.routerLoop()

	return agent

}

func (agent *Agent) routerLoop() {
	for {
		msg, err := agent.router.RecvMessage(0)
		if err != nil {
			fmt.Println("ROUTER ERR:", err)
		}

		id := msg[0]
		header := msg[1]
		peer, ok := agent.peers[id]

		// Discard messages until HELLO is received
		if header != "HELLO" && (!ok || !peer.hello) {
			continue
		}

		switch header {
		case "HELLO":
			fmt.Println("HELLO received from", id)
			if !ok {
				uuidBytes := uuid.Parse(id)
				peerAddr := msg[2]
				peer = agent.createPeer(uuidBytes, peerAddr)
			}
			peer.hello = true
		}

		peer.isAlive()
	}
}

func (agent *Agent) createPeer(uuid uuid.UUID, peerAddr string) (peer *Peer) {
	peer = newPeer(agent, uuid, peerAddr)
	agent.peers[uuid.String()] = peer

	// Report peer joined the network
	agent.pipe.SendMessage("JOINED", uuid)

	return
}

// Handle different control messages from the front-end
func (agent *Agent) controlMessage() (err error) {
	// Get the whole message off the pipe in one go
	msg, e := agent.pipe.RecvMessage(0)
	if e != nil {
		return e
	}
	command := msg[0]

	// No control commands implemented yet
	switch command {
	case "EXAMPLE":
	default:
	}

	return
}

// Handle a beacon coming into our UDP socket
func (agent *Agent) handleBeacon() (err error) {
	// [uuid, addr]
	msg, err := agent.udp.RecvMessage(0)
	uuid := uuid.Parse(msg[0])
	if uuid == nil || len(msg) != 2 || len(uuid) != 16 {
		fmt.Println("not a beacon")
		return errors.New("Not a beacon")
	}

	// If we got a UUID and it's not our own beacon, we have a peer
	peerAddr := msg[1]
	if bytes.Compare(uuid, agent.uuidBytes) != 0 {
		// Find or create peer via its UUID string
		uuidString := uuid.String()
		peer, ok := agent.peers[uuidString]
		if !ok {
			fmt.Println("BEACON:", peerAddr)
			peer = agent.createPeer(uuid, peerAddr)
		}
		// Any activity from the peer means it's alive
		peer.isAlive()
	}
	return
}

// Main loop for the background agent.
// zmq_poll monitors the front-end pipe (commands from the API)
// and the back-end UDP handle (beacons):
func (iface *Intface) agent() {
	agent := newAgent()

	// Send first beacon immediately
	pingAt := time.Now()

	poller := zmq.NewPoller()
	poller.Add(agent.pipe, zmq.POLLIN)
	poller.Add(agent.udp, zmq.POLLIN)

	bcast := &net.UDPAddr{Port: pingPortNumber, IP: net.IPv4bcast}
	for {
		timeout := pingAt.Add(time.Millisecond).Sub(time.Now())
		if timeout < 0 {
			timeout = 0
		}
		polled, err := poller.Poll(timeout)
		if err != nil {
			log.Println("Poll error: ", err)
			break
		}

		for _, item := range polled {
			switch socket := item.Socket; socket {
			case agent.pipe:
				// If we had activity on the pipe, go handle the control
				// message. Current code never sends control messages.
				agent.controlMessage()

			case agent.udp:
				// If we had input on the UDP socket, go process that
				agent.handleBeacon()
			}
		}

		// If the 1-second mark passed, broadcast our beacon
		now := time.Now()
		if now.After(pingAt) {
			b := beacon{agent.uuidBytes, agent.port}
			bytes, _ := json.Marshal(b)
			agent.conn.WriteTo(bytes, bcast)
			pingAt = now.Add(pingInterval)
		}
		// Delete and report any expired peers
		for _, peer := range agent.peers {
			if time.Now().After(peer.expiresAt) {
				// Report peer left the network
				agent.pipe.SendMessage("LEFT", peer.uuidString)
				delete(agent.peers, peer.uuidString)
			}
		}
	}
}
