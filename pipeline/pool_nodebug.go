//go:build !race && !pipelinedebug

package pipeline

// poolDebug makes a pool poison a released value instead of recycling it. It is
// on in the race build and under the pipelinedebug tag.
const poolDebug = false
