package media

import (
	"flag"
	"fmt"
	"strings"

	"github.com/mengelbart/mrtp"
)

// DefaultPayloadType is the RTP payload type used when a caller does not know
// a better one.
const DefaultPayloadType = 96

// Flags holds the command line configuration that is common to all media
// pipelines. Commands embed it, register the flags they need, and then ask it
// for a pipeline and for stream configurations.
//
// Implementation specific flags are registered by the Factory instead, see
// Factory.ConfigureFlags.
type Flags struct {
	PipelineName string
	Codec        string

	SourceLocation string
	SinkLocation   string

	commonFlagsSet bool
}

// configureCommon registers the flags that apply to both directions, plus the
// flags of every registered implementation. It is idempotent, so that a
// command that sends and receives can call both ConfigureSender and
// ConfigureReceiver.
func (f *Flags) configureCommon(fs *flag.FlagSet) error {
	if f.commonFlagsSet {
		return nil
	}
	f.commonFlagsSet = true

	fs.StringVar(&f.PipelineName, "media-pipeline", DefaultPipeline,
		fmt.Sprintf("Media pipeline implementation to use (%v)", strings.Join(Names(), ", ")))
	fs.StringVar(&f.Codec, "codec", mrtp.H264.String(), "Codec to encode and decode with (H264, VP8, VP9, FAKE)")
	for _, name := range Names() {
		if err := registerFactoryFlags(fs, name, factories[name]); err != nil {
			return err
		}
	}
	return nil
}

// registerFactoryFlags registers the flags of one implementation, rejecting
// any flag that is not prefixed with the implementation's name.
//
// The prefix is enforced rather than documented because the flags of every
// registered implementation are registered, not just those of the selected
// one: without it, two implementations would fight over the same flag name,
// and an implementation's flags would be indistinguishable from the options
// that apply to all of them.
func registerFactoryFlags(fs *flag.FlagSet, name string, factory Factory) error {
	// Collect the flags separately first, so that a misbehaving
	// implementation cannot leave half of its flags on the real flag set.
	scratch := flag.NewFlagSet(name, flag.ContinueOnError)
	factory.ConfigureFlags(scratch)

	// Validate every flag before registering any of them, so that a rejected
	// implementation contributes nothing at all. flag.FlagSet.VisitAll walks
	// the flags in lexicographic order, so validating while registering would
	// leave behind whatever sorts before the offending flag.
	prefix := name + "-"
	var err error
	scratch.VisitAll(func(registered *flag.Flag) {
		if err != nil {
			return
		}
		if !strings.HasPrefix(registered.Name, prefix) {
			err = fmt.Errorf("media pipeline %q registers flag -%v, which is missing the required %q prefix",
				name, registered.Name, prefix)
			return
		}
		if fs.Lookup(registered.Name) != nil {
			err = fmt.Errorf("media pipeline %q registers flag -%v, which is already registered",
				name, registered.Name)
		}
	})
	if err != nil {
		return err
	}

	scratch.VisitAll(func(registered *flag.Flag) {
		fs.Var(registered.Value, registered.Name, registered.Usage)
	})
	return nil
}

// ConfigureSender registers the flags needed to send a stream. It fails if a
// registered pipeline implementation registers flags that are not prefixed
// with its name.
func (f *Flags) ConfigureSender(fs *flag.FlagSet) error {
	if err := f.configureCommon(fs); err != nil {
		return err
	}
	fs.StringVar(&f.SourceLocation, "source-location", SourceTest,
		"Media file to send. Empty, the default, uses the pipeline's generated test source.")
	return nil
}

// ConfigureReceiver registers the flags needed to receive a stream. It fails
// if a registered pipeline implementation registers flags that are not
// prefixed with its name.
func (f *Flags) ConfigureReceiver(fs *flag.FlagSet) error {
	if err := f.configureCommon(fs); err != nil {
		return err
	}
	fs.StringVar(&f.SinkLocation, "sink-location", SinkDisplay,
		fmt.Sprintf("File to write the received media to, or %q to drop it. Empty, the default, renders it.", SinkDiscard))
	return nil
}

// NewPipeline creates the pipeline selected by -media-pipeline.
func (f *Flags) NewPipeline() (Pipeline, error) {
	factory, err := Lookup(f.PipelineName)
	if err != nil {
		return nil, err
	}
	return factory.NewPipeline()
}

// SenderConfig builds the configuration of an outgoing stream from the parsed
// flags. The caller fills in the transport endpoints, and may override the
// payload type.
func (f *Flags) SenderConfig(name string) (SenderConfig, error) {
	codec, err := mrtp.NewCodec(f.Codec)
	if err != nil {
		return SenderConfig{}, err
	}
	// The location selects the source: empty is the generated test source,
	// anything else is the file to read from.
	return SenderConfig{
		Name:           name,
		Codec:          codec,
		PayloadType:    DefaultPayloadType,
		SourceLocation: f.SourceLocation,
	}, nil
}

// ReceiverConfig builds the configuration of an incoming stream from the
// parsed flags. The caller fills in the transport endpoints, and may override
// the payload type with the one signalled by the peer.
func (f *Flags) ReceiverConfig(name string) (ReceiverConfig, error) {
	codec, err := mrtp.NewCodec(f.Codec)
	if err != nil {
		return ReceiverConfig{}, err
	}
	// The location selects the sink: empty renders, SinkDiscard drops, and
	// anything else is the file to write to. A file sink can therefore not be
	// asked for without a location.
	return ReceiverConfig{
		Name:         name,
		Codec:        codec,
		PayloadType:  DefaultPayloadType,
		SinkLocation: f.SinkLocation,
	}, nil
}
