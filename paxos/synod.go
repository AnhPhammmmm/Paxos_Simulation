package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
)

type State struct {
	Nodes    []Node
	Messages map[Key]string
}

type Node struct {
	PromiseSequence int
	AcceptSequence  int
	AcceptValue     int

	// Proposer state
	ProposalCount   int            // Base (e.g., 5000, 5010)
	CurrentSequence int            // Current active sequence
	CurrentValue    int            // Value being proposed
	ProposerState   int            // 0:Idle, 1:Prepare, 2:Accept, 3:Decided
	PrepareVotes    map[int]string // senderID -> "accSeq accVal"
	AcceptVotes     map[int]bool   // senderID -> true
	RejectCount     int            // Tracking NACKs
}

const (
	StateIdle = iota
	StatePrepare
	StateAccept
	StateDecided
)

const (
	MsgPrepareRequest = iota
	MsgPrepareResponse
	MsgAcceptRequest
	MsgAcceptResponse
	MsgDecideRequest
)

type Key struct {
	Type   int
	Time   int
	Target int
}

func main() {
	log.SetFlags(log.Lshortfile)
	state := &State{Messages: make(map[Key]string)}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		if len(strings.TrimSpace(line)) == 0 {
			continue
		}
		line += "\n"

		switch {
		case state.TryInitialize(line):
		case state.TrySendPrepare(line):
		case state.TryDeliverPrepareRequest(line):
		case state.TryDeliverPrepareResponse(line):
		case state.TryDeliverAcceptRequest(line):
		case state.TryDeliverAcceptResponse(line):
		case state.TryDeliverDecideRequest(line):
		default:
			log.Fatalf("unknown line: %s", line)
		}
	}
}

func (s *State) log(msg string) {
	fmt.Printf("--> %s\n", msg)
}

func (s *State) quorum() int {
	return (len(s.Nodes) / 2) + 1
}

// Start a new round (manual or restart)
func (s *State) startNewRound(time int, sender int) {
	node := &s.Nodes[sender-1]
	if node.ProposalCount == 0 {
		node.ProposalCount = 5000
	} else {
		node.ProposalCount += 10
	}
	seq := node.ProposalCount + sender

	node.CurrentSequence = seq
	node.ProposerState = StatePrepare
	node.PrepareVotes = make(map[int]string)
	node.AcceptVotes = make(map[int]bool)
	node.RejectCount = 0

	s.log(fmt.Sprintf("sent prepare requests to all nodes from %d with sequence %d", sender, seq))

	for i := 1; i <= len(s.Nodes); i++ {
		key := Key{Type: MsgPrepareRequest, Time: time, Target: i}
		s.Messages[key] = fmt.Sprintf("%d %d", sender, seq)
	}
}

func (s *State) TryInitialize(line string) bool {
	var n int
	if _, err := fmt.Sscanf(line, "initialize %d nodes", &n); err != nil {
		return false
	}
	s.Nodes = make([]Node, n)
	for i := range s.Nodes {
		s.Nodes[i].PrepareVotes = make(map[int]string)
		s.Nodes[i].AcceptVotes = make(map[int]bool)
	}
	s.log(fmt.Sprintf("initialized %d nodes", n))
	return true
}

func (s *State) TrySendPrepare(line string) bool {
	var time, sender int
	if _, err := fmt.Sscanf(line, "at %d send prepare request from %d", &time, &sender); err != nil {
		return false
	}
	s.startNewRound(time, sender)
	return true
}

func (s *State) TryDeliverPrepareRequest(line string) bool {
	var now, target, sendTime int
	if _, err := fmt.Sscanf(line, "at %d deliver prepare request message to %d from time %d", &now, &target, &sendTime); err != nil {
		return false
	}

	key := Key{Type: MsgPrepareRequest, Time: sendTime, Target: target}
	msg, ok := s.Messages[key]
	if !ok { return true }

	var sender, seq int
	fmt.Sscanf(msg, "%d %d", &sender, &seq)
	node := &s.Nodes[target-1]

	if seq > node.PromiseSequence {
		node.PromiseSequence = seq
		valStr := "with no value"
		if node.AcceptValue != 0 {
			valStr = fmt.Sprintf("with value %d sequence %d", node.AcceptValue, node.AcceptSequence)
		}
		s.log(fmt.Sprintf("prepare request from %d sequence %d accepted by %d %s", sender, seq, target, valStr))
	}

	respKey := Key{Type: MsgPrepareResponse, Time: now, Target: sender}
	s.Messages[respKey] = fmt.Sprintf("%d %d %d %d", target, seq, node.AcceptSequence, node.AcceptValue)
	return true
}

