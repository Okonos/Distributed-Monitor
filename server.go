package main

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"time"

	"interface"

	"github.com/pborman/uuid"
)

// Server states
type serverState int

func (state serverState) String() (s string) {
	switch state {
	case follower:
		s = "follower"
	case candidate:
		s = "candidate"
	case leader:
		s = "leader"
	}
	return
}

const (
	follower serverState = iota
	candidate
	leader
)

type candidateVars struct {
	votesResponded map[string]bool // imitates set
	votesGranted   map[string]bool
}

func newCandidateVars() *candidateVars {
	return &candidateVars{
		votesResponded: make(map[string]bool),
		votesGranted:   make(map[string]bool),
	}
}

func (cv *candidateVars) reset() {
	for k := range cv.votesResponded {
		delete(cv.votesResponded, k)
	}
	for k := range cv.votesGranted {
		delete(cv.votesGranted, k)
	}
}

type leaderVars struct {
	nextIndex  map[string]int
	matchIndex map[string]int
}

func newLeaderVars() *leaderVars {
	return &leaderVars{
		nextIndex:  make(map[string]int),
		matchIndex: make(map[string]int),
	}
}

func (lv *leaderVars) reset(servers []string, lastLogIndex int) {
	for _, id := range servers {
		lv.nextIndex[id] = lastLogIndex + 1
		lv.matchIndex[id] = 0
	}
}

type server struct {
	uuid        uuid.UUID
	iface       *intface.MessagingInterface
	elog        *entryLog
	currentTerm int
	votedFor    uuid.UUID // the candidate the server voted for in the current term
	state       serverState
	servers     map[string]bool // set of other servers (len has to be incremented)
	cVars       *candidateVars
	lVars       *leaderVars
}

func newServer() *server {
	uuid := uuid.NewRandom()
	return &server{
		uuid:        uuid,
		iface:       intface.New(uuid),
		elog:        newLog(),
		currentTerm: 0,
		votedFor:    nil,
		state:       follower,
		servers:     make(map[string]bool),
		cVars:       newCandidateVars(),
		lVars:       newLeaderVars(),
	}
}

func (s *server) getServersIDs() []string {
	i := 0
	ids := make([]string, len(s.servers))
	for id := range s.servers {
		ids[i] = id
		i++
	}
	return ids
}

func (s *server) getNumberOfServers() int {
	// this one + all the others that this one knows of
	return len(s.servers) + 1
}

func (s *server) handleControlMessage(msg []string) {
	serverID := msg[1]
	switch msg[0] {
	case "JOINED":
		s.servers[serverID] = true
		if s.state == leader {
			s.lVars.nextIndex[serverID] = s.elog.lastIndex()
			s.lVars.matchIndex[serverID] = 0
		}
	case "LEFT":
		delete(s.servers, serverID)
		if s.state == leader {
			delete(s.lVars.nextIndex, serverID)
			delete(s.lVars.matchIndex, serverID)
		}
	}
}

func (s *server) requestVote() {
	// bez uuid, bo przesyłany przez socket
	s.iface.Send("RV",
		strconv.Itoa(s.currentTerm),
		strconv.Itoa(s.elog.lastIndex()),
		strconv.Itoa(s.elog.lastTerm()))
}

func (s *server) handleRequestVote(msg []string) {
	currentTerm := strconv.Itoa(s.currentTerm)
	term, err := strconv.Atoi(msg[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "handleRequestVote term conv err:", err)
		return
	}
	voteGranted := false
	if term < s.currentTerm {
		s.iface.Send("RVR", currentTerm, strconv.FormatBool(voteGranted))
		return
	}

	candidateID := msg[0]
	lastTerm, err := strconv.Atoi(msg[4])
	if err != nil {
		fmt.Fprintln(os.Stderr, "handleRequestVote lastTerm conv err:", err)
		return
	}
	lastIndex, err := strconv.Atoi(msg[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, "handleRequestVote lastIndex conv err:", err)
		return
	}
	if (s.votedFor == nil || s.votedFor.String() == candidateID) &&
		s.elog.candidateIsUpToDate(lastTerm, lastIndex) {
		voteGranted = true
	}
	s.iface.Send("RVR", currentTerm, strconv.FormatBool(voteGranted), candidateID)

	fmt.Println("XXX handleRequestVote: voteGranted ==", voteGranted)
}

func (s *server) handleRequestVoteResponse(msg []string) {
	fmt.Println("YYY handleRequestVoteResponse:", msg)

	serverID := msg[0]
	s.cVars.votesResponded[serverID] = true
	if voteGranted, err := strconv.ParseBool(msg[3]); err == nil && voteGranted {
		s.cVars.votesGranted[serverID] = true
	}

	n := s.getNumberOfServers()
	if len(s.cVars.votesGranted)*2 >= n {
		fmt.Println("candidate -> leader | term:", s.currentTerm)
		// TODO s.state = leader
		s.lVars.reset(s.getServersIDs(), s.elog.lastIndex())
	}
}

func (s *server) loop() {
	const (
		timeoutLB = 300 // election timeout lower bound
		timeoutHB = 500 // election timeout higher bound
	)

	var timeout time.Time
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	resetElectionTimeout := func() {
		// random int in range [LB, HB)
		timeoutValue := r.Intn(timeoutHB-timeoutLB) + timeoutLB
		timeout = time.Now().Add(time.Duration(timeoutValue) * time.Millisecond)
	}
	resetElectionTimeout()

	for {
		var msgType string
		msg, err := s.iface.Recv()
		if err == nil {
			fmt.Println("server", msg)

			if len(msg) == 2 { // server JOINED/LEFT
				s.handleControlMessage(msg)
			} else {
				msgType = msg[1]

				// "if candidate's or leader's term is out of date,
				// it immediately reverts to follower state"
				if term, err := strconv.Atoi(msg[2]); err == nil {
					if term > s.currentTerm {
						fmt.Println(s.state, "-> follower | term:", s.currentTerm)
						s.state = follower
						s.currentTerm = term
						s.votedFor = nil
						resetElectionTimeout()
					}
				} else {
					fmt.Fprintln(os.Stderr, "term conv err:", err)
				}
			}
		}

		switch s.state {
		case follower:
			if msgType == "RV" {
				s.handleRequestVote(msg)
				resetElectionTimeout()
				continue
			}
			// TODO handleAppendEntries (obviously resets timeout)
			// if msgType == "AE" {
			// prawdopodobnie jeszcze obsluga zadania klienta (przekierowanie go)

			// check election timeout
			if time.Now().After(timeout) {
				fmt.Println("follower -> candidate | term:", s.currentTerm)
				s.state = candidate
				s.cVars.reset()
			}

		case candidate:
			// TODO napisać funkcje konwersji stanów? bo votedFor trza zerować
			// zrobione powyżej
			// TODO if RPC request has term T > current term
			// then update current term and convert to follower
			// if it's requestVote RPC then vote for new candidate!
			// even though you're also a candidate

			if msgType == "RVR" {
				s.handleRequestVoteResponse(msg)
				// ...
			}

			if time.Now().After(timeout) {
				s.currentTerm++
				// vote for self
				s.votedFor = s.uuid
				resetElectionTimeout()
				// send requestVote RPC
				s.requestVote()
			}

		case leader:
		}
	}
}
