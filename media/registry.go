package media

import (
	"flag"
	"fmt"
	"log"
	"maps"
	"slices"
)

// DefaultPipeline is the pipeline used when the user does not select one.
const DefaultPipeline = "gst"

// A Factory creates pipelines of one implementation. Implementations register
// a Factory from an init function, so that build tags decide which ones exist
// in a given binary.
type Factory interface {
	// ConfigureFlags registers the implementation's own flags. Flags common to
	// all pipelines are registered by Flags.Configure* instead, so whatever is
	// registered here must be prefixed with the implementation's name (for
	// example -gst-ccfb) to avoid collisions between implementations.
	ConfigureFlags(*flag.FlagSet)

	// NewPipeline creates a pipeline. It is called after flags are parsed.
	NewPipeline() (Pipeline, error)
}

var factories = map[string]Factory{}

// Register makes a pipeline implementation available under name. Registering
// the same name twice is a programming error and aborts the process.
//
// Register is also the supported way to plug in a pipeline from outside this
// repository: register it under a new name and select it with -media-pipeline.
func Register(name string, f Factory) {
	if _, ok := factories[name]; ok {
		log.Fatalf("duplicate media pipeline: %q", name)
	}
	factories[name] = f
}

// Lookup returns the factory registered under name.
func Lookup(name string) (Factory, error) {
	f, ok := factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown media pipeline %q, available: %v", name, Names())
	}
	return f, nil
}

// Names returns the names of all registered pipelines, sorted.
func Names() []string {
	return slices.Sorted(maps.Keys(factories))
}
