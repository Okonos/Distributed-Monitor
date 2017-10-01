package monitorInterface

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pborman/uuid"
	zmq "github.com/pebbe/zmq4"
)

// Monitor struct for handling communication with servers
type Monitor struct {
	id          string
	servers     *sync.Map
	requestSock *zmq.Socket // PAIR socket between main thread and connectionhandler
}

const (
	pingPortNumber = 9999
	serverExpiry   = 5000 * time.Millisecond
)

type beacon struct {
	UUID uuid.UUID // Node's uuid
	Port string    // Router's port
}

type clientRequest struct {
	Command  string
	Argument int
}

type clientResponse struct {
	Text  string
	Value int
}

// Init things
func Init() *Monitor {
	reqSock, err := zmq.NewSocket(zmq.PAIR)
	if err != nil {
		panic(err)
	}
	if err = reqSock.Bind("inproc://request"); err != nil {
		panic(err)
	}

	monitor := &Monitor{
		id:          uuid.NewRandom().String(),
		servers:     &sync.Map{},
		requestSock: reqSock,
	}
	fmt.Println("UUID:", monitor.id)

	go monitor.listenForServers()
	time.Sleep(3 * time.Second)
	go monitor.connectionHandler()
	time.Sleep(500 * time.Millisecond)

	return monitor
}

func (m *Monitor) listenForServers() {
	notifierSock, err := zmq.NewSocket(zmq.PAIR)
	if err != nil {
		panic(err)
	}
	if err = notifierSock.Bind("inproc://notify"); err != nil {
		panic(err)
	}

	bcast := &syscall.SockaddrInet4{
		Port: pingPortNumber,
		Addr: [4]byte{255, 255, 255, 255},
	}
	conn, e := listenUDP(bcast)
	if e != nil {
		panic(e)
	}
	buffer := make([]byte, 512)
	expiry := make(map[string]time.Time)

	for {
		if n, udpAddr, err := conn.ReadFrom(buffer); err == nil {
			var b beacon
			json.Unmarshal(buffer[:n], &b)
			ipAddr := strings.Split(udpAddr.String(), ":")[0]
			addr := "tcp://" + ipAddr + ":" + b.Port
			id := b.UUID.String()
			m.servers.Store(id, addr)
			expiry[id] = time.Now().Add(serverExpiry)

			checkExpiry := func(k, v interface{}) bool {
				id := k.(string)
				if time.Now().After(expiry[id]) {
					m.servers.Delete(k)
					delete(expiry, id)
					notifierSock.SendMessage(id)
				}
				return true
			}
			m.servers.Range(checkExpiry)
		}
	}
}

func (m *Monitor) connectionHandler() {
	notifierSock, err := zmq.NewSocket(zmq.PAIR)
	if err != nil {
		panic(err)
	}
	if err = notifierSock.Connect("inproc://notify"); err != nil {
		panic(err)
	}
	requestSock, err := zmq.NewSocket(zmq.PAIR)
	if err != nil {
		panic(err)
	}
	if err = requestSock.Connect("inproc://request"); err != nil {
		panic(err)
	}
	sock, err := zmq.NewSocket(zmq.DEALER)
	if err != nil {
		panic(err)
	}
	sock.SetIdentity(m.id)

	poller := zmq.NewPoller()
	poller.Add(notifierSock, zmq.POLLIN)
	poller.Add(requestSock, zmq.POLLIN)
	poller.Add(sock, zmq.POLLIN)

	var (
		requestBytes  []byte
		leaderID      string
		serverAddr    string
		connected     bool
		requestQueued bool
	)
	replyReceived := true
	disconnectAndUpdateLeader := func(serverID string) {
		sock.Disconnect(serverAddr)
		connected = false
		if serverID != "" {
			leaderID = serverID
			if addr, ok := m.servers.Load(leaderID); ok {
				serverAddr = addr.(string)
				return
			}
		}
		serverAddr = ""
	}

	for {
		if !connected {
			for serverAddr == "" {
				time.Sleep(100 * time.Millisecond)
				loadAny := func(id, addr interface{}) bool {
					leaderID = id.(string)
					serverAddr = addr.(string)
					return false
				}
				m.servers.Range(loadAny)
			}

			if err := sock.Connect(serverAddr); err != nil {
				panic(err)
			}
			time.Sleep(100 * time.Millisecond)
			connected = true
			fmt.Println("Connected to", leaderID, serverAddr)

			// retry request if not sent or reply not received
			if !replyReceived || requestQueued {
				sock.SendMessage("CREQ", requestBytes)
				if requestQueued {
					requestQueued = false
					replyReceived = false
				}
			}
		}

		sockets, err := poller.Poll(-1)
		if err != nil {
			panic(err)
		}

		for _, socket := range sockets {
			switch s := socket.Socket; s {
			case notifierSock: // when server leaves/dies
				msg, err := notifierSock.RecvMessage(0)
				if err != nil {
					panic(err)
				}
				leaverID := msg[0]
				if leaverID == leaderID {
					disconnectAndUpdateLeader("")
					fmt.Println("Leader died, attempting reconnect")
				}

			case requestSock: // request from application
				msg, err := requestSock.RecvMessage(0)
				if err != nil {
					panic(err)
				}
				cmd := msg[0]
				request := clientRequest{Command: cmd}
				if cmd == "PUT" {
					argument, _ := strconv.Atoi(msg[1])
					request.Argument = argument
				}
				requestBytes, err = json.Marshal(request)
				if err != nil {
					panic(err)
				}
				if connected {
					sock.SendMessage("CREQ", requestBytes)
					replyReceived = false
				} else {
					requestQueued = true
				}

			case sock: // message from server
				msg, err := sock.RecvMessageBytes(0)
				if err != nil {
					panic(err)
				}
				serverID := string(msg[0])
				var response clientResponse
				if err = json.Unmarshal(msg[1], &response); err != nil {
					panic(err)
				}

				switch response.Text {
				case "RETRY":
					time.Sleep(100 * time.Millisecond)
					sock.SendMessage("CREQ", requestBytes)
				case "GET", "PUT":
					replyReceived = true
					if serverID != leaderID { // new leader; reconnect
						disconnectAndUpdateLeader(serverID)
					}
					requestSock.SendMessage(strconv.Itoa(response.Value))
				default: // redirect, response.Text == uuid
					disconnectAndUpdateLeader(response.Text)
					fmt.Println("Being redirected to", leaderID[:8], serverAddr)
				}
			}
		}
	}
}

// Get sends get request
func (m *Monitor) Get() int {
	m.requestSock.SendMessage("GET")
	reply, err := m.requestSock.RecvMessage(0)
	if err != nil {
		panic(err)
	}
	value, _ := strconv.Atoi(reply[0])
	return value
}

// Put sends put request
func (m *Monitor) Put(value int) int {
	m.requestSock.SendMessage("PUT", value)
	_, err := m.requestSock.RecvMessage(0)
	if err != nil {
		panic(err)
	}
	return 0
}

// GetID returns monitor's ID
func (m *Monitor) GetID() string {
	return m.id
}
