package intface

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/pborman/uuid"
	zmq "github.com/pebbe/zmq4"
)

// =====================================================================
// Synchronous part, works in application thread

// MessagingInterface : an excuse to start the background thread
// also a wrapper around send and recv
type MessagingInterface struct {
	pipe       *zmq.Socket // Pipe through to agent
	routerPipe *zmq.Socket // Pipe to router
}

// ErrEAGAIN used in Recv()
var ErrEAGAIN = errors.New("resource temporarily unavailable")

// New : Constructor for the interface class
func New(uuid uuid.UUID) (iface *MessagingInterface) {
	iface = &MessagingInterface{}
	var err error
	iface.pipe, err = zmq.NewSocket(zmq.PAIR)
	if err != nil {
		panic(err)
	}
	if err = iface.pipe.Bind("inproc://iface"); err != nil {
		panic(err)
	}
	iface.routerPipe, err = zmq.NewSocket(zmq.PAIR)
	if err != nil {
		panic(err)
	}
	if err = iface.routerPipe.Bind("inproc://router"); err != nil {
		panic(err)
	}
	go iface.agent(uuid)
	time.Sleep(100 * time.Millisecond)
	return
}

// Send : send a message to backend; args are optional (e.g. recipient's id)
func (iface *MessagingInterface) Send(cmd string, msg interface{}, rcvrID string) {
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR in iface.Send marshal:", err)
		return
	}

	var pipe *zmq.Socket
	if cmd == "CREP" {
		pipe = iface.routerPipe
	} else {
		pipe = iface.pipe
	}

	if _, err := pipe.SendMessage(rcvrID, cmd, msgBytes); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR in iface.pipe.SendMessage:", err)
	}
}

// Recv : wait for a message from the interface
func (iface *MessagingInterface) Recv() (senderID string, msgType string,
	msgBytes []byte, err error) {
	var msg [][]byte
	msg, err = iface.pipe.RecvMessageBytes(zmq.DONTWAIT)
	if err != nil {
		if err == zmq.AsErrno(syscall.EAGAIN) {
			err = ErrEAGAIN
		}
	} else {
		senderID = string(msg[0])
		msgType = string(msg[1])
		if len(msg) > 2 { // exclude JOINED/LEFT control messages
			msgBytes = msg[2]
		}
	}
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
	pipe       *zmq.Socket        // Pipe back to application
	udp        *zmq.Socket        // Pipe to goroutine listening for beacons
	conn       net.PacketConn     // UDP socket for discovery
	router     *zmq.Socket        // Router socket for receiving messages
	port       string             // Port the router is bound to
	peers      *concurrentPeerMap // Hash of known peers, fast lookup
	uuidBytes  []byte             // This node's UUID
	uuidString string
}

// Each interface has one agent object, which implements its background thread
func newAgent(uuid uuid.UUID) *Agent {
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

	fmt.Println("UUID:", uuid)
	agent := &Agent{
		pipe:       pipe,
		udp:        udp,
		conn:       conn,
		router:     router,
		port:       port,
		peers:      newConcurrentPeerMap(),
		uuidBytes:  []byte(uuid),
		uuidString: uuid.String(),
	}

	go agent.routerLoop()

	return agent
}

func (agent *Agent) routerLoop() {
	pipe, err := zmq.NewSocket(zmq.PAIR)
	if err != nil {
		panic(err)
	}
	if err := pipe.Connect("inproc://router"); err != nil {
		panic(err)
	}
	poller := zmq.NewPoller()
	poller.Add(agent.router, zmq.POLLIN)
	poller.Add(pipe, zmq.POLLIN)

	for {
		sockets, err := poller.Poll(-1)
		if err != nil {
			log.Println("Router poll error", err)
			panic(err)
		}

		var msg [][]byte
		for _, socket := range sockets {
			switch s := socket.Socket; s {
			case agent.router:
				msg, err = agent.router.RecvMessageBytes(0)

			case pipe:
				// clientResponse received from front-end, handle it
				msg, err = pipe.RecvMessageBytes(0)
			}

			if err != nil {
				fmt.Fprintln(os.Stderr, "Router recv err", err)
				continue
			}

			// DEBUG fmt.Printf("router %T, %s, %d\n", msg, msg, len(msg))

			id := string(msg[0])
			header := string(msg[1])
			switch header {
			case "CREQ": // Client Request, pass to main thread
				agent.pipe.SendMessage(msg)

			case "CREP": // Reply for client received from main thread
				// msgType not needed, it's always clientResponse
				agent.router.SendMessage(id, msg[2])

			default:
				peer, ok := agent.peers.get(id)
				// Discard messages until HELLO is received
				if header != "HELLO" && (!ok || !peer.hello) {
					continue
				}

				if header == "HELLO" {
					fmt.Println("HELLO received from", id)
					if !ok {
						uuidBytes := uuid.Parse(id)
						peerAddr := string(msg[2])
						peer = agent.createPeer(uuidBytes, peerAddr)
					}
					peer.hello = true
					peer.isAlive()
					continue
				}

				peer.isAlive()
				// else send to server thread
				agent.pipe.SendMessage(msg)
			}
		}
	}
}

func (agent *Agent) createPeer(uuid uuid.UUID, peerAddr string) (peer *Peer) {
	peer = newPeer(agent, peerAddr)
	agent.peers.insert(uuid.String(), peer)
	return
}

// Handle different control messages from the front-end
func (agent *Agent) controlMessage() (err error) {
	// Get the whole message off the pipe in one go
	// [recipient ID, type, msg struct]
	msg, e := agent.pipe.RecvMessageBytes(0)
	if e != nil {
		return e
	}

	peerID := string(msg[0])
	peer, ok := agent.peers.get(peerID)
	if !ok {
		text := fmt.Sprintf("RVR err: peer (%s) not found\n", peerID)
		return errors.New(text)
	}
	peer.send(msg[1:])

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
		peer, ok := agent.peers.get(uuidString)
		if !ok {
			fmt.Println("BEACON:", peerAddr)
			peer = agent.createPeer(uuid, peerAddr)
			// Report peer joined the network
			agent.pipe.SendMessage(uuid, "JOINED")
		}
		// Any activity from the peer means it's alive
		peer.isAlive()
	}
	return
}

// Main loop for the background agent.
// zmq_poll monitors the front-end pipe (commands from the API)
// and the back-end UDP handle (beacons):
func (iface *MessagingInterface) agent(uuid uuid.UUID) {
	agent := newAgent(uuid)

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
				if err := agent.controlMessage(); err != nil {
					fmt.Fprintln(os.Stderr, "agent.controlMessage err:", err)
				}

			case agent.udp:
				// If we had input on the UDP socket, go process that
				if err := agent.handleBeacon(); err != nil {
					fmt.Fprintln(os.Stderr, "agent.handleBeacon err:", err)
				}
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
		peers := agent.peers.lockAndGetReference()
		for id, peer := range peers {
			if time.Now().After(peer.expiresAt) {
				// Report peer left the network
				agent.pipe.SendMessage(id, "LEFT")
				delete(peers, id)
			}
		}
		agent.peers.unlock()
	}
}
