// Package pipeline is the machinery behind the mrtp element model: pooled
// packets, the graph that wires elements together and drives them, and the
// generic elements that every pipeline needs.
//
// A graph is built, then run, then closed. Wiring records edges, [Graph.Run]
// binds and negotiates them in topological order and gives every [mrtp.Driver]
// a goroutine, and [Graph.Close] releases the elements.
package pipeline
