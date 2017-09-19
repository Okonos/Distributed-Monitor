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

const (
	follower serverState = iota
	candidate
	leader
)

type varStruct interface {
	resetVars()
}

type candidateVars struct {
	votesResponded map[string]bool
	votesGranted   map[string]bool
}

func newCandidateVars() *candidateVars {
	return &candidateVars{
		votesResponded: make(map[string]bool),
		votesGranted:   make(map[string]bool),
	}
}

func (cv *candidateVars) resetVars() {
	for k := range cv.votesResponded {
		delete(cv.votesResponded, k)
	}
	for k := range cv.votesGranted {
		delete(cv.votesGranted, k)
	}
}

// TODO
type leaderVars struct {
	nextIndex  []int
	matchIndex []int
}

func (lv *leaderVars) resetVars() {
}

type server struct {
	uuid        uuid.UUID
	iface       *intface.MessagingInterface
	elog        *entryLog
	currentTerm int
	state       serverState
	votedFor    uuid.UUID // the candidate the server voted in the current term
	cVars       *candidateVars
}

func newServer() *server {
	uuid := uuid.NewRandom()
	return &server{
		uuid:        uuid,
		iface:       intface.New(uuid),
		elog:        newLog(),
		currentTerm: 0,
		state:       follower,
		votedFor:    nil,
		cVars:       newCandidateVars(),
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

// TODO
const n = 2

func (s *server) handleRequestVoteResponse(msg []string) {
	fmt.Println("YYY handleRequestVoteResponse:", msg)

	serverID := msg[0]
	s.cVars.votesResponded[serverID] = true
	if voteGranted, err := strconv.ParseBool(msg[3]); err == nil && voteGranted {
		s.cVars.votesGranted[serverID] = true
	}

	if len(s.cVars.votesGranted)*2 >= n {
		fmt.Println("candidate -> leader")
		// s.state = leader
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
			msgType = msg[1]

			// TODO struct zamiast []string? Wtedy niepotrzebne ify?
			if msgType == "RV" {
				term, err := strconv.Atoi(msg[2])
				if err != nil {
					fmt.Fprintln(os.Stderr, "term conv err:", err)
				}

				// "if candidate's or leader's term is out of date,
				// it immediately reverts to follower state"
				if term > s.currentTerm {
					fmt.Println("candidate -> follower")
					s.state = follower
					s.currentTerm = term
					s.votedFor = nil
					resetElectionTimeout()
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
				fmt.Println("follower -> candidate")
				s.state = candidate
				s.cVars.resetVars()
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
