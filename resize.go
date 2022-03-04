package space

const (
	// Maximum values of height and width given, aspect ratio preserved.
	ResizeModeFit = 0

	// Minimum values of width and height given, aspect ratio preserved.
	// The image will be cut to fit it exactly.
	ResizeModeFill = 1

	// 	Width and height emphatically given, original aspect ratio ignored.
	ResizeModeStretch = 2
)

type ImageResizer interface {
	Resize(dst, src string, width, height uint, mode int) error
}
