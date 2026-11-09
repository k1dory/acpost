package node

import (
	"testing"
	"time"
)

// For the tests of package node_test (a hub and a node over HTTP).

// FastHubForTest shortens the hub's timings for the test (fastHub).
func FastHubForTest(t *testing.T, d time.Duration) { fastHub(t, d) }

// MaintainEveryForTest makes the node's upkeep (expiring messages among it) run every d.
func (n *Node) MaintainEveryForTest(d time.Duration) { n.setMaintainEvery(d) }
