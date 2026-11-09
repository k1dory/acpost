package store

import "strings"

// CopyOrigins are the origins of a joined node's local copies of messages its hub holds: sent
// through the hub (remote), on their way (forwarding), or not sent (unsent). Such a copy is the
// hub's work, whatever its recipient reads: nothing here claims it, counts it in a queue,
// requeues it or reaps it. (A copy stored before the copy kept the sender's address names the
// hub's agent like a local one: agent:bot.)
var CopyOrigins = []string{"remote", "forwarding", "unsent"}

// localWork is the condition every query over "this node's work for an agent" adds.
var localWork = "origin NOT IN ('" + strings.Join(CopyOrigins, "', '") + "')"

// copyRecipient is the recipient a local copy takes from its hub's message: the hub names an agent
// on another node qualified (agent:bot@other), and one of its own unqualified (agent:bot), which
// here would read as this node's agent. A qualified answer is taken; otherwise the copy keeps the
// address it was sent to (agent:bot@hub). FinishForward applies the same rule in SQL.
func copyRecipient(hubTo, cur string) string {
	if strings.Contains(hubTo, "@") || cur == "" {
		return hubTo
	}
	return cur
}
