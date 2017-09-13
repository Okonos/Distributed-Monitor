package main

import (
	"fmt"
	"log"
	"strings"
	"syscall"
	"time"

	"interface"

	"github.com/pborman/uuid"
	zmq "github.com/pebbe/zmq4"
)

// Server states
type serverState int

const (
	follower serverState = iota
	candidate
	leader
)

type server struct {
	currentTerm int
	state       serverState
	votedFor    uuid.UUID // the candidate the server voted in the current term
}

func initServer() *server {
	return &server{
		currentTerm: 1,
		state:       follower,
		votedFor:    nil,
	}
}

type entryLog struct {
	entries     []string
	commitIndex int // the index of the latest entry the state machine may apply
}

func main() {
	log.SetFlags(log.Lshortfile)
	iface := intface.New()
	bcastAt := time.Now().Add(2 * time.Second)

	for {
		now := time.Now()
		if now.After(bcastAt) {
			iface.Send("BROADCAST", "czesc")
			bcastAt = now.Add(2 * time.Second)
		}

		msg, err := iface.Recv() // non-blocking
		if err != nil {
			if err == zmq.AsErrno(syscall.EAGAIN) {
				continue
			} else {
				log.Fatalln(err)
			}
		}
		fmt.Println(strings.Join(msg, " "))
	}
}
