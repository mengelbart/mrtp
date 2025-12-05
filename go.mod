module github.com/mengelbart/mrtp

go 1.25

require (
	github.com/Willi-42/go-nada v0.0.0-20250918135705-6a600030be0b
	github.com/go-gst/go-glib v1.4.0
	github.com/go-gst/go-gst v1.4.0
	github.com/julienschmidt/httprouter v1.3.0
	github.com/mengelbart/moqtransport v0.5.0
	github.com/mengelbart/qlog v0.1.0
	github.com/mengelbart/quicdc v0.0.0-20250910122056-7f9492d1bdd6
	github.com/mengelbart/roq v0.3.1-0.20251205153041-761658450d93
	github.com/pion/bwe v0.0.0-20251016094233-195285a437f6
	github.com/pion/interceptor v0.1.42-0.20251016092317-ce5124bd6cdf
	github.com/pion/rtcp v1.2.16-0.20251011202153-8aedb55aecbf
	github.com/pion/rtp v1.8.23
	github.com/pion/sdp/v2 v2.4.0
	github.com/pion/transport/v3 v3.0.8
	github.com/pion/webrtc/v4 v4.1.4
	github.com/quic-go/quic-go v0.57.1
	github.com/stretchr/testify v1.11.1
	github.com/wlynxg/anet v0.0.5
	golang.org/x/sync v0.16.0
	golang.org/x/time v0.14.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/onsi/ginkgo/v2 v2.22.1 // indirect
	github.com/onsi/gomega v1.36.2 // indirect
	github.com/pion/datachannel v1.5.10 // indirect
	github.com/pion/dtls/v3 v3.0.7 // indirect
	github.com/pion/ice/v4 v4.0.10 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/mdns/v2 v2.0.7 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/sctp v1.8.39 // indirect
	github.com/pion/sdp/v3 v3.0.15 // indirect
	github.com/pion/srtp/v3 v3.0.7 // indirect
	github.com/pion/stun/v3 v3.0.0 // indirect
	github.com/pion/turn/v4 v4.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/exp v0.0.0-20250128182459-e0ece0dbea4c // indirect
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/quic-go/quic-go v0.57.1 => github.com/Willi-42/quic-go v0.7.1
