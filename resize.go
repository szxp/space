package space

const (
	// Maximum values of height and width given, aspect ratio preserved.
	ResizeModeFit = 1

	// Minimum values of width and height given, aspect ratio preserved.
	// The image will be cut to fit it exactly.
	ResizeModeCover = 2

	// 	Width and height emphatically given, original aspect ratio ignored.
	ResizeModeStretch = 3
)

type ImageResizer interface {
	Resize(dst, src string, width, height uint64, mode int8) error
}
