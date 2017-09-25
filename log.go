package main

type entry struct {
	Term    int
	Command string
}

type entryLog struct {
	entries     []entry
	commitIndex int // the index of the latest entry the state machine may apply
}

func newLog() *entryLog {
	return &entryLog{
		entries:     make([]entry, 0),
		commitIndex: -1,
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
		// (indexing from 0 as opposed to indexing from one in paper)
		return index+1 >= logLen
	}
	return term > logTerm
}
