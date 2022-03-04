package imagemagick

import (
	"github.com/szxp/space"

	"fmt"
	"os/exec"
)

type ImageResizer struct{}

func (r *ImageResizer) Resize(dst, src string, width, height uint64, mode int8) error {
	var size string
	switch {
	case width > 0 && height > 0:
		size = fmt.Sprintf("%dx%d", width, height)
	case width > 0 && height == 0:
		size = fmt.Sprintf("%d", width)
	case width == 0 && height > 0:
		size = fmt.Sprintf("x%d", height)
	}

	switch mode {
	case space.ResizeModeCover:
		size += "^"
	case space.ResizeModeStretch:
		size += "!"
	}

	args := []string{
		// use only the first frame
		src + "[0]",

		"-resize", size,

		// reads and resets the EXIF image profile setting 'Orientation' and then performs the appropriate 90 degree rotation on the image to orient the image, for correct viewing
		"-auto-orient",

		// removes any ICM, EXIF, IPTC, or other profiles that might be present in the input and aren't needed in the thumbnail.
		//"+profile", "\"*\"",

		"-strip",
		"-quality", "75",
	}

	if mode == space.ResizeModeCover {
		args = append(args,
			"-gravity", "center",
			"-crop", fmt.Sprintf("%dx%d+0+0", width, height),
			// completely remove/reset the virtual canvas meta-data from the images.
			"+repage",
		)
	}

	args = append(args, dst)

	_, err := exec.Command("convert", args...).Output()
	if err != nil {
		return fmt.Errorf("Failed to create thumbnail: %w", err)
	}
	return nil
}

func Version() (string, error) {
	ver, err := exec.Command("convert", "-version").Output()
	if err != nil {
		return "", err
	}
	return string(ver), nil
}
