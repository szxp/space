package imagemagick

import (
	"fmt"
	"os/exec"
	//"strconv"
	//"strings"
)

type ImageResizer struct{}

func (r *ImageResizer) Resize(dst, src string, width, height uint, mode int) error {
	var size string
	switch {
	case width > 0 && height > 0:
		size = fmt.Sprintf("%dx%d", width, height)
	case width > 0 && height == 0:
		size = fmt.Sprintf("%d", width)
	case width == 0 && height > 0:
		size = fmt.Sprintf("x%d", height)
	}
	//if cover {
	//	size += "^"
	//}

	args := []string{
		// use only the first frame
		src + "[0]",

		"-resize", size,

		// reads and resets the EXIF image profile setting 'Orientation' and then performs the appropriate 90 degree rotation on the image to orient the image, for correct viewing
		"-auto-orient",

		// removes any ICM, EXIF, IPTC, or other profiles that might be present in the input and aren't needed in the thumbnail.
		//"+profile", "\"*\"",

		"-quality", "75",
		"-strip",
	}

	/*
		if cover {
			crop := fmt.Sprintf("%dx%d+0+0", width, height)
			args = append(args,
				"-gravity", "center",
				"-crop", crop,
				"+repage", // completely remove/reset the virtual canvas meta-data from the images.
			)
		}
	*/

	args = append(args, dst)

	//fmt.Println(args)

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
