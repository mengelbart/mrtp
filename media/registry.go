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

// An Implementation is one registered media pipeline. Implementations
// register from an init function, so that build tags decide which ones exist
// in a given binary.
type Implementation interface {
	// ConfigureFlags registers the implementation's own flags. Flags common to
	// all pipelines are registered by Flags.Configure* instead, so whatever is
	// registered here must be prefixed with the implementation's name (for
	// example -gst-ccfb) to avoid collisions between implementations.
	ConfigureFlags(*flag.FlagSet)

	// NewFactory creates the factory the streams are built with. It is called
	// after flags are parsed.
	NewFactory() (Factory, error)
}

var implementations = map[string]Implementation{}

// Register makes a pipeline implementation available under name. Registering
// the same name twice is a programming error and aborts the process.
//
// Register is also the supported way to plug in a pipeline from outside this
// repository: register it under a new name and select it with -media-pipeline.
func Register(name string, f Implementation) {
	if _, ok := implementations[name]; ok {
		log.Fatalf("duplicate media pipeline: %q", name)
	}
	implementations[name] = f
}

// Lookup returns the implementation registered under name.
func Lookup(name string) (Implementation, error) {
	f, ok := implementations[name]
	if !ok {
		return nil, fmt.Errorf("unknown media pipeline %q, available: %v", name, Names())
	}
	return f, nil
}

// Names returns the names of all registered pipelines, sorted.
func Names() []string {
	return slices.Sorted(maps.Keys(implementations))
}
