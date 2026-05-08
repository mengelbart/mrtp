package webrtc

import "github.com/pion/rtp"

type EcnMap map[RtpID]uint8

type RtpID struct {
	SSRC           uint32
	SequenceNumber uint16
}

func GetRTPidFromPacket(pktBuf []byte) (RtpID, error) {
	pkt := rtp.Packet{}
	if err := pkt.Unmarshal(pktBuf); err != nil {
		return RtpID{}, err
	}

	return RtpID{
		SSRC:           pkt.Header.SSRC,
		SequenceNumber: pkt.Header.SequenceNumber,
	}, nil
}
