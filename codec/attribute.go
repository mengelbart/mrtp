package codec

import (
	"fmt"
	"image"
	"time"
)

type AttributeKey int

const (
	ChromaSubsampling AttributeKey = iota
	IsKeyFrame
	SampleDuration
	Width
	Height
	PTS
	FrameDuration
)

type Attributes map[any]any

// Getter functions for attributes

func getPTS(attrs Attributes) (int64, error) {
	ptsAttr, ok := attrs[PTS]
	if !ok {
		return 0, fmt.Errorf("PTS attribute not found")
	}
	ptsVal, ok := ptsAttr.(int64)
	if !ok {
		return 0, fmt.Errorf("PTS attribute is not int64")
	}
	return ptsVal, nil
}

func getFrameDuration(attrs Attributes) (time.Duration, error) {
	fdAttr, ok := attrs[FrameDuration]
	if !ok {
		return 0, fmt.Errorf("FrameDuration attribute not found")
	}
	fdVal, ok := fdAttr.(time.Duration)
	if !ok {
		return 0, fmt.Errorf("FrameDuration attribute is not time.Duration")
	}
	return fdVal, nil
}

func getChromaSubsampling(attrs Attributes) (image.YCbCrSubsampleRatio, error) {
	csAttr, ok := attrs[ChromaSubsampling]
	if !ok {
		return 0, fmt.Errorf("ChromaSubsampling attribute not found")
	}
	csVal, ok := csAttr.(image.YCbCrSubsampleRatio)
	if !ok {
		return 0, fmt.Errorf("ChromaSubsampling attribute is not image.YCbCrSubsampleRatio")
	}
	return csVal, nil
}

func getWidth(attrs Attributes) (int, error) {
	widthAttr, ok := attrs[Width]
	if !ok {
		return 0, fmt.Errorf("Width attribute not found")
	}
	widthVal, ok := widthAttr.(int)
	if !ok {
		return 0, fmt.Errorf("Width attribute is not int")
	}
	return widthVal, nil
}

func getHeight(attrs Attributes) (int, error) {
	heightAttr, ok := attrs[Height]
	if !ok {
		return 0, fmt.Errorf("Height attribute not found")
	}
	heightVal, ok := heightAttr.(int)
	if !ok {
		return 0, fmt.Errorf("Height attribute is not int")
	}
	return heightVal, nil
}
