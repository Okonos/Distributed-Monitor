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

type requestVoteResponse struct {
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

type appendEntriesResponse struct {
	Term       int
	Success    bool // true if follower had entry matching prevLog{Index, Term}
	MatchIndex int
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
	hbTimeout  time.Time // heartbeat timeout
}

func newLeaderVars() *leaderVars {
	return &leaderVars{
		nextIndex:  make(map[string]int),
		matchIndex: make(map[string]int),
		hbTimeout:  time.Now(),
	}
}

func (lv *leaderVars) reset(servers []string, lastLogIndex int) {
	// XXX in paper first index is 1!
	for _, id := range servers {
		lv.nextIndex[id] = lastLogIndex + 1
		lv.matchIndex[id] = -1
	}
	lv.hbTimeout = time.Now()
}

func (lv *leaderVars) setTimeout() {
	const heartbeatTimeoutLen = 150 * time.Millisecond
	lv.hbTimeout = time.Now().Add(heartbeatTimeoutLen)
}

type server struct {
	uuid        uuid.UUID
	iface       *intface.MessagingInterface
	elog        *entryLog
	currentTerm int
	votedFor    string // the candidate the server voted for in the current term
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
		votedFor:    "",
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

func (s *server) isQuorum(n int) bool {
	// +1 to include this server
	return 2*n > len(s.servers)+1
}

func (s *server) handleControlMessage(senderID, msgType string) {
	switch msgType {
	case "JOINED":
		s.servers[senderID] = true
		if s.state == leader {
			s.lVars.nextIndex[senderID] = s.elog.lastIndex() + 1
			s.lVars.matchIndex[senderID] = -1
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
	for serverID := range s.servers {
		s.iface.Send("RV", msg, serverID)
	}
}

func (s *server) handleRequestVote(candidateID string, msg requestVoteMsg) bool {
	response := requestVoteResponse{
		Term: s.currentTerm,
	}
	if msg.Term < s.currentTerm {
		response.VoteGranted = false
		s.iface.Send("RVR", response, candidateID)
		fmt.Println("handleRequestVote: voteGranted == ", false, "(obsolete term)")
		return true // message dropped, no timeout reset
	}

	voteGranted := false
	if (s.votedFor == "" || s.votedFor == candidateID) &&
		s.elog.candidateIsUpToDate(msg.LastLogTerm, msg.LastLogIndex) {
		voteGranted = true
		s.votedFor = candidateID
	}
	response.VoteGranted = voteGranted
	s.iface.Send("RVR", response, candidateID)

	fmt.Println("XXX handleRequestVote: voteGranted ==", voteGranted)

	return false
}

func (s *server) handleRequestVoteResponse(voterID string,
	msg requestVoteResponse) {
	fmt.Println("YYY handleRequestVoteResponse:", msg)

	s.cVars.votesResponded[voterID] = true
	if msg.VoteGranted {
		s.cVars.votesGranted[voterID] = true
	}

	if s.state == candidate && s.isQuorum(len(s.cVars.votesGranted)) {
		fmt.Println("candidate -> leader | term:", s.currentTerm)
		s.state = leader
		s.lVars.reset(s.getServersIDs(), s.elog.lastIndex())
	}
}

func (s *server) appendEntries(heartbeat bool) {
	if heartbeat { // send appendEntries with empty entries[]
		for serverID, nextIndex := range s.lVars.nextIndex {
			prevLogIndex := nextIndex - 1
			prevLogTerm := 0
			if prevLogIndex >= 0 {
				prevLogTerm = s.elog.entries[prevLogIndex].Term
			}
			// up to 1 entry
			var lastEntry int // = min(len(log), index+1)
			if len(s.elog.entries) < nextIndex+1 {
				lastEntry = len(s.elog.entries)
			} else {
				lastEntry = nextIndex + 1
			}
			// XXX nextIndex a len
			entries := s.elog.entries[nextIndex:lastEntry]

			msg := appendEntriesMsg{
				Term:         s.currentTerm,
				PrevLogIndex: prevLogIndex,
				PrevLogTerm:  prevLogTerm,
				Entries:      entries,
				LeaderCommit: s.elog.commitIndex,
			}
			s.iface.Send("AE", msg, serverID)
		}
	}
}

func (s *server) handleAppendEntries(leaderID string, msg appendEntriesMsg) bool {
	logOk := msg.PrevLogIndex == -1 ||
		(msg.PrevLogIndex >= 0 &&
			msg.PrevLogIndex < len(s.elog.entries) &&
			msg.PrevLogTerm == s.elog.entries[msg.PrevLogIndex].Term)

	response := appendEntriesResponse{
		Term:       s.currentTerm,
		Success:    false,
		MatchIndex: -1}

	// reject request
	if msg.Term < s.currentTerm ||
		(msg.Term == s.currentTerm && s.state == follower && !logOk) {
		s.iface.Send("AER", response, leaderID)
		return true // message dropped, no timeout reset
	}

	// return to follower state
	if msg.Term == s.currentTerm {
		if s.state == candidate {
			s.state = follower
			return false
		}
		if s.state == follower && logOk {
			index := msg.PrevLogIndex + 1
			if len(msg.Entries) == 0 || (len(s.elog.entries)-1 >= index &&
				s.elog.entries[index].Term == msg.Entries[0].Term) {

				s.elog.commitIndex = msg.LeaderCommit
				response.Success = true
				response.MatchIndex = msg.PrevLogIndex + len(msg.Entries)
				s.iface.Send("AER", response, leaderID)
			} else if len(msg.Entries) != 0 {
				if len(s.elog.entries)-1 >= index &&
					s.elog.entries[index].Term != msg.Entries[0].Term {
					// conflict: remove 1 entry
					s.elog.entries = s.elog.entries[:len(s.elog.entries)-1]
				} else if len(s.elog.entries)-1 == msg.PrevLogIndex {
					// no conflict: append entry
					s.elog.entries = append(s.elog.entries, msg.Entries[0])
				}
			}
		}
	}

	return false
}

func (s *server) handleAppendEntriesResponse(followerID string,
	msg appendEntriesResponse) {
	nextIndex := s.lVars.nextIndex
	if msg.Success {
		nextIndex[followerID] = msg.MatchIndex + 1
		s.lVars.matchIndex[followerID] = msg.MatchIndex
	} else {
		nextIndex[followerID]--
		if nextIndex[followerID] < 0 {
			nextIndex[followerID] = 0
		}
	}
}

func (s *server) advanceCommitIndex() {
	agreeing := 1 // since only leader advances the commitIndex and it uses his log
	maxAgreeIndex := -1
	for index := 0; index < len(s.elog.entries); index++ {
		for serverID := range s.servers {
			if s.lVars.matchIndex[serverID] >= index {
				agreeing++
			}
			if s.isQuorum(agreeing) && index > maxAgreeIndex {
				maxAgreeIndex = index
			}
		}
	}
	if maxAgreeIndex != -1 && s.elog.entries[maxAgreeIndex].Term == s.currentTerm {
		if s.elog.commitIndex != maxAgreeIndex {
			fmt.Println("CommitIndex advanced from",
				s.elog.commitIndex, "to", maxAgreeIndex)
		}
		s.elog.commitIndex = maxAgreeIndex
	}
}

func decodeMessage(msgType string, msg []byte) (interface{}, int, error) {
	switch msgType {
	case "RV":
		var msgStruct requestVoteMsg
		err := json.Unmarshal(msg, &msgStruct)
		return msgStruct, msgStruct.Term, err
	case "RVR":
		var msgStruct requestVoteResponse
		err := json.Unmarshal(msg, &msgStruct)
		return msgStruct, msgStruct.Term, err
	case "AE":
		var msgStruct appendEntriesMsg
		err := json.Unmarshal(msg, &msgStruct)
		// fmt.Println("AppendEntries received |", msgStruct.Term)
		fmt.Println(msgStruct)
		return msgStruct, msgStruct.Term, err
	case "AER":
		var msgStruct appendEntriesResponse
		err := json.Unmarshal(msg, &msgStruct)
		fmt.Println("AER", msgStruct)
		return msgStruct, msgStruct.Term, err
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

	// TODO remove
	nTimeoutsPassed := 0

	for {
		senderID, msgType, msgBytes, err := s.iface.Recv()
		if err != nil && err != intface.ErrEAGAIN {
			fmt.Fprintln(os.Stderr, "ERROR in iface.Recv:", err)
			continue
		}

		// fmt.Printf("server loop: [%s, %s, %v]\n", senderID, msgType, msgStruct)

		if err == nil { // Message was received
			switch msgType {
			case "JOINED", "LEFT":
				s.handleControlMessage(senderID, msgType)
			default:
				message, term, err := decodeMessage(msgType, msgBytes)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					break
				}
				// "if candidate's or leader's term is out of date,
				// it immediately reverts to follower state"
				if term > s.currentTerm {
					fmt.Println(s.state, "-> follower | term:", s.currentTerm)
					s.state = follower
					s.currentTerm = term
					s.votedFor = ""
					resetElectionTimeout()
				}

				var dropped bool // term was obsolete and message was dropped
				// TODO client redirect
				switch msg := message.(type) {
				case requestVoteMsg:
					dropped = s.handleRequestVote(senderID, msg)

				case requestVoteResponse:
					if term < s.currentTerm { // drop stale responses
						dropped = true
						break
					}
					s.handleRequestVoteResponse(senderID, msg)

				case appendEntriesMsg:
					dropped = s.handleAppendEntries(senderID, msg)

				case appendEntriesResponse:
					if term < s.currentTerm { // drop stale responses
						dropped = true
						break
					}
					s.handleAppendEntriesResponse(senderID, msg)
					s.advanceCommitIndex() // TODO tylko tu?
				}

				// reset the timeout only if message was not dropped
				// XXX client request should not reset the timeout
				if !dropped {
					resetElectionTimeout()
				}

				fmt.Println("LOG:", s.elog.entries, "nextIndex:", s.lVars.nextIndex)
			}
		}

		switch s.state {
		case follower:
			// check election timeout
			if time.Now().After(timeout) {
				fmt.Println("follower -> candidate | term:", s.currentTerm)
				s.state = candidate
				s.cVars.reset()
			}

		case candidate:
			if time.Now().After(timeout) {
				s.currentTerm++
				// vote for self
				s.votedFor = s.uuid.String()
				resetElectionTimeout()
				// send requestVote RPC
				s.requestVote()
			}

		case leader:
			if time.Now().After(s.lVars.hbTimeout) {
				s.appendEntries(true) // heartbeat
				s.lVars.setTimeout()
				// TODO remove
				nTimeoutsPassed++
				if nTimeoutsPassed > 10 {
					s.elog.append(s.currentTerm, "GET")
					fmt.Println("LOG:", s.elog.entries)
					nTimeoutsPassed = 0
				}
			}
		}
	}
}
