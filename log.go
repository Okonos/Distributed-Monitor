package main

import "fmt"

type entry struct {
	Term      int
	Requester string
	Request   clientRequest
}

func (e entry) String() string {
	return fmt.Sprintf("{%d %s %s}", e.Term, e.Requester[:8], e.Request)
}

type entryLog struct {
	entries      []entry
	commitIndex  int // index of the latest entry the state machine may apply
	lastApplied  int // index of highest log entry applied to state machine
	itemCount    int
	maxSize      int
	stateMachine []int
}

func newLog() *entryLog {
	return &entryLog{
		entries:      make([]entry, 0),
		commitIndex:  -1,
		lastApplied:  -1,
		itemCount:    0,
		maxSize:      10,
		stateMachine: make([]int, 0),
	}
}

// XXX in paper first index is 1!
func (l *entryLog) lastIndex() int {
	return len(l.entries) - 1
}

func (l *entryLog) lastTerm() int {
	if len(l.entries) == 0 {
		return 0
	}
	return l.entries[len(l.entries)-1].Term
}

// args: candidate's last term and index
func (l *entryLog) candidateIsUpToDate(term, index int) bool {
	logLen := len(l.entries)
	logTerm := l.lastTerm()
	if term == logTerm {
		// XXX since this checks which log is longer, the index must be incremented
		// (indexing from 0 as opposed to indexing from 1 in paper)
		return index+1 >= logLen
	}
	return term > logTerm
}

// run when becoming leader
func (l *entryLog) calculateItemCount() {
	l.itemCount = 0
	for _, e := range l.entries {
		switch e.Request.Command {
		case "GET":
			l.itemCount--
		case "PUT":
			l.itemCount++
		}
	}
}

func (l *entryLog) append(term int, requester string, request clientRequest) {
	l.entries = append(l.entries, entry{term, requester, request})
}

func (l *entryLog) applyEntry() (clientID string, response clientResponse) {
	if l.commitIndex > l.lastApplied {
		l.lastApplied++
		request := l.entries[l.lastApplied].Request
		clientID = l.entries[l.lastApplied].Requester
		response.Text = request.Command

		switch request.Command {
		case "GET":
			last := len(l.stateMachine) - 1
			response.Value, l.stateMachine =
				l.stateMachine[last], l.stateMachine[:last]
		case "PUT":
			l.stateMachine = append(l.stateMachine, request.Argument)
			response.Value = request.Argument
		}

		fmt.Println("STATE", l.stateMachine)
	}

	return
}
