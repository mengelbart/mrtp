package media

import (
	"flag"
	"testing"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testFactory struct {
	flagName string
	flagVal  bool
}

func (f *testFactory) ConfigureFlags(fs *flag.FlagSet) {
	fs.BoolVar(&f.flagVal, f.flagName, false, "test flag")
}

func (f *testFactory) NewFactory() (Factory, error) { return &testStreamFactory{}, nil }

// multiFlagFactory registers several flags, to check what happens to the good
// ones when a later one is rejected.
type multiFlagFactory struct {
	flagNames []string
}

func (f *multiFlagFactory) ConfigureFlags(fs *flag.FlagSet) {
	for _, name := range f.flagNames {
		fs.Bool(name, false, "test flag")
	}
}

func (f *multiFlagFactory) NewFactory() (Factory, error) { return &testStreamFactory{}, nil }

type testStreamFactory struct{}

func (f *testStreamFactory) Shared() *pipeline.Graph                            { return pipeline.NewGraph() }
func (f *testStreamFactory) NewSender(SenderConfig) (*SendStream, error)        { return nil, nil }
func (f *testStreamFactory) NewReceiver(ReceiverConfig) (*ReceiveStream, error) { return nil, nil }

func TestRegistry(t *testing.T) {
	Register("test-registry", &testFactory{flagName: "test-registry-flag"})

	impl, err := Lookup("test-registry")
	require.NoError(t, err)
	factory, err := impl.NewFactory()
	require.NoError(t, err)
	assert.NotNil(t, factory)

	assert.Contains(t, Names(), "test-registry")

	_, err = Lookup("does-not-exist")
	assert.Error(t, err)
}

func TestConfigureFlagsRegistersImplementationFlags(t *testing.T) {
	Register("test-flags", &testFactory{flagName: "test-flags-flag"})

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	f := &Flags{}
	require.NoError(t, f.ConfigureSender(fs))
	require.NoError(t, f.ConfigureReceiver(fs))

	for _, name := range []string{"media-pipeline", "codec", "source-location", "sink-location", "test-flags-flag"} {
		assert.NotNil(t, fs.Lookup(name), "flag %v not registered", name)
	}
	assert.Equal(t, DefaultPipeline, f.PipelineName)

	// The default source and sink are the empty location.
	assert.Equal(t, SourceTest, f.SourceLocation)
	assert.Equal(t, SinkDisplay, f.SinkLocation)
}

func TestSenderConfig(t *testing.T) {
	f := &Flags{Codec: "vp8", SourceLocation: "in.y4m"}

	config, err := f.SenderConfig("source")
	require.NoError(t, err)
	assert.Equal(t, mrtp.VP8, config.Codec)
	assert.Equal(t, "in.y4m", config.SourceLocation)
	assert.Equal(t, DefaultPayloadType, config.PayloadType)

	f.Codec = "nope"
	_, err = f.SenderConfig("source")
	assert.Error(t, err)
}

func TestSenderConfigSourceSelectedByLocation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location string
		want     string
	}{
		{name: "default", location: "", want: SourceTest},
		{name: "file", location: "in.y4m", want: "in.y4m"},
		{name: "test is not reserved on the source side", location: "test", want: "test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &Flags{Codec: "H264", SourceLocation: tc.location}

			config, err := f.SenderConfig("source")
			require.NoError(t, err)
			assert.Equal(t, tc.want, config.SourceLocation)
		})
	}
}

func TestReceiverConfig(t *testing.T) {
	f := &Flags{Codec: "H264", SinkLocation: "out.y4m"}

	config, err := f.ReceiverConfig("sink")
	require.NoError(t, err)
	assert.Equal(t, mrtp.H264, config.Codec)
	assert.Equal(t, "out.y4m", config.SinkLocation)
	assert.Equal(t, DefaultPayloadType, config.PayloadType)

	f.Codec = "nope"
	_, err = f.ReceiverConfig("sink")
	assert.Error(t, err)
}

func TestReceiverConfigSinkSelectedByLocation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location string
		want     string
	}{
		{name: "default", location: "", want: SinkDisplay},
		{name: "discard", location: SinkDiscard, want: SinkDiscard},
		{name: "file", location: "out.y4m", want: "out.y4m"},
		{name: "file named like the reserved value", location: "./discard", want: "./discard"},
		{name: "display is not reserved", location: "display", want: "display"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &Flags{Codec: "H264", SinkLocation: tc.location}

			config, err := f.ReceiverConfig("sink")
			require.NoError(t, err)
			assert.Equal(t, tc.want, config.SinkLocation)
		})
	}
}

func TestFactoryFlagsMustBePrefixed(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)

	err := registerImplementationFlags(fs, "prefixed", &testFactory{flagName: "prefixed-thing"})
	require.NoError(t, err)
	assert.NotNil(t, fs.Lookup("prefixed-thing"))

	err = registerImplementationFlags(fs, "sloppy", &testFactory{flagName: "thing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prefix")
	assert.Nil(t, fs.Lookup("thing"), "a rejected flag must not be registered")

	// The name alone is not enough, the separator is part of the prefix.
	err = registerImplementationFlags(fs, "sloppy", &testFactory{flagName: "sloppything"})
	assert.Error(t, err)
}

func TestFactoryFlagsRejectDuplicates(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("dup-thing", false, "already taken")

	err := registerImplementationFlags(fs, "dup", &testFactory{flagName: "dup-thing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestFactoryFlagsKeepDefaultsAndBoolShorthand(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	factory := &testFactory{flagName: "kept-thing"}
	require.NoError(t, registerImplementationFlags(fs, "kept", factory))

	registered := fs.Lookup("kept-thing")
	require.NotNil(t, registered)
	assert.Equal(t, "false", registered.DefValue)

	// Registering through flag.Var must not turn a bool into a flag that
	// swallows the next argument.
	require.NoError(t, fs.Parse([]string{"-kept-thing", "leftover"}))
	assert.True(t, factory.flagVal)
	assert.Equal(t, []string{"leftover"}, fs.Args())
}

func TestFactoryFlagsAreAllOrNothing(t *testing.T) {
	// "aaa-ok" sorts before the offending flag, so an implementation that
	// validated while registering would already have added it.
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	err := registerImplementationFlags(fs, "aaa", &multiFlagFactory{flagNames: []string{"aaa-ok", "zzz-no-prefix"}})
	require.Error(t, err)
	assert.Nil(t, fs.Lookup("aaa-ok"), "flags of a rejected implementation must not be registered")
	assert.Nil(t, fs.Lookup("zzz-no-prefix"))

	// Same for a collision that is only discovered on a later flag.
	fs = flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("aaa-taken", false, "already taken")
	err = registerImplementationFlags(fs, "aaa", &multiFlagFactory{flagNames: []string{"aaa-fine", "aaa-taken"}})
	require.Error(t, err)
	assert.Nil(t, fs.Lookup("aaa-fine"), "flags of a rejected implementation must not be registered")
}
