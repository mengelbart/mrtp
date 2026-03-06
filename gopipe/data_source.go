package gopipe

type MediaFrameSource interface {
	FrameRate() (numerator uint, denominator uint)
	ReadFrame(targetSize uint) ([]byte, error)
}
