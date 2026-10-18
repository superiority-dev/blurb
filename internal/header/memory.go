package header

import (
	"image"
	"image/color"
	"math"
)

// decodeOverhead is the memory a decode holds whatever the image's size.
const decodeOverhead = 1 << 20 // 1 MiB

// modelDecodeBytes estimates the memory decoding an image of cfg's size holds,
// from its color model alone, for a format without an estimate of its own.
func modelDecodeBytes(cfg image.Config) int64 {
	return decodeBytes(cfg.Width, cfg.Height, modelBytes(cfg.ColorModel))
}

// decodeBytes is the memory decoding a w by h image holds at perPixel bytes a
// pixel, or math.MaxInt64 if that exceeds it.
func decodeBytes(w, h int, perPixel float64) int64 {
	n := math.Ceil(float64(w) * float64(h) * perPixel)
	if n >= math.MaxInt64-decodeOverhead {
		return math.MaxInt64
	}

	return int64(n) + decodeOverhead
}

// modelBytes is the bytes per pixel of a decoded image whose color model is
// model, and 8, the most any image type in package image holds, for a model it
// does not list.
func modelBytes(model color.Model) float64 {
	switch model {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.RGBAModel, color.NRGBAModel:
		return 4
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	}

	if _, ok := model.(color.Palette); ok {
		return 1
	}

	return 8
}
