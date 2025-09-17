module github.com/mengelbart/mrtp

go 1.24.1

require (
	github.com/Willi-42/go-nada v0.0.0-20250917150156-abed9a73c328
	github.com/go-gst/go-glib v1.4.0
	github.com/go-gst/go-gst v1.4.0
	github.com/julienschmidt/httprouter v1.3.0
	github.com/mengelbart/qlog v0.0.0-20250304144032-13728b0b6fae
	github.com/mengelbart/quicdc v0.0.0-20250910122056-7f9492d1bdd6
	github.com/mengelbart/roq v0.3.1-0.20250904124336-5b57f8dff945
	github.com/pion/bwe-test v0.0.0-20250731151800-5362b62c9a75
	github.com/pion/interceptor v0.1.41-0.20250731115150-266b5e5ce04a
	github.com/pion/rtcp v1.2.16-0.20250423150132-b78b08322f5c
	github.com/pion/rtp v1.8.21
	github.com/pion/webrtc/v4 v4.1.1
	github.com/quic-go/quic-go v0.54.0
	github.com/stretchr/testify v1.10.0
	golang.org/x/sync v0.16.0
	golang.org/x/time v0.0.0-20181108054448-85acf8d2951c
)

require github.com/onsi/gomega v1.27.6 // indirect

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/francoispqt/gojay v1.2.13 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/onsi/ginkgo/v2 v2.9.5 // indirect
	github.com/pion/datachannel v1.5.10 // indirect
	github.com/pion/dtls/v3 v3.0.6 // indirect
	github.com/pion/ice/v4 v4.0.10 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/mdns/v2 v2.0.7 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/sctp v1.8.39 // indirect
	github.com/pion/sdp/v2 v2.4.0
	github.com/pion/sdp/v3 v3.0.14 // indirect
	github.com/pion/srtp/v3 v3.0.6 // indirect
	github.com/pion/stun/v3 v3.0.0 // indirect
	github.com/pion/transport/v3 v3.0.7 // indirect
	github.com/pion/turn/v4 v4.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/quic-go/qpack v0.5.1 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/exp v0.0.0-20240909161429-701f63a606c0 // indirect
	golang.org/x/mod v0.27.0 // indirect
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	golang.org/x/tools v0.36.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/quic-go/quic-go v0.54.0 => github.com/Willi-42/quic-go v0.3.0
