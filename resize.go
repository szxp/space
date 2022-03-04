package space

const (
	ResizeModeFit     = 0
	ResizeModeFill    = 1
	ResizeModeStretch = 2
)

type ImageResizer interface {
	Resize(dst, src string, width, height uint, mode int) error
}