func (s *State) TryDeliverPrepareResponse(line string) bool {
	var now, target, sendTime int
	if _, err := fmt.Sscanf(line, "at %d deliver prepare response message to %d from time %d", &now, &target, &sendTime); err != nil {
		return false
	}

	key := Key{Type: MsgPrepareResponse, Time: sendTime, Target: target}
	msg, ok := s.Messages[key]
	if !ok { return true }

	var sender, seq, accSeq, accVal int
	fmt.Sscanf(msg, "%d %d %d %d", &sender, &seq, &accSeq, &accVal)
	node := &s.Nodes[target-1]

	if seq != node.CurrentSequence {
		// Just check for duplicates of current sequence, otherwise ignore if past
		return true
	}

	if _, exists := node.PrepareVotes[sender]; exists {
		s.log(fmt.Sprintf("prepare response from %d sequence %d ignored as a duplicate by %d", sender, seq, target))
		return true
	}

	valStr := "with no value"
	if accVal != 0 {
		valStr = fmt.Sprintf("with value %d sequence %d", accVal, accSeq)
	}

	s.log(fmt.Sprintf("positive prepare response from %d sequence %d recorded by %d %s", sender, seq, target, valStr))
	
	if node.ProposerState != StatePrepare {
		s.log(fmt.Sprintf("valid prepare vote ignored by %d because round is already resolved", target))
		return true
	}

	node.PrepareVotes[sender] = fmt.Sprintf("%d %d", accSeq, accVal)

	if len(node.PrepareVotes) == s.quorum() {
		highestSeq := -1
		propValue := target * 11111
		foundVal := false
		for _, v := range node.PrepareVotes {
			var pSeq, pVal int
			fmt.Sscanf(v, "%d %d", &pSeq, &pVal)
			if pVal != 0 && pSeq > highestSeq {
				highestSeq, propValue, foundVal = pSeq, pVal, true
			}
		}

		origin := fmt.Sprintf("proposing its own value %d", propValue)
		if foundVal {
			origin = fmt.Sprintf("proposing discovered value %d sequence %d", propValue, highestSeq)
		}
		s.log(fmt.Sprintf("prepare round successful: %d %s", target, origin))
		node.CurrentValue = propValue
		node.ProposerState = StateAccept
		s.log(fmt.Sprintf("sent accept requests to all nodes from %d with value %d sequence %d", target, propValue, seq))

		for i := 1; i <= len(s.Nodes); i++ {
			s.Messages[Key{Type: MsgAcceptRequest, Time: now, Target: i}] = fmt.Sprintf("%d %d %d", target, seq, propValue)
		}
	}
	return true
}

func (s *State) TryDeliverAcceptRequest(line string) bool {
	var now, target, sendTime int
	if _, err := fmt.Sscanf(line, "at %d deliver accept request message to %d from time %d", &now, &target, &sendTime); err != nil {
		return false
	}

	key := Key{Type: MsgAcceptRequest, Time: sendTime, Target: target}
	msg, ok := s.Messages[key]
	if !ok { return true }

	var sender, seq, val int
	fmt.Sscanf(msg, "%d %d %d", &sender, &seq, &val)
	node := &s.Nodes[target-1]

	status := "REJECT"
	if seq >= node.PromiseSequence {
		node.PromiseSequence, node.AcceptSequence, node.AcceptValue = seq, seq, val
		status = "OK"
		s.log(fmt.Sprintf("accept request from %d with value %d sequence %d accepted by %d", sender, val, seq, target))
		
		count := 0
		for _, n := range s.Nodes {
			if n.AcceptSequence == seq { count++ }
		}
		if count == s.quorum() {
			s.log("note: consensus has been achieved")
		}
	} else {
		s.log(fmt.Sprintf("accept request from %d with value %d sequence %d rejected by %d", sender, val, seq, target))
	}

	s.Messages[Key{Type: MsgAcceptResponse, Time: now, Target: sender}] = fmt.Sprintf("%d %d %s", target, seq, status)
	return true
}

func (s *State) TryDeliverAcceptResponse(line string) bool {
	var now, target, sendTime int
	if _, err := fmt.Sscanf(line, "at %d deliver accept response message to %d from time %d", &now, &target, &sendTime); err != nil {
		return false
	}

	key := Key{Type: MsgAcceptResponse, Time: sendTime, Target: target}
	msg, ok := s.Messages[key]
	if !ok { return true }

	var sender, seq int
	var status string
	fmt.Sscanf(msg, "%d %d %s", &sender, &seq, &status)
	node := &s.Nodes[target-1]

	if seq != node.CurrentSequence {
		s.log(fmt.Sprintf("accept response from %d sequence %d from the past ignored by %d", sender, seq, target))
		return true
	}

	if status == "REJECT" {
		s.log(fmt.Sprintf("negative accept response from %d sequence %d recorded by %d", sender, seq, target))
		node.RejectCount++
		if node.RejectCount == s.quorum() && node.ProposerState != StateDecided {
			s.log(fmt.Sprintf("accept round failed at %d, restarting", target))
			s.startNewRound(now, target)
		}
		return true
	}

	s.log(fmt.Sprintf("positive accept response from %d sequence %d recorded by %d", sender, seq, target))
	if node.ProposerState == StateAccept {
		node.AcceptVotes[sender] = true
		if len(node.AcceptVotes) == s.quorum() {
			node.ProposerState = StateDecided
			s.log(fmt.Sprintf("accept round successful: %d detected consensus with value %d", target, node.CurrentValue))
			s.log(fmt.Sprintf("sent decide requests to all nodes from %d with value %d", target, node.CurrentValue))
			for i := 1; i <= len(s.Nodes); i++ {
				s.Messages[Key{Type: MsgDecideRequest, Time: now, Target: i}] = fmt.Sprintf("%d %d", target, node.CurrentValue)
			}
		}
	} else {
		s.log(fmt.Sprintf("valid accept vote ignored by %d because round is already resolved", target))
	}
	return true
}

func (s *State) TryDeliverDecideRequest(line string) bool {
	var now, target, sendTime int
	if _, err := fmt.Sscanf(line, "at %d deliver decide request message to %d from time %d", &now, &target, &sendTime); err != nil {
		return false
	}
	key := Key{Type: MsgDecideRequest, Time: sendTime, Target: target}
	msg, ok := s.Messages[key]
	if !ok { return true }
	var sender, val int
	fmt.Sscanf(msg, "%d %d", &sender, &val)
	s.log(fmt.Sprintf("recording consensus value %d at %d", val, target))
	return true
}