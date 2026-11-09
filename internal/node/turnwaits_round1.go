package node

import "sync"

// What agents' turns wait for: a turn that sent a message and waits for it (send --wait, follow)
// holds its session's locks until the answer comes; a message of that answer's chain that needs
// one of those locks would wait for itself. A turn that sent a message and went on waits for
// nothing: a message of its chain queues behind its locks, as any other.
type turnWaits struct {
	mu sync.Mutex
	of map[string]map[string]int // the waiting turn's message → the messages it waits for (how many waits)
}

var waitsOfNodes sync.Map // *Node → *turnWaits

func (n *Node) waits() *turnWaits {
	w, _ := waitsOfNodes.LoadOrStore(n, &turnWaits{of: map[string]map[string]int{}})
	return w.(*turnWaits)
}

// noteWait records that p, an agent in a turn, waits for message id; the func returned ends it.
// Anyone else's wait is not a turn's and is not recorded.
func (n *Node) noteWait(p Principal, id string) func() {
	if p.Kind != "agent" {
		return func() {}
	}
	turn := n.turnOf(p).id
	if turn == "" || id == "" {
		return func() {}
	}
	w := n.waits()
	w.mu.Lock()
	if w.of[turn] == nil {
		w.of[turn] = map[string]int{}
	}
	w.of[turn][id]++
	w.mu.Unlock()
	return func() {
		w.mu.Lock()
		if w.of[turn][id]--; w.of[turn][id] <= 0 {
			delete(w.of[turn], id)
			if len(w.of[turn]) == 0 {
				delete(w.of, turn)
			}
		}
		w.mu.Unlock()
	}
}

// waitsReach says whether the turn of message from waits, through waits only, for message to: from
// waits for M1, M1's turn waits for M2, …, one of them waits for to. A message that needs a lock
// from's turn holds is then in a cycle (from never lets go before to is answered) and is refused
// at once; a chain with one hop that does not wait ends by itself, and the message queues.
func (n *Node) waitsReach(from, to string) bool {
	w := n.waits()
	w.mu.Lock()
	defer w.mu.Unlock()
	seen := map[string]bool{from: true}
	next := []string{from}
	for len(next) > 0 && len(seen) < 1024 {
		turn := next[0]
		next = next[1:]
		for id := range w.of[turn] {
			if id == to {
				return true
			}
			if !seen[id] {
				seen[id] = true
				next = append(next, id)
			}
		}
	}
	return false
}
