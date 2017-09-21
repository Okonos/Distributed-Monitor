package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
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

type requestVoteMsg struct {
	Term         int
	LastLogIndex int
	LastLogTerm  int
}

type requestVoteRespMsg struct {
	Term        int
	VoteGranted bool
}

type appendEntriesMsg struct {
	Term         int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []entry // log entries to store (empty for heartbeat)
	LeaderCommit int     // leader's commitIndex
}

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

func (s *server) handleControlMessage(senderID, msgType string) {
	switch msgType {
	case "JOINED":
		s.servers[senderID] = true
		if s.state == leader {
			s.lVars.nextIndex[senderID] = s.elog.lastIndex()
			s.lVars.matchIndex[senderID] = 0
		}
	case "LEFT":
		delete(s.servers, senderID)
		if s.state == leader {
			delete(s.lVars.nextIndex, senderID)
			delete(s.lVars.matchIndex, senderID)
		}
	}
}

func (s *server) requestVote() {
	msg := requestVoteMsg{
		Term:         s.currentTerm,
		LastLogIndex: s.elog.lastIndex(),
		LastLogTerm:  s.elog.lastTerm(),
	}
	// bez uuid, bo przesyłany przez socket
	s.iface.Send("RV", msg)
}

func (s *server) handleRequestVote(candidateID string, msg requestVoteMsg) {
	response := requestVoteRespMsg{
		Term: s.currentTerm,
	}
	if msg.Term < s.currentTerm {
		response.VoteGranted = false
		s.iface.Send("RVR", response, candidateID)
		fmt.Println("handleRequestVote: voteGranted == ", false, "(obsolete term)")
		return
	}

	voteGranted := false
	if (s.votedFor == nil || s.votedFor.String() == candidateID) &&
		s.elog.candidateIsUpToDate(msg.LastLogTerm, msg.LastLogIndex) {
		voteGranted = true
	}
	response.VoteGranted = voteGranted
	s.iface.Send("RVR", response, candidateID)

	fmt.Println("XXX handleRequestVote: voteGranted ==", voteGranted)
}

func (s *server) handleRequestVoteResponse(voterID string, msg requestVoteRespMsg) {
	fmt.Println("YYY handleRequestVoteResponse:", msg)

	s.cVars.votesResponded[voterID] = true
	if msg.VoteGranted {
		s.cVars.votesGranted[voterID] = true
	}

	n := s.getNumberOfServers()
	if len(s.cVars.votesGranted)*2 >= n {
		fmt.Println("candidate -> leader | term:", s.currentTerm)
		s.state = leader
		s.lVars.reset(s.getServersIDs(), s.elog.lastIndex())
	}
}

func decodeMessage(msgType string, msg []byte) (interface{}, int, error) {
	switch msgType {
	case "RV":
		var msgStruct requestVoteMsg
		if err := json.Unmarshal(msg, &msgStruct); err != nil {
			return nil, 0, err
		}
		return msgStruct, msgStruct.Term, nil
	case "RVR":
		var msgStruct requestVoteRespMsg
		if err := json.Unmarshal(msg, &msgStruct); err != nil {
			return nil, 0, err
		}
		return msgStruct, msgStruct.Term, nil
	case "AE":
		var msgStruct appendEntriesMsg
		if err := json.Unmarshal(msg, &msgStruct); err != nil {
			return nil, 0, err
		}
		return msgStruct, msgStruct.Term, nil
	default:
		text := fmt.Sprintf("decodeMessage mismatched msgType (%s)\n", msgType)
		return nil, 0, errors.New(text)
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
		senderID, msgType, msgBytes, err := s.iface.Recv()
		if err != nil && err != intface.ErrEAGAIN {
			fmt.Fprintln(os.Stderr, "ERROR in iface.Recv:", err)
		}

		var msg interface{}

		if msgType == "JOINED" || msgType == "LEFT" {
			s.handleControlMessage(senderID, msgType)
		} else if msgType != "" {
			var term int
			msg, term, err = decodeMessage(msgType, msgBytes)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				msgType = ""
			}
			// "if candidate's or leader's term is out of date,
			// it immediately reverts to follower state"
			if term > s.currentTerm {
				fmt.Println(s.state, "-> follower | term:", s.currentTerm)
				s.state = follower
				s.currentTerm = term
				s.votedFor = nil
				resetElectionTimeout()
			} else if term < s.currentTerm { // drop stale responses
				// instead of continue, so that timeouts may happen
				msgType = ""
			}
		}

		// fmt.Printf("server loop: [%s, %s, %v]\n", senderID, msgType, msgStruct)

		switch s.state {
		case follower:
			if msgType == "RV" {
				s.handleRequestVote(senderID, msg.(requestVoteMsg))
				resetElectionTimeout()
				continue
			}
			// TODO handleAppendEntries (obviously resets timeout)
			// also in candidate (cand -> follower when discovers current leader or new term) -- new term taken care of above, discovering leader in case candidate?
			// if msgType == "AE" {
			// prawdopodobnie jeszcze obsluga zadania klienta (przekierowanie go)

			// check election timeout
			if time.Now().After(timeout) {
				fmt.Println("follower -> candidate | term:", s.currentTerm)
				s.state = candidate
				s.cVars.reset()
			}

		case candidate:
			if msgType == "RVR" {
				s.handleRequestVoteResponse(senderID, msg.(requestVoteRespMsg))
				if s.state == leader {
					continue
				}
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
