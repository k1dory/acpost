//go:build race

package main

// raceBuild: the tests run under the race detector, and so do the binaries they build (the nodes
// and CLIs the end-to-end tests drive are where their concurrency is).
const raceBuild = true
